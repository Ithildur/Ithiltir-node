package push

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/reportcfg"
	"Ithiltir-node/internal/selfupdate"
)

const metricsTimeout = 7 * time.Second

var (
	staticRetryDelay             = 10 * time.Second
	staticChangeCheckMinInterval = 10 * time.Second
	staticChangeCheckMaxInterval = time.Minute
	applyUpdate                  = selfupdate.Apply
)

type source interface {
	Snapshot() *metrics.Snapshot
	Time() time.Time
	Version() string
	Hostname() string
	PushDelay() time.Duration
}

type staticSource interface {
	Static() *metrics.Static
}

type logLimiter struct {
	cooldown time.Duration
	last     time.Time
}

func (l *logLimiter) logf(format string, args ...any) {
	now := time.Now()
	if now.Sub(l.last) < l.cooldown {
		return
	}
	l.last = now
	log.Printf(format, args...)
}

func isPlainHTTP(err error) bool {
	if _, ok := errors.AsType[*tls.RecordHeaderError](err); ok {
		return true
	}

	msg := err.Error()
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		msg = urlErr.Err.Error()
	}
	if strings.Contains(msg, "http: server gave HTTP response to HTTPS client") {
		return true
	}
	if strings.Contains(msg, "tls: first record does not look like a TLS handshake") {
		return true
	}
	return false
}

func isCertError(err error) bool {
	if _, ok := errors.AsType[x509.UnknownAuthorityError](err); ok {
		return true
	}
	if _, ok := errors.AsType[x509.HostnameError](err); ok {
		return true
	}
	_, ok := errors.AsType[x509.CertificateInvalidError](err)
	return ok
}

func isExpiredCert(err error) bool {
	if certInvalid, ok := errors.AsType[x509.CertificateInvalidError](err); ok {
		return certInvalid.Reason == x509.Expired
	}
	return false
}

func allowExpiredCertFallback(host, port string) bool {
	addr := net.JoinHostPort(host, port)
	d := &net.Dialer{Timeout: 5 * time.Second}
	cfg := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         host,
	}

	conn, err := tls.DialWithDialer(d, "tcp", addr, cfg)
	if err != nil {
		return false
	}
	defer conn.Close()

	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return false
	}
	leaf := state.PeerCertificates[0]
	if time.Now().Before(leaf.NotAfter) {
		return false
	}

	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	inter := x509.NewCertPool()
	for _, cert := range state.PeerCertificates[1:] {
		inter.AddCert(cert)
	}

	verifyTime := leaf.NotAfter.Add(-time.Second)
	if verifyTime.Before(leaf.NotBefore) {
		verifyTime = leaf.NotBefore.Add(time.Second)
	}
	opts := x509.VerifyOptions{
		DNSName:       host,
		Roots:         roots,
		Intermediates: inter,
		CurrentTime:   verifyTime,
	}
	if _, err := leaf.Verify(opts); err != nil {
		return false
	}
	return true
}

type agent struct {
	source           source
	staticSource     staticSource
	hostName         string
	debug            bool
	cache            *Cache
	targets          []*target
	roundErrLimiter  logLimiter
	staticWG         sync.WaitGroup
	staticMu         sync.Mutex
	staticFP         [32]byte
	staticFPSet      bool
	updateNoopLogged bool
}

type target struct {
	mu           sync.RWMutex
	client       *http.Client
	id           int
	endpoint     *url.URL
	hostIsIP     bool
	secret       string
	requireHTTPS bool
	staticWake   chan string
	rpc          rpcTarget

	staticErrLimiter      logLimiter
	errLimiter            logLimiter
	connRefusedCount      int
	connRefusedSuppressed bool
	rpcFailed             bool
}

func newTarget(spec reportcfg.Target, requireHTTPS bool) (*target, error) {
	mode, err := transportMode()
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(strings.TrimSpace(spec.URL))
	if err != nil {
		return nil, fmt.Errorf("parse target %d url: %w", spec.ID, err)
	}
	if requireHTTPS && endpoint.Scheme != "https" {
		return nil, fmt.Errorf("target %d url must use https when require_https is enabled", spec.ID)
	}
	host := endpoint.Hostname()
	if host == "" {
		return nil, fmt.Errorf("target %d host is required", spec.ID)
	}
	return &target{
		rpc:              rpcTarget{mode: mode},
		client:           &http.Client{Timeout: 10 * time.Second},
		id:               spec.ID,
		endpoint:         endpoint,
		hostIsIP:         net.ParseIP(host) != nil,
		secret:           spec.Key,
		requireHTTPS:     requireHTTPS,
		staticWake:       make(chan string, 1),
		staticErrLimiter: logLimiter{cooldown: time.Minute},
		errLimiter:       logLimiter{cooldown: time.Minute},
	}, nil
}

func (t *target) hostType() string {
	if t.hostIsIP {
		return "ip"
	}
	return "domain"
}

func (t *target) metricsURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.endpoint.String()
}

func (t *target) staticURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !strings.HasSuffix(t.endpoint.Path, "/metrics") {
		return ""
	}
	endpoint := *t.endpoint
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/metrics") + "/static"
	return endpoint.String()
}

func (t *target) identityURL() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !strings.HasSuffix(t.endpoint.Path, "/metrics") {
		return ""
	}
	endpoint := *t.endpoint
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/metrics") + "/identity"
	return endpoint.String()
}

func (t *target) endpointParts() (scheme string, host string, port string, hostPort string) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	scheme = t.endpoint.Scheme
	host = t.endpoint.Hostname()
	port = t.endpoint.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	hostPort = t.endpoint.Host
	return scheme, host, port, hostPort
}

func (t *target) fallbackHTTP(err error) bool {
	// Automatic negotiation never permits a TLS-to-plaintext downgrade.
	// Explicit HTTP mode retains the existing legacy HTTP fallback policy.
	if t.rpc.mode != "http" {
		return false
	}
	scheme, host, port, hostPort := t.endpointParts()
	if scheme != "https" || t.requireHTTPS {
		return false
	}
	allowed := isPlainHTTP(err) || t.hostIsIP && isCertError(err)
	if !allowed && !t.hostIsIP && isExpiredCert(err) {
		allowed = allowExpiredCertFallback(host, port)
		if !allowed {
			log.Printf("ERROR: push target %d https refused fallback, cert expired but chain invalid for %s", t.id, hostPort)
		}
	}
	if !allowed {
		return false
	}
	t.mu.Lock()
	t.endpoint.Scheme = "http"
	hostPort = t.endpoint.Host
	t.mu.Unlock()
	log.Printf("WARN: push target %d https failed, fallback to http for %s", t.id, hostPort)
	return true
}

func (t *target) wakeStatic(reason string) {
	select {
	case t.staticWake <- reason:
	default:
	}
}

func (t *target) sendStatic(ctx context.Context, endpoint string, snap *metrics.Static) error {
	client, err := t.rpcClient(ctx)
	if err != nil {
		return err
	}
	if client != nil {
		return t.rpcStatic(ctx, client, snap)
	}
	return sendStatic(ctx, t.client, endpoint, t.secret, snap)
}

func (a *agent) runStatic(ctx context.Context, target *target) {
	timer := time.NewTimer(staticRetryDelay)
	timer.Stop()
	defer timer.Stop()
	retry := a.syncStatic(ctx, target, "startup")
	for {
		var retryCh <-chan time.Time
		if retry {
			timer.Reset(staticRetryDelay)
			retryCh = timer.C
		}

		select {
		case reason := <-target.staticWake:
			timer.Stop()
			retry = a.syncStatic(ctx, target, reason)
		case <-retryCh:
			retry = a.syncStatic(ctx, target, "fallback")
		case <-ctx.Done():
			return
		}
	}
}

func (a *agent) syncStatic(ctx context.Context, target *target, reason string) bool {
	snap := a.staticSource.Static()
	if err := validateStatic(snap); err != nil {
		target.staticErrLimiter.logf("push static error target=%d (%s): %v", target.id, reason, err)
		return true
	}
	missing := missingCompleteFields(snap)

	endpoint := target.staticURL()
	if endpoint == "" {
		return false
	}

	err := target.sendStatic(ctx, endpoint, snap)
	if err != nil {
		target.staticErrLimiter.logf("push static error target=%d (%s): %v", target.id, reason, err)
		if target.fallbackHTTP(err) {
			endpoint = target.staticURL()
			err = target.sendStatic(ctx, endpoint, snap)
			if err != nil {
				target.staticErrLimiter.logf("push static error target=%d (%s): %v", target.id, reason, err)
			}
		}
	}

	if err == nil {
		if a.debug {
			if len(missing) == 0 {
				log.Printf("push static ok target=%d (%s)", target.id, reason)
			} else {
				log.Printf("push static partial ok target=%d (%s), missing=%s", target.id, reason, strings.Join(missing, ", "))
			}
		}
		a.recordStaticFingerprint(snap)
	}
	return err != nil || len(missing) > 0
}

func staticChangeCheckInterval(reportInterval time.Duration) time.Duration {
	if reportInterval >= staticChangeCheckMaxInterval {
		return staticChangeCheckMaxInterval
	}
	return min(staticChangeCheckMaxInterval, max(staticChangeCheckMinInterval, reportInterval*20))
}

func newAgent(specs []reportcfg.Target, interval time.Duration, s source, debug bool, requireHTTPS bool, cache *Cache) (*agent, error) {
	hostName := s.Hostname()
	if hostName == "" {
		hostName = "unknown"
	}

	static, _ := s.(staticSource)
	targets := make([]*target, 0, len(specs))
	for _, spec := range specs {
		target, err := newTarget(spec, requireHTTPS)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	a := &agent{
		source:          s,
		staticSource:    static,
		hostName:        hostName,
		debug:           debug,
		cache:           cache,
		targets:         targets,
		roundErrLimiter: logLimiter{cooldown: time.Minute},
	}

	log.Printf("Ithiltir-node (push mode) targets=%d, interval=%s, node_id=%s, require_https=%t", len(targets), interval, a.hostName, requireHTTPS)
	for _, target := range targets {
		log.Printf("Ithiltir-node (push mode) target=%d url=%s host_type=%s", target.id, target.metricsURL(), target.hostType())
	}
	if len(targets) == 0 {
		log.Printf("Ithiltir-node (push mode) no report targets configured")
	}

	return a, nil
}

func (a *agent) buildReport() (*metrics.NodeReport, []byte, error) {
	m := a.source.Snapshot()
	if m == nil {
		return nil, nil, nil
	}
	report := &metrics.NodeReport{
		Version:   a.source.Version(),
		Hostname:  a.hostName,
		Timestamp: a.source.Time(),
		Metrics:   m,
	}
	body, err := json.Marshal(report)
	if err != nil {
		return nil, nil, err
	}
	return report, body, nil
}

func sendReport(ctx context.Context, target *target, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", target.metricsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", target.secret)

	return target.client.Do(req)
}

type Identity struct {
	TargetID  int
	URL       string
	InstallID string
	Created   bool
}

type identityResponse struct {
	InstallID string `json:"install_id"`
	Created   bool   `json:"created"`
}

func FetchIdentity(ctx context.Context, spec reportcfg.Target, requireHTTPS bool) (Identity, error) {
	target, err := newTarget(spec, requireHTTPS)
	if err != nil {
		return Identity{}, err
	}
	defer target.closeRPC()
	client, err := target.rpcClient(ctx)
	if err != nil {
		return Identity{}, err
	}
	if client != nil {
		target.rpc.mu.Lock()
		identity := target.rpc.identity
		target.rpc.mu.Unlock()
		if identity == nil {
			callCtx, cancel := rpcTimeout(ctx, target.secret)
			identity, err = client.Identify(callCtx, &nodewire.Empty{})
			cancel()
			if err != nil {
				return Identity{}, err
			}
		}
		installID, err := reportcfg.NormalizeServerInstallID(identity.InstallId)
		if err != nil || identity.ProtocolVersion != 1 {
			return Identity{}, fmt.Errorf("invalid RPC identity")
		}
		return Identity{TargetID: target.id, URL: target.metricsURL(), InstallID: installID, Created: identity.Created}, nil
	}
	resp, err := sendIdentity(ctx, target)
	if err != nil {
		if target.fallbackHTTP(err) {
			resp, err = sendIdentity(ctx, target)
		}
		if err != nil {
			return Identity{}, err
		}
	}
	defer drainBody(resp)
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("identity non-200 status: %s", resp.Status)
	}
	var body identityResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Identity{}, fmt.Errorf("decode identity response: %w", err)
	}
	installID, err := reportcfg.NormalizeServerInstallID(body.InstallID)
	if err != nil {
		return Identity{}, fmt.Errorf("invalid identity response: %w", err)
	}
	return Identity{
		TargetID:  target.id,
		URL:       target.metricsURL(),
		InstallID: installID,
		Created:   body.Created,
	}, nil
}

func sendIdentity(ctx context.Context, target *target) (*http.Response, error) {
	endpoint := target.identityURL()
	if endpoint == "" {
		return nil, fmt.Errorf("target %d url must end with /metrics to resolve identity", target.id)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", target.secret)
	return target.client.Do(req)
}

func (t *target) handleError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if t.fallbackHTTP(err) {
		return nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		t.connRefusedCount++
		if !t.connRefusedSuppressed && t.connRefusedCount >= 3 {
			log.Printf("push target %d error: 对端端口未打开", t.id)
			t.connRefusedSuppressed = true
			return nil
		}
		if !t.connRefusedSuppressed {
			t.errLimiter.logf("push target %d error: %v", t.id, err)
		}
		return nil
	}
	t.errLimiter.logf("push target %d error: %v", t.id, err)
	return nil
}

func drainBody(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

type metricsResponse struct {
	Update *selfupdate.Manifest `json:"update"`
}

func sameNodeVersion(updateVersion, currentVersion string) bool {
	updateVersion = strings.TrimSpace(updateVersion)
	currentVersion = strings.TrimSpace(currentVersion)
	return updateVersion != "" && updateVersion == currentVersion
}

type updatePlan struct {
	manifest       *selfupdate.Manifest
	conflict       bool
	skippedCurrent bool
}

func (p *updatePlan) add(currentVersion string, manifest *selfupdate.Manifest, secret string) {
	if manifest == nil {
		return
	}
	if sameNodeVersion(manifest.Version, currentVersion) {
		p.skippedCurrent = true
		return
	}
	if p.manifest == nil {
		selected := *manifest
		selected.Secret = strings.TrimSpace(secret)
		p.manifest = &selected
		return
	}
	if !sameUpdateManifest(p.manifest, manifest) {
		p.conflict = true
	}
}

func sameUpdateManifest(a, b *selfupdate.Manifest) bool {
	return a.ID == b.ID &&
		a.Version == b.Version &&
		a.URL == b.URL &&
		a.SHA256 == b.SHA256 &&
		a.Size == b.Size
}

func (t *target) handleResponse(resp *http.Response, debug bool) (bool, bool, *selfupdate.Manifest) {
	defer drainBody(resp)

	if resp.StatusCode != http.StatusOK {
		t.errLimiter.logf("push target %d non-200 status: %s", t.id, resp.Status)
		return false, false, nil
	}

	manifest := decodeMetricsResponse(resp)
	recoverStatic := t.connRefusedSuppressed
	if t.connRefusedSuppressed {
		log.Printf("push target %d recovered: 对端已恢复", t.id)
	}
	t.connRefusedSuppressed = false
	t.connRefusedCount = 0
	if debug {
		log.Printf("push target %d ok: %s", t.id, resp.Status)
	}
	return true, recoverStatic, manifest
}

func decodeMetricsResponse(resp *http.Response) *selfupdate.Manifest {
	contentType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(contentType, "application/json") {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		log.Printf("push metrics response read failed: %v", err)
		return nil
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "ok" {
		return nil
	}

	var parsed metricsResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		log.Printf("push metrics response ignored: invalid JSON: %v", err)
		return nil
	}
	return parsed.Update
}

func StartWithCache(ctx context.Context, targets []reportcfg.Target, interval time.Duration, s source, debug bool, requireHTTPS bool, cache *Cache) error {
	if interval <= 0 {
		return fmt.Errorf("push interval must be positive")
	}
	agent, err := newAgent(targets, interval, s, debug, requireHTTPS, cache)
	if err != nil {
		return err
	}
	runCtx, stop := context.WithCancel(ctx)
	virtWG := startVirt(runCtx, agent.targets)
	sessionWG := agent.startSessions(runCtx)
	defer func() {
		stop()
		virtWG.Wait()
		sessionWG.Wait()
		agent.staticWG.Wait()
		for _, target := range agent.targets {
			target.closeRPC()
		}
	}()

	if d := s.PushDelay(); d > 0 {
		select {
		case <-time.After(d):
		case <-runCtx.Done():
			return runCtx.Err()
		}
	}

	agent.startStatic(runCtx, interval)

	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			started := time.Now()
			retry, err := agent.sendRound(runCtx)
			if err != nil {
				return err
			}
			delay := interval
			if retry {
				delay = 0
			}
			// Keep the send cadence without queueing snapshots behind a slow request.
			timer.Reset(max(0, delay-time.Since(started)))
		case <-runCtx.Done():
			return runCtx.Err()
		}
	}
}

type targetResult struct {
	ok       bool
	retry    bool
	manifest *selfupdate.Manifest
	err      error
	secret   string
}

func (a *agent) sendRound(ctx context.Context) (bool, error) {
	if len(a.targets) == 0 {
		return false, nil
	}

	report, body, err := a.buildReport()
	if err != nil {
		return false, err
	}
	if report == nil {
		return false, nil
	}

	results := make(chan targetResult, len(a.targets))
	var wg sync.WaitGroup
	for _, target := range a.targets {
		wg.Go(func() {
			callCtx, cancel := context.WithTimeout(ctx, metricsTimeout)
			defer cancel()
			ok, manifest, err := a.sendTarget(callCtx, target, body)
			timedOut := ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded)
			if timedOut {
				ok, err = false, nil
				target.errLimiter.logf("push target %d timeout: %v", target.id, callCtx.Err())
			}
			results <- targetResult{ok: ok, retry: timedOut, manifest: manifest, err: err, secret: target.secret}
		})
	}
	wg.Wait()
	close(results)

	success := false
	retry := false
	var update updatePlan
	currentVersion := report.Version
	for result := range results {
		if result.err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			return false, result.err
		}
		success = success || result.ok
		retry = retry || result.retry
		update.add(currentVersion, result.manifest, result.secret)
	}
	if update.conflict {
		a.roundErrLimiter.logf("push update skipped: conflicting manifests in one round")
	} else if update.skippedCurrent && update.manifest == nil {
		if !a.updateNoopLogged {
			log.Printf("push update skipped: version=%s already active", strings.TrimSpace(currentVersion))
			a.updateNoopLogged = true
		}
	} else if update.manifest != nil {
		if err := applyUpdate(ctx, *update.manifest); err != nil {
			if errors.Is(err, selfupdate.ErrRestart) {
				log.Printf("node update staged: version=%s", update.manifest.Version)
				return false, selfupdate.ErrRestart
			}
			a.roundErrLimiter.logf("push update failed: %v", err)
		}
	}
	if success {
		if a.cache != nil {
			a.cache.Set(report)
		}
		return retry, nil
	}

	a.roundErrLimiter.logf("push round failed: all %d targets failed", len(a.targets))
	return retry, nil
}

func (a *agent) sendTarget(ctx context.Context, target *target, body []byte) (bool, *selfupdate.Manifest, error) {
	client, err := target.rpcClient(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return false, nil, ctx.Err()
		}
		target.errLimiter.logf("push target %d transport: %v", target.id, err)
		return false, nil, nil
	}
	if client != nil {
		manifest, err := target.rpcMetrics(ctx, client, body)
		if err != nil {
			target.rpcFailed = true
			if ctx.Err() != nil {
				return false, nil, ctx.Err()
			}
			target.errLimiter.logf("push target %d RPC: %v", target.id, err)
			return false, nil, nil
		}
		if target.rpcFailed {
			target.rpcFailed = false
			target.wakeStatic("recovery")
		}
		if a.debug {
			log.Printf("push target %d RPC ok", target.id)
		}
		return true, manifest, nil
	}
	resp, err := sendReport(ctx, target, body)
	if err != nil {
		if err := target.handleError(ctx, err); err != nil {
			return false, nil, err
		}
		return false, nil, nil
	}
	ok, recoverStatic, manifest := target.handleResponse(resp, a.debug)
	if recoverStatic {
		target.wakeStatic("recovery")
	}
	return ok, manifest, nil
}

func (a *agent) startStatic(ctx context.Context, interval time.Duration) {
	if a.staticSource == nil || len(a.targets) == 0 {
		return
	}
	for _, target := range a.targets {
		a.staticWG.Go(func() {
			a.runStatic(ctx, target)
		})
	}

	a.staticWG.Go(func() {
		a.watchStaticChanges(ctx, staticChangeCheckInterval(interval))
	})
}

func (a *agent) recordStaticFingerprint(snap *metrics.Static) {
	fp, ok, err := staticFingerprint(snap)
	if err != nil {
		a.roundErrLimiter.logf("static fingerprint error: %v", err)
		return
	}
	if !ok {
		return
	}

	a.staticMu.Lock()
	a.staticFP = fp
	a.staticFPSet = true
	a.staticMu.Unlock()
}

func (a *agent) staticChanged() (bool, error) {
	fp, ok, err := staticFingerprint(a.staticSource.Static())
	if err != nil || !ok {
		return false, err
	}

	a.staticMu.Lock()
	defer a.staticMu.Unlock()
	if !a.staticFPSet {
		a.staticFP = fp
		a.staticFPSet = true
		return false, nil
	}
	if a.staticFP == fp {
		return false, nil
	}
	a.staticFP = fp
	return true, nil
}

func (a *agent) wakeStatic(reason string) {
	for _, target := range a.targets {
		target.wakeStatic(reason)
	}
}

func (a *agent) watchStaticChanges(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			changed, err := a.staticChanged()
			if err != nil {
				a.roundErrLimiter.logf("static change check failed: %v", err)
				continue
			}
			if changed {
				a.wakeStatic("static_changed")
			}
		case <-ctx.Done():
			return
		}
	}
}

func sendStatic(ctx context.Context, client *http.Client, endpoint, secret string, snap *metrics.Static) error {
	body, err := json.Marshal(snap)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", secret)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("static non-200 status: %s", resp.Status)
	}
	return nil
}

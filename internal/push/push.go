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
	"Ithiltir-node/internal/nodeiface"
	"Ithiltir-node/internal/reportcfg"
	"Ithiltir-node/internal/selfupdate"
)

var staticRetryDelay = 10 * time.Second

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

func isConnRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

func isPlainHTTP(err error) bool {
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}

	var recordErr *tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}

	msg := err.Error()
	if strings.Contains(msg, "http: server gave HTTP response to HTTPS client") {
		return true
	}
	if strings.Contains(msg, "tls: first record does not look like a TLS handshake") {
		return true
	}
	return false
}

func isCertError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}

	var unknownAuth x509.UnknownAuthorityError
	if errors.As(err, &unknownAuth) {
		return true
	}
	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return true
	}
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &certInvalid) {
		return true
	}
	return false
}

func isExpiredCert(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		err = urlErr.Err
	}
	var certInvalid x509.CertificateInvalidError
	if errors.As(err, &certInvalid) {
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

type fallbackDecision struct {
	useHTTP        bool
	expiredRefused bool
}

func stopTimer(t *time.Timer) {
	if t == nil {
		return
	}
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

type agent struct {
	source          nodeiface.PushSource
	hostName        string
	debug           bool
	cache           *Cache
	targets         []*target
	roundErrLimiter logLimiter
	staticWG        sync.WaitGroup
}

type target struct {
	mu           sync.RWMutex
	client       *http.Client
	id           int
	endpoint     *url.URL
	hostIsIP     bool
	secret       string
	requireHTTPS bool
	static       *staticSync
	staticWake   chan string
	delivery     *delivery
}

func newTarget(spec reportcfg.Target, staticSource nodeiface.StaticSource, requireHTTPS bool) (*target, error) {
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
		client:       &http.Client{Timeout: 10 * time.Second},
		id:           spec.ID,
		endpoint:     endpoint,
		hostIsIP:     net.ParseIP(host) != nil,
		secret:       spec.Key,
		requireHTTPS: requireHTTPS,
		static:       newStaticSync(staticSource),
		staticWake:   make(chan string, 1),
		delivery:     newDelivery(),
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

func (t *target) httpFallback(err error) fallbackDecision {
	scheme, host, port, _ := t.endpointParts()
	if scheme != "https" || t.requireHTTPS {
		return fallbackDecision{}
	}
	if isPlainHTTP(err) {
		return fallbackDecision{useHTTP: true}
	}
	if t.hostIsIP && isCertError(err) {
		return fallbackDecision{useHTTP: true}
	}
	if !t.hostIsIP && isExpiredCert(err) {
		if allowExpiredCertFallback(host, port) {
			return fallbackDecision{useHTTP: true}
		}
		return fallbackDecision{expiredRefused: true}
	}
	return fallbackDecision{}
}

func (t *target) fallbackHTTP(err error) bool {
	decision := t.httpFallback(err)
	if decision.expiredRefused {
		_, _, _, hostPort := t.endpointParts()
		log.Printf("ERROR: push target %d https refused fallback, cert expired but chain invalid for %s", t.id, hostPort)
	}
	if !decision.useHTTP {
		return false
	}
	t.mu.Lock()
	t.endpoint.Scheme = "http"
	hostPort := t.endpoint.Host
	t.mu.Unlock()
	log.Printf("WARN: push target %d https failed, fallback to http for %s", t.id, hostPort)
	return true
}

func (t *target) wakeStatic(reason string) {
	if t.static == nil || t.static.source == nil {
		return
	}
	select {
	case t.staticWake <- reason:
	default:
	}
}

type staticSync struct {
	source     nodeiface.StaticSource
	errLimiter logLimiter
}

type staticState uint8

const (
	staticRetry staticState = iota
	staticPartial
	staticComplete
)

func newStaticSync(source nodeiface.StaticSource) *staticSync {
	return &staticSync{
		source:     source,
		errLimiter: logLimiter{cooldown: time.Minute},
	}
}

func (s *staticSync) prepare() (*metrics.Static, staticState, []string, error) {
	if s.source == nil {
		return nil, staticRetry, nil, nil
	}

	snap := s.source.Static()
	if err := validateStatic(snap); err != nil {
		return nil, staticRetry, nil, err
	}
	missing := missingCompleteFields(snap)
	if len(missing) == 0 {
		return snap, staticComplete, missing, nil
	}
	return snap, staticPartial, missing, nil
}

func (s *staticSync) send(ctx context.Context, target *target, endpoint string, debug bool, reason string, snap *metrics.Static, state staticState, missing []string) error {
	if err := sendStatic(ctx, target.client, endpoint, target.secret, snap); err != nil {
		return err
	}
	if debug {
		if state == staticComplete {
			log.Printf("push static ok target=%d (%s)", target.id, reason)
		} else {
			log.Printf("push static partial ok target=%d (%s), missing=%s", target.id, reason, strings.Join(missing, ", "))
		}
	}
	return nil
}

func (s *staticSync) run(ctx context.Context, target *target, debug bool) {
	if s.source == nil {
		return
	}

	retry := s.sync(ctx, target, debug, "startup")
	for {
		var retryCh <-chan time.Time
		var retryTimer *time.Timer
		if retry {
			retryTimer = time.NewTimer(staticRetryDelay)
			retryCh = retryTimer.C
		}

		select {
		case reason := <-target.staticWake:
			stopTimer(retryTimer)
			retry = s.sync(ctx, target, debug, reason)
		case <-retryCh:
			retry = s.sync(ctx, target, debug, "fallback")
		case <-ctx.Done():
			stopTimer(retryTimer)
			return
		}
	}
}

func (s *staticSync) sync(ctx context.Context, target *target, debug bool, reason string) bool {
	snap, state, missing, err := s.prepare()
	if err != nil {
		s.errLimiter.logf("push static error target=%d (%s): %v", target.id, reason, err)
		return true
	}

	endpoint := target.staticURL()
	if endpoint == "" {
		return false
	}

	err = s.send(ctx, target, endpoint, debug, reason, snap, state, missing)
	if err != nil {
		s.errLimiter.logf("push static error target=%d (%s): %v", target.id, reason, err)
		if target.fallbackHTTP(err) {
			endpoint = target.staticURL()
			err = s.send(ctx, target, endpoint, debug, reason, snap, state, missing)
			if err != nil {
				s.errLimiter.logf("push static error target=%d (%s): %v", target.id, reason, err)
			}
		}
	}

	return err != nil || state != staticComplete
}

type delivery struct {
	errLimiter logLimiter

	connRefusedCount      int
	connRefusedSuppressed bool
}

func newDelivery() *delivery {
	return &delivery{
		errLimiter: logLimiter{cooldown: time.Minute},
	}
}

func newAgent(specs []reportcfg.Target, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool, cache *Cache) (*agent, error) {
	hostName := s.Hostname()
	if hostName == "" {
		hostName = "unknown"
	}

	staticSource, _ := s.(nodeiface.StaticSource)
	targets := make([]*target, 0, len(specs))
	for _, spec := range specs {
		target, err := newTarget(spec, staticSource, requireHTTPS)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	a := &agent{
		source:          s,
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
	report := metrics.NewNodeReport(a.source.Version(), a.hostName, a.source.Time(), m)
	body, err := json.Marshal(report)
	if err != nil {
		return nil, nil, err
	}
	return &report, body, nil
}

func sendReport(ctx context.Context, target *target, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", target.metricsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", target.secret)

	resp, err := target.client.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
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
	target, err := newTarget(spec, nil, requireHTTPS)
	if err != nil {
		return Identity{}, err
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

func (s *delivery) handleError(ctx context.Context, target *target, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if target.fallbackHTTP(err) {
		return nil
	}
	if isConnRefused(err) {
		s.connRefusedCount++
		if !s.connRefusedSuppressed && s.connRefusedCount >= 3 {
			log.Printf("push target %d error: 对端端口未打开", target.id)
			s.connRefusedSuppressed = true
			return nil
		}
		if !s.connRefusedSuppressed {
			s.errLimiter.logf("push target %d error: %v", target.id, err)
		}
		return nil
	}
	s.errLimiter.logf("push target %d error: %v", target.id, err)
	return nil
}

func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

type metricsResponse struct {
	OK     bool                 `json:"ok"`
	Update *selfupdate.Manifest `json:"update"`
}

func (s *delivery) handleResponse(resp *http.Response, target *target, debug bool) (bool, bool, *selfupdate.Manifest) {
	defer drainBody(resp)

	if resp.StatusCode == http.StatusUnprocessableEntity {
		s.errLimiter.logf("push target %d non-200 status: %s", target.id, resp.Status)
		return false, false, nil
	}

	if resp.StatusCode != http.StatusOK {
		s.errLimiter.logf("push target %d non-200 status: %s", target.id, resp.Status)
		return false, false, nil
	}

	manifest := decodeMetricsResponse(resp)
	recoverStatic := s.connRefusedSuppressed
	if s.connRefusedSuppressed {
		log.Printf("push target %d recovered: 对端已恢复", target.id)
	}
	s.connRefusedSuppressed = false
	s.connRefusedCount = 0
	if debug {
		log.Printf("push target %d ok: %s", target.id, resp.Status)
	}
	return true, recoverStatic, manifest
}

func decodeMetricsResponse(resp *http.Response) *selfupdate.Manifest {
	if resp == nil || resp.Body == nil {
		return nil
	}
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

func StartWithCache(ctx context.Context, targets []reportcfg.Target, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool, cache *Cache) error {
	return start(ctx, targets, interval, s, debug, requireHTTPS, cache)
}

func start(ctx context.Context, specs []reportcfg.Target, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool, cache *Cache) error {
	if interval <= 0 {
		return fmt.Errorf("push interval must be positive")
	}
	agent, err := newAgent(specs, interval, s, debug, requireHTTPS, cache)
	if err != nil {
		return err
	}
	defer agent.waitStatic()

	if d := s.PushDelay(); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	agent.startStatic(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := agent.sendRound(ctx); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type targetResult struct {
	ok       bool
	manifest *selfupdate.Manifest
	err      error
}

func (a *agent) sendRound(ctx context.Context) error {
	if len(a.targets) == 0 {
		return nil
	}

	report, body, err := a.buildReport()
	if err != nil {
		return err
	}
	if report == nil {
		return nil
	}

	results := make(chan targetResult, len(a.targets))
	var wg sync.WaitGroup
	for _, target := range a.targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, manifest, err := a.sendTarget(ctx, target, body)
			results <- targetResult{ok: ok, manifest: manifest, err: err}
		}()
	}
	wg.Wait()
	close(results)

	success := false
	var manifest *selfupdate.Manifest
	conflict := false
	for result := range results {
		if result.err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return result.err
		}
		success = success || result.ok
		if result.manifest == nil {
			continue
		}
		if manifest == nil {
			manifest = result.manifest
			continue
		}
		if *manifest != *result.manifest {
			conflict = true
		}
	}
	if conflict {
		a.roundErrLimiter.logf("push update skipped: conflicting manifests in one round")
	} else if manifest != nil {
		if err := selfupdate.Apply(ctx, *manifest); err != nil {
			if errors.Is(err, selfupdate.ErrRestart) {
				log.Printf("node update staged: version=%s", manifest.Version)
				return selfupdate.ErrRestart
			}
			a.roundErrLimiter.logf("push update failed: %v", err)
		}
	}
	if success {
		if a.cache != nil {
			a.cache.Set(report)
		}
		return nil
	}

	a.roundErrLimiter.logf("push round failed: all %d targets failed", len(a.targets))
	return nil
}

func (a *agent) sendTarget(ctx context.Context, target *target, body []byte) (bool, *selfupdate.Manifest, error) {
	resp, err := sendReport(ctx, target, body)
	if err != nil {
		if err := target.delivery.handleError(ctx, target, err); err != nil {
			return false, nil, err
		}
		return false, nil, nil
	}
	ok, recoverStatic, manifest := target.delivery.handleResponse(resp, target, a.debug)
	if recoverStatic {
		target.wakeStatic("recovery")
	}
	return ok, manifest, nil
}

func (a *agent) startStatic(ctx context.Context) {
	for _, target := range a.targets {
		if target.static.source == nil {
			continue
		}
		target := target
		a.staticWG.Add(1)
		go func() {
			defer a.staticWG.Done()
			target.static.run(ctx, target, a.debug)
		}()
	}
}

func (a *agent) waitStatic() {
	a.staticWG.Wait()
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

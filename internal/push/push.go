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
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodeiface"
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
	source   nodeiface.PushSource
	hostName string
	debug    bool
	cache    *Cache

	target   *target
	static   *staticSync
	delivery *delivery
}

type target struct {
	client       *http.Client
	host         string
	port         string
	hostIsIP     bool
	secret       string
	requireHTTPS bool
	scheme       string
}

func newTarget(host, port, secret string, requireHTTPS bool) *target {
	return &target{
		client:       &http.Client{Timeout: 10 * time.Second},
		host:         host,
		port:         port,
		hostIsIP:     net.ParseIP(host) != nil,
		secret:       secret,
		requireHTTPS: requireHTTPS,
		scheme:       "https",
	}
}

func (t *target) hostType() string {
	if t.hostIsIP {
		return "ip"
	}
	return "domain"
}

func (t *target) metricsURL() string {
	return fmt.Sprintf("%s://%s:%s/api/node/metrics", t.scheme, t.host, t.port)
}

func (t *target) staticURL() string {
	return fmt.Sprintf("%s://%s:%s/api/node/static", t.scheme, t.host, t.port)
}

func (t *target) httpFallback(err error) fallbackDecision {
	if t.scheme != "https" || t.requireHTTPS {
		return fallbackDecision{}
	}
	if isPlainHTTP(err) {
		return fallbackDecision{useHTTP: true}
	}
	if t.hostIsIP && isCertError(err) {
		return fallbackDecision{useHTTP: true}
	}
	if !t.hostIsIP && isExpiredCert(err) {
		if allowExpiredCertFallback(t.host, t.port) {
			return fallbackDecision{useHTTP: true}
		}
		return fallbackDecision{expiredRefused: true}
	}
	return fallbackDecision{}
}

func (t *target) fallbackHTTP(err error) bool {
	decision := t.httpFallback(err)
	if decision.expiredRefused {
		log.Printf("ERROR: push https refused fallback, cert expired but chain invalid for %s:%s", t.host, t.port)
	}
	if !decision.useHTTP {
		return false
	}
	t.scheme = "http"
	log.Printf("WARN: push https failed, fallback to http for %s:%s", t.host, t.port)
	return true
}

type staticSync struct {
	source     nodeiface.StaticSource
	errLimiter logLimiter
	retryTimer *time.Timer
	retryCh    <-chan time.Time
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

func (s *staticSync) retryChan() <-chan time.Time {
	return s.retryCh
}

func (s *staticSync) scheduleRetry() {
	if s.retryTimer == nil {
		s.retryTimer = time.NewTimer(staticRetryDelay)
	} else {
		stopTimer(s.retryTimer)
		s.retryTimer.Reset(staticRetryDelay)
	}
	s.retryCh = s.retryTimer.C
}

func (s *staticSync) clearRetry() {
	if s.retryTimer != nil {
		stopTimer(s.retryTimer)
	}
	s.retryCh = nil
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

func (s *staticSync) send(ctx context.Context, target *target, debug bool, reason string, snap *metrics.Static, state staticState, missing []string) error {
	if err := sendStatic(ctx, target.client, target.staticURL(), target.secret, snap); err != nil {
		return err
	}
	if debug {
		if state == staticComplete {
			log.Printf("push static ok (%s)", reason)
		} else {
			log.Printf("push static partial ok (%s), missing=%s", reason, strings.Join(missing, ", "))
		}
	}
	return nil
}

func (s *staticSync) sync(ctx context.Context, target *target, debug bool, reason string) {
	if s.source == nil {
		return
	}

	snap, state, missing, err := s.prepare()
	if err != nil {
		s.errLimiter.logf("push static error (%s): %v", reason, err)
		s.scheduleRetry()
		return
	}

	err = s.send(ctx, target, debug, reason, snap, state, missing)
	if err != nil {
		s.errLimiter.logf("push static error (%s): %v", reason, err)
		if target.fallbackHTTP(err) {
			err = s.send(ctx, target, debug, reason, snap, state, missing)
			if err != nil {
				s.errLimiter.logf("push static error (%s): %v", reason, err)
			}
		}
	}

	if err == nil && state == staticComplete {
		s.clearRetry()
	} else {
		s.scheduleRetry()
	}
}

type delivery struct {
	errLimiter logLimiter

	connRefusedCount      int
	connRefusedSuppressed bool
	consecutive422        int
}

func newDelivery() *delivery {
	return &delivery{
		errLimiter: logLimiter{cooldown: time.Minute},
	}
}

func newAgent(dashHost, dashPort, secret string, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool, cache *Cache) *agent {
	hostName := s.Hostname()
	if hostName == "" {
		hostName = "unknown"
	}

	staticSource, _ := s.(nodeiface.StaticSource)
	target := newTarget(dashHost, dashPort, secret, requireHTTPS)
	a := &agent{
		source:   s,
		hostName: hostName,
		debug:    debug,
		cache:    cache,
		target:   target,
		static:   newStaticSync(staticSource),
		delivery: newDelivery(),
	}

	log.Printf("Ithiltir-node (push mode) to %s, interval=%s, node_id=%s, host_type=%s, require_https=%t", a.target.metricsURL(), interval, a.hostName, a.target.hostType(), requireHTTPS)

	return a
}

func (a *agent) sendReport(ctx context.Context) (*metrics.NodeReport, *http.Response, error) {
	m := a.source.Snapshot()
	if m == nil {
		return nil, nil, nil
	}
	report := metrics.NewNodeReport(a.source.Version(), a.hostName, a.source.Time(), m)
	body, err := json.Marshal(report)
	if err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.target.metricsURL(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Node-Secret", a.target.secret)

	resp, err := a.target.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	return &report, resp, nil
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
			log.Printf("push error: 对端端口未打开")
			s.connRefusedSuppressed = true
			return nil
		}
		if !s.connRefusedSuppressed {
			s.errLimiter.logf("push error: %v", err)
		}
		return nil
	}
	s.errLimiter.logf("push error: %v", err)
	return nil
}

func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func (s *delivery) handleResponse(ctx context.Context, resp *http.Response, report *metrics.NodeReport, cache *Cache, static *staticSync, target *target, debug bool) error {
	defer drainBody(resp)

	if resp.StatusCode == http.StatusUnprocessableEntity {
		s.consecutive422++
		if s.consecutive422 >= 5 {
			log.Printf("ERROR: push stopped after 5 consecutive 422 responses")
			return fmt.Errorf("push stopped after consecutive 422 responses")
		}
		s.errLimiter.logf("push non-200 status: %s", resp.Status)
		return nil
	}

	s.consecutive422 = 0

	if resp.StatusCode != http.StatusOK {
		s.errLimiter.logf("push non-200 status: %s", resp.Status)
		return nil
	}

	if cache != nil && report != nil {
		cache.Set(report)
	}
	if s.connRefusedSuppressed {
		log.Printf("push recovered: 对端已恢复")
		static.sync(ctx, target, debug, "recovery")
	}
	s.connRefusedSuppressed = false
	s.connRefusedCount = 0
	if debug {
		log.Printf("push ok: %s", resp.Status)
	}
	return nil
}

func Start(ctx context.Context, dashHost, dashPort, secret string, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool) error {
	return start(ctx, dashHost, dashPort, secret, interval, s, debug, requireHTTPS, nil)
}

func StartWithCache(ctx context.Context, dashHost, dashPort, secret string, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool, cache *Cache) error {
	return start(ctx, dashHost, dashPort, secret, interval, s, debug, requireHTTPS, cache)
}

func start(ctx context.Context, dashHost, dashPort, secret string, interval time.Duration, s nodeiface.PushSource, debug bool, requireHTTPS bool, cache *Cache) error {
	if interval <= 0 {
		return fmt.Errorf("push interval must be positive")
	}
	agent := newAgent(dashHost, dashPort, secret, interval, s, debug, requireHTTPS, cache)
	defer agent.static.clearRetry()

	if d := s.PushDelay(); d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	agent.static.sync(ctx, agent.target, agent.debug, "startup")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-agent.static.retryChan():
			agent.static.sync(ctx, agent.target, agent.debug, "fallback")

		case <-ticker.C:
			report, resp, err := agent.sendReport(ctx)
			if report == nil && resp == nil && err == nil {
				continue
			}
			if err != nil {
				if err := agent.delivery.handleError(ctx, agent.target, err); err != nil {
					return err
				}
				continue
			}
			if err := agent.delivery.handleResponse(ctx, resp, report, agent.cache, agent.static, agent.target, agent.debug); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
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

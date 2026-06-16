package push

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/reportcfg"
	"Ithiltir-node/internal/selfupdate"
)

type fakeSource struct {
	snapshot     *metrics.Snapshot
	snapshotTime time.Time
	pushDelay    time.Duration
	version      string
	hostname     string
}

func (f *fakeSource) Snapshot() *metrics.Snapshot { return f.snapshot }
func (f *fakeSource) Time() time.Time             { return f.snapshotTime }
func (f *fakeSource) PushDelay() time.Duration    { return f.pushDelay }
func (f *fakeSource) Version() string             { return f.version }
func (f *fakeSource) Hostname() string            { return f.hostname }

func TestUpdatePlanIgnoresCurrentVersion(t *testing.T) {
	current := &selfupdate.Manifest{
		Version: "1.2.3",
		URL:     "https://example.test/current",
		SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		Size:    1,
	}
	next := &selfupdate.Manifest{
		Version: "1.2.4",
		URL:     "https://example.test/next",
		SHA256:  "1111111111111111111111111111111111111111111111111111111111111111",
		Size:    1,
	}

	var plan updatePlan
	plan.add("1.2.3", current, "current-secret")
	plan.add("1.2.3", next, "next-secret")

	if plan.conflict {
		t.Fatal("update plan conflicts when current-version manifest should be ignored")
	}
	if plan.manifest == nil || !sameUpdateManifest(plan.manifest, next) {
		t.Fatalf("update plan manifest = %+v, want %+v", plan.manifest, next)
	}
	if plan.manifest.Secret != "next-secret" {
		t.Fatalf("update plan secret = %q, want next-secret", plan.manifest.Secret)
	}
}

func TestUpdatePlanConflictsOnDifferentNewVersions(t *testing.T) {
	first := &selfupdate.Manifest{
		Version: "1.2.4",
		URL:     "https://example.test/first",
		SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		Size:    1,
	}
	second := &selfupdate.Manifest{
		Version: "1.2.5",
		URL:     "https://example.test/second",
		SHA256:  "1111111111111111111111111111111111111111111111111111111111111111",
		Size:    1,
	}

	var plan updatePlan
	plan.add("1.2.3", first, "first-secret")
	plan.add("1.2.3", second, "second-secret")

	if !plan.conflict {
		t.Fatal("update plan conflict = false, want true")
	}
}

func TestUpdatePlanIgnoresSecretForConflict(t *testing.T) {
	first := &selfupdate.Manifest{
		Version: "1.2.4",
		URL:     "https://example.test/node",
		SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
		Size:    1,
	}
	second := *first

	var plan updatePlan
	plan.add("1.2.3", first, "first-secret")
	plan.add("1.2.3", &second, "second-secret")

	if plan.conflict {
		t.Fatal("update plan conflict = true, want false")
	}
	if plan.manifest == nil || !sameUpdateManifest(plan.manifest, first) {
		t.Fatalf("update plan manifest = %+v, want %+v", plan.manifest, first)
	}
	if plan.manifest.Secret != "first-secret" {
		t.Fatalf("update plan secret = %q, want first-secret", plan.manifest.Secret)
	}
}

type fakeStaticSource struct {
	*fakeSource
	staticFn func() *metrics.Static
}

func (f *fakeStaticSource) Static() *metrics.Static {
	return f.staticFn()
}

func validStatic() *metrics.Static {
	return &metrics.Static{
		Version:               "1.0.0",
		Timestamp:             time.Now().UTC(),
		ReportIntervalSeconds: 3,
		CPU: metrics.StaticCPU{
			Info: metrics.StaticCPUInfo{
				Sockets:       1,
				CoresPhysical: 4,
				CoresLogical:  8,
			},
		},
		Memory: metrics.StaticMemory{
			Total: 1024,
		},
		System: metrics.StaticSystem{
			Hostname:        "node-1",
			OS:              "linux",
			Platform:        "debian",
			PlatformVersion: "12",
			KernelVersion:   "6.8.12",
			Arch:            "amd64",
		},
	}
}

func TestStaticFingerprintIgnoresTimestamp(t *testing.T) {
	first := validStatic()
	second := validStatic()
	second.Timestamp = first.Timestamp.Add(time.Hour)

	firstFP, ok, err := staticFingerprint(first)
	if err != nil {
		t.Fatalf("staticFingerprint(first) error = %v", err)
	}
	if !ok {
		t.Fatal("staticFingerprint(first) ok = false, want true")
	}
	secondFP, ok, err := staticFingerprint(second)
	if err != nil {
		t.Fatalf("staticFingerprint(second) error = %v", err)
	}
	if !ok {
		t.Fatal("staticFingerprint(second) ok = false, want true")
	}
	if firstFP != secondFP {
		t.Fatal("static fingerprint changed when only timestamp changed")
	}

	second.Disk.Physical = []metrics.StaticDiskPhysical{{Name: "sdb"}}
	changedFP, ok, err := staticFingerprint(second)
	if err != nil {
		t.Fatalf("staticFingerprint(changed) error = %v", err)
	}
	if !ok {
		t.Fatal("staticFingerprint(changed) ok = false, want true")
	}
	if firstFP == changedFP {
		t.Fatal("static fingerprint did not change after disk topology changed")
	}
}

func newIPv4Server(t *testing.T, handler http.Handler) (*httptest.Server, string, string) {
	t.Helper()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp4: %v", err)
	}

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	return srv, host, port
}

func testTargets(host, port, secret string) []reportcfg.Target {
	return []reportcfg.Target{{
		ID:  1,
		URL: "https://" + net.JoinHostPort(host, port) + "/api/node/metrics",
		Key: secret,
	}}
}

func TestStartPushAgentResendsStaticWhenSnapshotChanges(t *testing.T) {
	oldMinInterval := staticChangeCheckMinInterval
	oldMaxInterval := staticChangeCheckMaxInterval
	defer func() {
		staticChangeCheckMinInterval = oldMinInterval
		staticChangeCheckMaxInterval = oldMaxInterval
	}()
	staticChangeCheckMinInterval = 20 * time.Millisecond
	staticChangeCheckMaxInterval = 20 * time.Millisecond

	var expanded atomic.Bool
	var staticPosts atomic.Int32
	done := make(chan struct{}, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/static":
			staticPosts.Add(1)
			var snap metrics.Static
			if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
				t.Errorf("decode static request: %v", err)
				break
			}
			if len(snap.Disk.Physical) == 1 {
				expanded.Store(true)
			}
			if len(snap.Disk.Physical) == 2 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		case "/api/node/metrics":
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	_, host, port := newIPv4Server(t, handler)

	snapshotter := &fakeStaticSource{
		fakeSource: &fakeSource{
			snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
			snapshotTime: time.Now().UTC(),
			version:      "1.0.0",
			hostname:     "node-1",
		},
		staticFn: func() *metrics.Static {
			snap := validStatic()
			snap.Disk.Physical = []metrics.StaticDiskPhysical{{Name: "sda"}}
			if expanded.Load() {
				snap.Disk.Physical = append(snap.Disk.Physical, metrics.StaticDiskPhysical{Name: "sdb"})
			}
			return snap
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- start(ctx, testTargets(host, port, "secret"), 10*time.Millisecond, snapshotter, false, false, nil)
	}()

	select {
	case <-done:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("static snapshot change did not trigger another static post")
	}

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("start() error = %v, want context.Canceled", err)
	}
	if got := staticPosts.Load(); got < 2 {
		t.Fatalf("static posts = %d, want at least 2", got)
	}
}

func TestStartPushAgentRetriesStaticWhileMetricsContinue(t *testing.T) {
	oldRetryDelay := staticRetryDelay
	defer func() { staticRetryDelay = oldRetryDelay }()
	staticRetryDelay = 20 * time.Millisecond

	var staticCalls atomic.Int32
	var staticPosts atomic.Int32
	var metricsPosts atomic.Int32
	var mu sync.Mutex
	staticPhysicalCores := make([]int, 0, 2)
	done := make(chan struct{}, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/static":
			staticPosts.Add(1)
			var snap metrics.Static
			if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
				t.Errorf("decode static request: %v", err)
			} else {
				mu.Lock()
				staticPhysicalCores = append(staticPhysicalCores, snap.CPU.Info.CoresPhysical)
				mu.Unlock()
			}
		case "/api/node/metrics":
			metricsPosts.Add(1)
		default:
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		if staticPosts.Load() >= 2 && metricsPosts.Load() >= 1 {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})

	_, host, port := newIPv4Server(t, handler)

	snapshotter := &fakeStaticSource{
		fakeSource: &fakeSource{
			snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
			snapshotTime: time.Now().UTC(),
			version:      "1.0.0",
			hostname:     "node-1",
		},
		staticFn: func() *metrics.Static {
			if staticCalls.Add(1) == 1 {
				snap := validStatic()
				snap.CPU.Info.CoresPhysical = 0
				return snap
			}
			return validStatic()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- start(ctx, testTargets(host, port, "secret"), 10*time.Millisecond, snapshotter, false, false, nil)
	}()

	select {
	case <-done:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("static retry and metric push did not complete")
	}

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("start() error = %v, want context.Canceled", err)
	}
	if got := staticCalls.Load(); got != 2 {
		t.Fatalf("Static() called %d times, want exactly 2", got)
	}
	if got := staticPosts.Load(); got < 2 {
		t.Fatalf("static posts = %d, want at least 2", got)
	}
	if got := metricsPosts.Load(); got < 1 {
		t.Fatalf("metrics posts = %d, want at least 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(staticPhysicalCores) < 2 {
		t.Fatalf("static physical core reports = %v, want at least 2 posts", staticPhysicalCores)
	}
	if staticPhysicalCores[0] != 0 {
		t.Fatalf("first static post cores_physical = %d, want partial value 0", staticPhysicalCores[0])
	}
	foundComplete := false
	for _, cores := range staticPhysicalCores[1:] {
		if cores == 4 {
			foundComplete = true
			break
		}
	}
	if !foundComplete {
		t.Fatalf("static physical core reports = %v, want a later complete report", staticPhysicalCores)
	}
}

func TestStartPushAgentReturnsRestartWithStaticSync(t *testing.T) {
	oldApply := applyUpdate
	defer func() { applyUpdate = oldApply }()

	var applyCalled atomic.Bool
	var gotSecret atomic.Value
	applyUpdate = func(_ context.Context, m selfupdate.Manifest) error {
		applyCalled.Store(true)
		gotSecret.Store(m.Secret)
		return selfupdate.ErrRestart
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/node/static":
			w.WriteHeader(http.StatusOK)
		case "/api/node/metrics":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(metricsResponse{
				OK: true,
				Update: &selfupdate.Manifest{
					Version: "1.0.1",
					URL:     "https://example.test/node",
					SHA256:  "0000000000000000000000000000000000000000000000000000000000000000",
					Size:    1,
				},
			}); err != nil {
				t.Errorf("encode metrics response: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	})

	_, host, port := newIPv4Server(t, handler)

	snapshotter := &fakeStaticSource{
		fakeSource: &fakeSource{
			snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
			snapshotTime: time.Now().UTC(),
			version:      "1.0.0",
			hostname:     "node-1",
		},
		staticFn: validStatic,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- start(context.Background(), testTargets(host, port, "secret"), 10*time.Millisecond, snapshotter, false, false, nil)
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, selfupdate.ErrRestart) {
			t.Fatalf("start() error = %v, want ErrRestart", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("start() did not return after update restart")
	}
	if !applyCalled.Load() {
		t.Fatal("applyUpdate was not called")
	}
	if got, _ := gotSecret.Load().(string); got != "secret" {
		t.Fatalf("update secret = %q, want secret", got)
	}
}

func TestStartPushAgentKeepsRunningAfterTarget422(t *testing.T) {
	var metricsPosts atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node/metrics" {
			http.NotFound(w, r)
			return
		}
		metricsPosts.Add(1)
		http.Error(w, "invalid", http.StatusUnprocessableEntity)
	})

	_, host, port := newIPv4Server(t, handler)

	snapshotter := &fakeSource{
		snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
		snapshotTime: time.Now().UTC(),
		version:      "1.0.0",
		hostname:     "node-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := start(ctx, testTargets(host, port, "secret"), 10*time.Millisecond, snapshotter, false, false, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("start() error = %v, want context.DeadlineExceeded", err)
	}
	if got := metricsPosts.Load(); got < 5 {
		t.Fatalf("metrics posts = %d, want at least 5", got)
	}
}

func TestStartPushAgentFallsBackToHTTPForPlaintextServer(t *testing.T) {
	var metricsPosts atomic.Int32
	done := make(chan struct{}, 1)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node/metrics" {
			http.NotFound(w, r)
			return
		}
		metricsPosts.Add(1)
		w.WriteHeader(http.StatusOK)
		select {
		case done <- struct{}{}:
		default:
		}
	})

	_, host, port := newIPv4Server(t, handler)

	snapshotter := &fakeSource{
		snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
		snapshotTime: time.Now().UTC(),
		version:      "1.0.0",
		hostname:     "node-1",
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- start(ctx, testTargets(host, port, "secret"), 10*time.Millisecond, snapshotter, false, false, nil)
	}()

	select {
	case <-done:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("plaintext server did not receive metrics after fallback")
	}

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("start() error = %v, want context.Canceled", err)
	}
	if got := metricsPosts.Load(); got < 1 {
		t.Fatalf("metrics posts = %d, want at least 1", got)
	}
}

func TestStartPushAgentRequireHTTPSDoesNotFallback(t *testing.T) {
	var metricsPosts atomic.Int32

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/node/metrics" {
			http.NotFound(w, r)
			return
		}
		metricsPosts.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	_, host, port := newIPv4Server(t, handler)

	snapshotter := &fakeSource{
		snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
		snapshotTime: time.Now().UTC(),
		version:      "1.0.0",
		hostname:     "node-1",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	err := start(ctx, testTargets(host, port, "secret"), 10*time.Millisecond, snapshotter, false, true, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("start() error = %v, want context.DeadlineExceeded", err)
	}
	if got := metricsPosts.Load(); got != 0 {
		t.Fatalf("metrics posts = %d, want 0 when requireHTTPS blocks fallback", got)
	}
}

func TestStartPushAgentSendsRoundToAllTargets(t *testing.T) {
	var firstPosts atomic.Int32
	var secondPosts atomic.Int32
	done := make(chan struct{}, 1)

	okHandler := func(counter *atomic.Int32, wantKey string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/node/metrics" {
				http.NotFound(w, r)
				return
			}
			if got := r.Header.Get("X-Node-Secret"); got != wantKey {
				t.Errorf("X-Node-Secret = %q, want %q", got, wantKey)
			}
			counter.Add(1)
			w.WriteHeader(http.StatusOK)
			if firstPosts.Load() >= 1 && secondPosts.Load() >= 1 {
				select {
				case done <- struct{}{}:
				default:
				}
			}
		}
	}

	_, firstHost, firstPort := newIPv4Server(t, okHandler(&firstPosts, "first-secret"))
	_, secondHost, secondPort := newIPv4Server(t, okHandler(&secondPosts, "second-secret"))

	snapshotter := &fakeSource{
		snapshot:     &metrics.Snapshot{System: metrics.System{Alive: true}},
		snapshotTime: time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC),
		version:      "1.0.0",
		hostname:     "node-1",
	}
	targets := []reportcfg.Target{
		{ID: 1, URL: "https://" + net.JoinHostPort(firstHost, firstPort) + "/api/node/metrics", Key: "first-secret"},
		{ID: 2, URL: "https://" + net.JoinHostPort(secondHost, secondPort) + "/api/node/metrics", Key: "second-secret"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- start(ctx, targets, 10*time.Millisecond, snapshotter, false, false, nil)
	}()

	select {
	case <-done:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("both targets did not receive metrics")
	}

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("start() error = %v, want context.Canceled", err)
	}
}

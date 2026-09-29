package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/reportcfg"
)

type testRPC struct {
	nodewire.UnimplementedNodeServer
	metrics, static, virt   atomic.Int32
	identifies              atomic.Int32
	identifyErr, metricsErr error
	connect                 func(grpc.BidiStreamingServer[nodewire.NodeMessage, nodewire.DashMessage]) error
}

func (s *testRPC) Connect(stream grpc.BidiStreamingServer[nodewire.NodeMessage, nodewire.DashMessage]) error {
	if s.connect == nil {
		return status.Error(codes.Unimplemented, "unsupported")
	}
	return s.connect(stream)
}

func (s *testRPC) Identify(ctx context.Context, _ *nodewire.Empty) (*nodewire.Identity, error) {
	s.identifies.Add(1)
	if s.identifyErr != nil {
		return nil, s.identifyErr
	}
	if md, _ := metadata.FromIncomingContext(ctx); len(md.Get("x-node-secret")) != 1 || md.Get("x-node-secret")[0] != "secret" {
		return nil, status.Error(codes.Unauthenticated, "unauthorized")
	}
	return &nodewire.Identity{InstallId: "install", ProtocolVersion: 1}, nil
}
func (s *testRPC) Metrics(ctx context.Context, in *nodewire.Report) (*nodewire.Reply, error) {
	s.metrics.Add(1)
	if s.metricsErr != nil {
		return nil, s.metricsErr
	}
	var report map[string]any
	if err := json.Unmarshal(in.Json, &report); err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid")
	}
	return &nodewire.Reply{Json: []byte(`{"ok":true,"update":{"id":"2.0.0","version":"2.0.0","url":"https://example.com/node","sha256":"hash","size":100}}`)}, nil
}
func (s *testRPC) Static(context.Context, *nodewire.Report) (*nodewire.Empty, error) {
	s.static.Add(1)
	return &nodewire.Empty{}, nil
}
func (s *testRPC) Virt(context.Context, *nodewire.Report) (*nodewire.Empty, error) {
	s.virt.Add(1)
	return &nodewire.Empty{}, nil
}

func rpcHTTPServer(t *testing.T, service *testRPC, posts *atomic.Int32) *httptest.Server {
	t.Helper()
	rpc := grpc.NewServer()
	nodewire.RegisterNodeServer(rpc, service)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			rpc.ServeHTTP(w, r)
			return
		}
		posts.Add(1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetHTTP1(true)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(func() { rpc.Stop(); server.Close() })
	return server
}

func TestRPCReportsShareConnectionAndPreserveUpdate(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "auto")
	service := new(testRPC)
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	spec := reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}
	destination, err := newTarget(spec, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	client, err := destination.rpcClient(t.Context())
	if err != nil || client == nil {
		t.Fatalf("negotiate: %v", err)
	}
	conn := destination.rpc.conn
	a := agent{}
	ok, manifest, err := a.sendTarget(t.Context(), destination, []byte("{}"))
	if err != nil || !ok || manifest == nil || manifest.Version != "2.0.0" {
		t.Fatalf("report result: %v %v %v", ok, manifest, err)
	}
	if err := destination.rpcStatic(t.Context(), client, &metrics.Static{}); err != nil {
		t.Fatal(err)
	}
	if err := sendVirt(t.Context(), destination, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if destination.rpc.conn != conn || service.metrics.Load() != 1 || service.static.Load() != 1 || service.virt.Load() != 1 || posts.Load() != 0 {
		t.Fatal("report transport was duplicated")
	}
	identity, err := FetchIdentity(t.Context(), spec, false)
	if err != nil || identity.InstallID != "install" {
		t.Fatalf("identity: %+v %v", identity, err)
	}
}

func TestRPCDoesNotReplayUncertainReportOverHTTP(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "auto")
	service := &testRPC{metricsErr: status.Error(codes.Unavailable, "ack lost")}
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	a := agent{}
	ok, _, err := a.sendTarget(t.Context(), destination, []byte("{}"))
	if err != nil || ok {
		t.Fatalf("unexpected report success: %v %v", ok, err)
	}
	if posts.Load() != 0 || service.metrics.Load() != 1 {
		t.Fatal("uncertain report was replayed")
	}
}

func TestRPCAutoDoesNotBypassAuthentication(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "auto")
	service := &testRPC{identifyErr: status.Error(codes.Unauthenticated, "unauthorized")}
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	_, err := FetchIdentity(t.Context(), reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, false)
	if status.Code(err) != codes.Unauthenticated || posts.Load() != 0 {
		t.Fatalf("auth fallback: %v posts=%d", err, posts.Load())
	}
}

func TestRPCAutoStopsAfterCooldownAuthenticationFailure(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "auto")
	for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			service := &testRPC{identifyErr: status.Error(code, "rejected")}
			var posts atomic.Int32
			server := rpcHTTPServer(t, service, &posts)
			destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			defer destination.closeRPC()
			// Resume the state left by a rate limit whose cooldown has elapsed.
			destination.rpc.probeErr = status.Error(codes.ResourceExhausted, "rate limited")
			destination.rpc.retryAt = time.Now().Add(-time.Second)
			for range 3 {
				if _, err := destination.rpcClient(t.Context()); status.Code(err) != code {
					t.Fatalf("authentication result: %v", err)
				}
			}
			if service.identifies.Load() != 1 || posts.Load() != 0 {
				t.Fatalf("rejected credentials retried: identifies=%d HTTP=%d", service.identifies.Load(), posts.Load())
			}
		})
	}
}

func TestRPCAutoSelectsHTTPBeforeReportingToLegacyDash(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "auto")
	service := &testRPC{identifyErr: status.Error(codes.Unimplemented, "legacy")}
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	client, err := destination.rpcClient(t.Context())
	if err != nil || client != nil {
		t.Fatalf("legacy selection: client=%v err=%v", client, err)
	}
	a := agent{}
	ok, _, err := a.sendTarget(t.Context(), destination, []byte("{}"))
	if err != nil || !ok || posts.Load() != 1 || service.metrics.Load() != 0 {
		t.Fatalf("legacy report: ok=%v err=%v posts=%d", ok, err, posts.Load())
	}
}

func TestRPCAutoTLSFailureCannotDowngradeToPlaintext(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "auto")
	var posts atomic.Int32
	server := rpcHTTPServer(t, new(testRPC), &posts)
	destination, err := newTarget(reportcfg.Target{ID: 1, URL: strings.Replace(server.URL, "http://", "https://", 1) + "/api/node/metrics", Key: "secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	a := agent{}
	ok, _, _ := a.sendTarget(t.Context(), destination, []byte("{}"))
	scheme, _, _, _ := destination.endpointParts()
	if ok || posts.Load() != 0 || scheme != "https" {
		t.Fatalf("TLS failure downgraded: ok=%v posts=%d scheme=%s", ok, posts.Load(), scheme)
	}
}

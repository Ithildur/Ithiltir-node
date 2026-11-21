package push

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/selfupdate"
)

// Selection precedes reports and is fixed for this target's lifetime. A report
// with an uncertain ACK is never replayed over another transport.
type rpcTarget struct {
	mu       sync.Mutex
	mode     string
	useHTTP  bool
	conn     *grpc.ClientConn
	identity *nodewire.Identity
	probeErr error
	retryAt  time.Time
}

func transportMode() (string, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("ITHILTIR_NODE_TRANSPORT")))
	if mode == "" {
		mode = "http"
	}
	switch mode {
	case "auto", "http", "grpc":
		return mode, nil
	}
	return "", fmt.Errorf("ITHILTIR_NODE_TRANSPORT must be auto, http or grpc")
}

func (t *target) rpcClient(ctx context.Context) (nodewire.NodeClient, error) {
	state := &t.rpc
	state.mu.Lock()
	defer state.mu.Unlock()
	enabled, err := t.selectRPC(ctx)
	if err != nil || !enabled {
		return nil, err
	}
	if state.conn == nil {
		state.conn, err = t.newRPCConn()
		if err != nil {
			return nil, err
		}
	}
	return nodewire.NewNodeClient(state.conn), nil
}

// selectRPC fixes the transport before the first report. The caller holds rpc.mu.
func (t *target) selectRPC(ctx context.Context) (bool, error) {
	state := &t.rpc
	if state.useHTTP || state.mode == "http" {
		return false, nil
	}
	if state.mode == "grpc" || state.identity != nil {
		return true, nil
	}
	if state.probeErr != nil {
		if state.retryAt.IsZero() || time.Now().Before(state.retryAt) {
			return false, state.probeErr
		}
		state.probeErr, state.retryAt = nil, time.Time{}
	}
	conn, err := t.newRPCConn()
	if err != nil {
		return false, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	identity, probeErr := nodewire.NewNodeClient(conn).Identify(rpcContext(probeCtx, t.secret), &nodewire.Empty{})
	cancel()
	if probeErr == nil && identity.ProtocolVersion == 1 {
		state.conn, state.identity = conn, identity
		return true, nil
	}
	conn.Close()
	if probeErr != nil && ctx.Err() != nil {
		return false, ctx.Err()
	}
	switch status.Code(probeErr) {
	case codes.Unauthenticated, codes.PermissionDenied:
		state.probeErr = probeErr
		return false, probeErr
	case codes.ResourceExhausted:
		state.probeErr, state.retryAt = probeErr, time.Now().Add(time.Minute)
		return false, probeErr
	}
	// Only the capability probe can fall back, to HTTP on the same URL/scheme.
	state.useHTTP = true
	return false, nil
}

func (t *target) newRPCConn() (*grpc.ClientConn, error) {
	scheme, host, port, _ := t.endpointParts()
	var creds credentials.TransportCredentials = insecure.NewCredentials()
	if scheme == "https" {
		creds = credentials.NewTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	return grpc.NewClient("passthrough:///"+net.JoinHostPort(host, port),
		grpc.WithTransportCredentials(creds),
		grpc.WithDisableRetry(),
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.Config{BaseDelay: time.Second, Multiplier: 1.6, Jitter: 0.2, MaxDelay: time.Minute}, MinConnectTimeout: 5 * time.Second}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize((4<<20)+1024)))
}

func (t *target) closeRPC() {
	t.rpc.mu.Lock()
	defer t.rpc.mu.Unlock()
	if t.rpc.conn != nil {
		t.rpc.conn.Close()
		t.rpc.conn = nil
	}
}
func rpcContext(ctx context.Context, secret string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-node-secret", secret)
}
func (t *target) rpcMetrics(ctx context.Context, client nodewire.NodeClient, body []byte) (*selfupdate.Manifest, error) {
	reply, err := client.Metrics(rpcContext(ctx, t.secret), &nodewire.Report{Json: body})
	if err != nil {
		return nil, err
	}
	var result metricsResponse
	if err := json.Unmarshal(reply.Json, &result); err != nil {
		return nil, fmt.Errorf("decode RPC metrics response: %w", err)
	}
	return result.Update, nil
}
func (t *target) rpcStatic(ctx context.Context, client nodewire.NodeClient, snapshot *metrics.Static) error {
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	_, err = client.Static(rpcContext(ctx, t.secret), &nodewire.Report{Json: body})
	return err
}

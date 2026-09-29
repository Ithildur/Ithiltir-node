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
	client   nodewire.NodeClient
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
	if state.probeErr != nil {
		if state.retryAt.IsZero() || time.Now().Before(state.retryAt) {
			return nil, state.probeErr
		}
		state.probeErr, state.retryAt = nil, time.Time{}
	}
	if state.useHTTP || state.mode == "http" {
		return nil, nil
	}
	if state.client != nil {
		return state.client, nil
	}
	scheme, host, port, _ := t.endpointParts()
	var creds credentials.TransportCredentials = insecure.NewCredentials()
	if scheme == "https" {
		creds = credentials.NewTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	}
	conn, err := grpc.NewClient("passthrough:///"+net.JoinHostPort(host, port),
		grpc.WithTransportCredentials(creds),
		grpc.WithDisableRetry(),
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.Config{BaseDelay: time.Second, Multiplier: 1.6, Jitter: 0.2, MaxDelay: time.Minute}, MinConnectTimeout: 5 * time.Second}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize((4<<20)+1024)))
	if err != nil {
		return nil, err
	}
	client := nodewire.NewNodeClient(conn)
	if state.mode == "auto" {
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		identity, probeErr := client.Identify(rpcContext(probeCtx, t.secret), &nodewire.Empty{})
		cancel()
		if probeErr != nil {
			conn.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			switch status.Code(probeErr) {
			case codes.Unauthenticated, codes.PermissionDenied:
				state.probeErr = probeErr
				return nil, probeErr
			case codes.ResourceExhausted:
				state.probeErr, state.retryAt = probeErr, time.Now().Add(time.Minute)
				return nil, probeErr
			}
			// Only the capability probe can fall back, to HTTP on the same URL/scheme.
			// No metrics have been sent and TLS errors never authorize plaintext.
			state.useHTTP = true
			return nil, nil
		}
		if identity.ProtocolVersion != 1 {
			conn.Close()
			state.useHTTP = true
			return nil, nil
		}
		state.identity = identity
	}
	state.conn, state.client = conn, client
	return client, nil
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
func rpcTimeout(ctx context.Context, secret string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	return rpcContext(ctx, secret), cancel
}
func (t *target) rpcMetrics(ctx context.Context, client nodewire.NodeClient, body []byte) (*selfupdate.Manifest, error) {
	ctx, cancel := rpcTimeout(ctx, t.secret)
	defer cancel()
	reply, err := client.Metrics(ctx, &nodewire.Report{Json: body})
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
	ctx, cancel := rpcTimeout(ctx, t.secret)
	defer cancel()
	_, err = client.Static(ctx, &nodewire.Report{Json: body})
	return err
}

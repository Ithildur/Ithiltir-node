package push

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"Ithiltir-node/internal/metrics"
	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/reportcfg"
)

type recoverySource struct{ fakeSource }

func (s *recoverySource) Time() time.Time { return time.Now().UTC() }

type recoveryRPC struct {
	nodewire.UnimplementedNodeServer
	receive func(context.Context, *metrics.NodeReport) error
}

func (s *recoveryRPC) Metrics(ctx context.Context, in *nodewire.Report) (*nodewire.Reply, error) {
	var report metrics.NodeReport
	if err := json.Unmarshal(in.Json, &report); err != nil {
		return nil, err
	}
	if err := s.receive(ctx, &report); err != nil {
		return nil, err
	}
	return &nodewire.Reply{Json: []byte("{}")}, nil
}

func TestMetricsRecoverWithLatestSnapshot(t *testing.T) {
	for _, tc := range []struct {
		transport string
		timeout   bool
		interval  time.Duration
	}{
		{transport: "http", timeout: true, interval: 8 * time.Second},
		{transport: "grpc", timeout: true, interval: 8 * time.Second},
		{transport: "http", interval: 2 * time.Second},
	} {
		name := tc.transport + "/rejected"
		if tc.timeout {
			name = tc.transport + "/timeout"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("ITHILTIR_NODE_TRANSPORT", tc.transport)
			received := make(chan metrics.NodeReport, 8)
			var requests atomic.Int32
			receive := func(ctx context.Context, report *metrics.NodeReport) error {
				received <- *report
				if requests.Add(1) != 1 {
					return nil
				}
				if tc.timeout {
					<-ctx.Done()
					return ctx.Err()
				}
				return errors.New("rejected")
			}
			var endpoint string
			var targets []reportcfg.Target
			if tc.transport == "grpc" {
				server := rpcHTTPServer(t, &recoveryRPC{receive: receive}, new(atomic.Int32))
				endpoint = server.URL
			} else {
				server, _, _ := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var report metrics.NodeReport
					if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
						t.Errorf("decode report: %v", err)
						return
					}
					if tc.timeout {
						w.Header().Set("Content-Type", "application/json")
						w.(http.Flusher).Flush()
					}
					if err := receive(r.Context(), &report); err != nil {
						if !tc.timeout {
							http.Error(w, "rejected", http.StatusForbidden)
						}
						return
					}
					_, _ = w.Write([]byte("{}"))
				}))
				endpoint = server.URL
				if tc.timeout {
					healthy, _, _ := newIPv4Server(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusOK)
					}))
					targets = append(targets, reportcfg.Target{ID: 2, URL: healthy.URL + "/metrics", Key: "secret"})
				}
			}
			targets = append(targets, reportcfg.Target{ID: 1, URL: endpoint + "/api/node/metrics", Key: "secret"})
			source := &recoverySource{fakeSource{snapshot: &metrics.Snapshot{System: metrics.System{Alive: true}}, version: "1.0.0", hostname: "node-1"}}
			ctx, cancel := context.WithCancel(t.Context())
			cache := new(Cache)
			done := make(chan error, 1)
			go func() {
				done <- StartWithCache(ctx, targets, tc.interval, source, false, false, cache)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("push loop stopped with %v", err)
					}
				case <-time.After(2 * time.Second):
					t.Error("push loop did not stop")
				}
			})
			var first, second metrics.NodeReport
			select {
			case first = <-received:
			case <-time.After(tc.interval + time.Second):
				t.Fatal("initial report was not received")
			}
			wait := tc.interval + time.Second
			minimum := tc.interval - 100*time.Millisecond
			if tc.timeout {
				wait = 7500 * time.Millisecond
				minimum = 6900 * time.Millisecond
			}
			select {
			case second = <-received:
			case <-time.After(wait):
				t.Fatal("metrics did not recover within the retry budget")
			}
			if second.Timestamp.Sub(first.Timestamp) < minimum {
				t.Fatal("report retried before its timeout or reporting interval")
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if report := cache.Get(); report != nil && report.Timestamp.After(first.Timestamp) {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("recovered report was not acknowledged and cached")
		})
	}
}

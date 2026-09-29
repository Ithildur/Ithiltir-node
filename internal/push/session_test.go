//go:build linux

package push

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"Ithiltir-node/internal/nodewire"
	"Ithiltir-node/internal/reportcfg"
	"Ithiltir-node/internal/virt"
)

func TestQuerySessionBoundsAndCancelsQueuedWork(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "grpc")
	socket := filepath.Join(t.TempDir(), "pve.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 32)
	canceled := make(chan struct{}, 32)
	var calls atomic.Int32
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"version":"1.0.0","history":true}`))
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	})}
	go helperServer.Serve(listener)
	defer helperServer.Close()
	verified := make(chan error, 1)
	service := &testRPC{connect: func(stream grpc.BidiStreamingServer[nodewire.NodeMessage, nodewire.DashMessage]) (err error) {
		defer func() { verified <- err }()
		if _, err = stream.Recv(); err != nil {
			return err
		}
		for i := 1; i <= 33; i++ {
			if err = stream.Send(&nodewire.DashMessage{Id: strconv.Itoa(i), Body: &nodewire.DashMessage_History{History: &nodewire.HistoryQuery{VmId: 101, Timeframe: "hour", Consolidation: "AVERAGE", TimeoutMs: 10000}}}); err != nil {
				return err
			}
			if i == 4 {
				for range 4 {
					select {
					case <-entered:
					case <-stream.Context().Done():
						return stream.Context().Err()
					}
				}
			}
		}
		result, err := stream.Recv()
		if err != nil {
			return err
		}
		if result.Id != "33" || result.GetResult() == nil || result.GetResult().Error != "busy" {
			return fmt.Errorf("query admission exceeded capacity: %v", result)
		}
		if err = stream.Send(&nodewire.DashMessage{Id: "32", Body: &nodewire.DashMessage_Cancel{Cancel: true}}); err != nil {
			return err
		}
		result, err = stream.Recv()
		if err != nil {
			return err
		}
		if result.Id != "32" || result.GetResult() == nil || result.GetResult().Error != "source_unavailable" {
			return fmt.Errorf("queued query did not cancel: %v", result)
		}
		if calls.Load() != 4 {
			return fmt.Errorf("history execution exceeded four connections: %d", calls.Load())
		}
		return nil
	}}
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	helper := virt.NewClient(socket)
	defer helper.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	_ = destination.querySession(ctx, helper, "1.0.0")
	select {
	case err := <-verified:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("session did not complete admission checks")
	}
	for range 4 {
		select {
		case <-canceled:
		case <-ctx.Done():
			t.Fatal("disconnected session retained local requests")
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("canceled queue executed after session exit: %d", calls.Load())
	}
}

func TestQuerySessionForwardsAndCancelsLocalRequest(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "grpc")
	socket := filepath.Join(t.TempDir(), "pve.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	canceled := make(chan struct{})
	var localCalls atomic.Int32
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"version":"1.0.0","history":true}`))
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		if localCalls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"source":"pve_rrd"}`))
			return
		}
		close(entered)
		<-r.Context().Done()
		close(canceled)
	})}
	go helperServer.Serve(listener)
	defer helperServer.Close()
	verified := make(chan error, 1)
	service := &testRPC{connect: func(stream grpc.BidiStreamingServer[nodewire.NodeMessage, nodewire.DashMessage]) error {
		first, err := stream.Recv()
		if err != nil {
			verified <- err
			return err
		}
		if first.GetCapabilities() == nil || !first.GetCapabilities().PveHistory {
			t.Error("PVE capability missing")
		}
		query := func(id string) error {
			return stream.Send(&nodewire.DashMessage{Id: id, Body: &nodewire.DashMessage_History{History: &nodewire.HistoryQuery{VmId: 101, Timeframe: "hour", Consolidation: "AVERAGE", TimeoutMs: 5000}}})
		}
		if err := query("first"); err != nil {
			verified <- err
			return err
		}
		response, err := stream.Recv()
		if err != nil {
			verified <- err
			return err
		}
		if response.Id != "first" || response.GetResult() == nil || response.GetResult().Error != "" || string(response.GetResult().Json) != `{"source":"pve_rrd"}` {
			t.Error("query response changed")
		}
		if err := query("second"); err != nil {
			verified <- err
			return err
		}
		select {
		case <-entered:
		case <-stream.Context().Done():
			verified <- stream.Context().Err()
			return stream.Context().Err()
		}
		if err := stream.Send(&nodewire.DashMessage{Id: "second", Body: &nodewire.DashMessage_Cancel{Cancel: true}}); err != nil {
			verified <- err
			return err
		}
		select {
		case <-canceled:
			verified <- nil
		case <-stream.Context().Done():
			verified <- stream.Context().Err()
		}
		return nil
	}}
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	helper := virt.NewClient(socket)
	defer helper.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	_ = destination.querySession(ctx, helper, "1.0.0")
	select {
	case err := <-verified:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("reverse query was not verified")
	}
	if localCalls.Load() != 2 || posts.Load() != 0 {
		t.Fatal("unexpected query traffic")
	}
}

func TestQuerySessionSurvivesFailedCapabilityProbe(t *testing.T) {
	t.Setenv("ITHILTIR_NODE_TRANSPORT", "grpc")
	socket := filepath.Join(t.TempDir(), "pve.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	failedProbe := make(chan struct{})
	var probes atomic.Int32
	helperServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			if probes.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"version":"1.0.0","history":true}`))
			} else {
				http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
				if probes.Load() == 2 {
					close(failedProbe)
				}
			}
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"source":"pve_rrd"}`))
	})}
	go helperServer.Serve(listener)
	defer helperServer.Close()
	verified := make(chan struct{})
	service := &testRPC{connect: func(stream grpc.BidiStreamingServer[nodewire.NodeMessage, nodewire.DashMessage]) error {
		if _, err := stream.Recv(); err != nil {
			return err
		}
		select {
		case <-failedProbe:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
		if err := stream.Send(&nodewire.DashMessage{Id: "after-probe", Body: &nodewire.DashMessage_History{History: &nodewire.HistoryQuery{VmId: 101, Timeframe: "hour", Consolidation: "AVERAGE", TimeoutMs: 5000}}}); err != nil {
			return err
		}
		for {
			message, err := stream.Recv()
			if err != nil {
				return err
			}
			if result := message.GetResult(); result != nil {
				if message.Id != "after-probe" || result.Error != "" || string(result.Json) != `{"source":"pve_rrd"}` {
					t.Errorf("query after failed probe: %v", message)
				}
				close(verified)
				return nil
			}
		}
	}}
	var posts atomic.Int32
	server := rpcHTTPServer(t, service, &posts)
	destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/api/node/metrics", Key: "secret"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer destination.closeRPC()
	helper := virt.NewClient(socket)
	defer helper.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	err = destination.querySession(ctx, helper, "1.0.0")
	select {
	case <-verified:
	default:
		t.Fatalf("failed probe terminated a healthy session: %v", err)
	}
}

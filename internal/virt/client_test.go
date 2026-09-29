//go:build linux

package virt

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCapabilitiesRemainAvailableWhileHistoryConnectionsAreBusy(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "pve.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/capabilities" {
			_, _ = w.Write([]byte(`{"version":"1.0.0","history":true}`))
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		entered <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write([]byte(`{"source":"pve_rrd"}`))
		case <-r.Context().Done():
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	client := NewClient(socket)
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan error, 4)
	defer func() { cancel(); wg.Wait() }()
	for i := range 4 {
		wg.Go(func() {
			_, err := client.History(ctx, HistoryQuery{VMID: 100 + i, Timeframe: "hour", Consolidation: "AVERAGE"})
			results <- err
		})
	}
	for range 4 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("history queries did not occupy all connections")
		}
	}
	caps, err := client.Capabilities(ctx)
	if err != nil || !caps.History || caps.Version != "1.0.0" {
		t.Fatalf("capability probe blocked by history: %+v, %v", caps, err)
	}
	close(release)
	for range 4 {
		if err := <-results; err != nil {
			t.Fatalf("probe interrupted history: %v", err)
		}
	}
}

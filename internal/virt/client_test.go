//go:build linux

package virt

import (
	"context"
	"encoding/json"
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
		if r.URL.Path == "/snapshot" {
			now := time.Now().UTC()
			sampled := now.Add(-time.Minute)
			_ = json.NewEncoder(w).Encode(Snapshot{Schema: 1, Provider: "pve", Host: "pve", CollectedAt: now, LastSuccessAt: &now, TTLSeconds: 90, Status: "ok", VMs: []VM{{ID: 100, Status: "running", CollectedAt: &sampled, TTLSeconds: 90, CPU: new(0.25)}}})
			return
		}
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
	snapshot, err := client.Snapshot(ctx)
	if err != nil || len(snapshot.VMs) != 1 || snapshot.VMs[0].CollectedAt == nil || !snapshot.VMs[0].CollectedAt.Before(snapshot.CollectedAt) {
		t.Fatalf("snapshot blocked by history or refreshed VM timestamp: %+v, %v", snapshot, err)
	}
	close(release)
	for range 4 {
		if err := <-results; err != nil {
			t.Fatalf("probe interrupted history: %v", err)
		}
	}
}

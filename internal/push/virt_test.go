package push

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"Ithiltir-node/internal/reportcfg"
	"Ithiltir-node/internal/virt"
)

func TestVirtPushUsesSeparateAuthenticatedEndpoint(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached", true: "missing"}[missing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "virt.json")
			t.Setenv("ITHILTIR_NODE_VIRT_CACHE", path)
			sampled := time.Now().UTC().Add(-5 * time.Minute)
			if !missing {
				raw, err := json.Marshal(virt.Snapshot{Schema: 1, Provider: "pve", Host: "pve", CollectedAt: sampled, LastSuccessAt: &sampled, TTLSeconds: 90, Status: "ok", VMs: []virt.VM{}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			received := make(chan virt.Snapshot, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/prefix/api/node/virt" || r.Header.Get("X-Node-Secret") != "test-secret" || r.Method != "POST" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(400)
					return
				}
				var snapshot virt.Snapshot
				if err := json.NewDecoder(r.Body).Decode(&snapshot); err != nil {
					t.Error(err)
				}
				received <- snapshot
				w.WriteHeader(204)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			destination, err := newTarget(reportcfg.Target{ID: 1, URL: server.URL + "/prefix/api/node/metrics", Key: "test-secret"}, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			defer destination.closeRPC()
			wg := startVirt(ctx, []*target{destination})
			defer func() { cancel(); wg.Wait() }()
			select {
			case snapshot := <-received:
				if err := snapshot.Validate(); err != nil {
					t.Fatal(err)
				}
				if missing && (snapshot.Status != "error" || snapshot.LastSuccessAt != nil) {
					t.Fatalf("missing cache reported as success: %+v", snapshot)
				}
				if !missing && !snapshot.CollectedAt.Equal(sampled) {
					t.Fatal("old cache timestamp was refreshed")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("no VM report")
			}
		})
	}
}

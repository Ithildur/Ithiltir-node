//go:build linux

package pve

import (
	"testing"
	"time"

	"Ithiltir-node/internal/virt"
)

func TestPVEObservationPreservesUnknownAndStoppedVMs(t *testing.T) {
	vms, err := decode([]byte(`[{"vmid":102,"name":"template","status":"stopped","template":1,"maxmem":2147483648},{"vmid":101,"name":"guest","status":"running","qmpstatus":"paused","cpu":0,"cpus":2,"mem":0,"netin":0}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vms) != 2 || vms[0].ID != 101 || vms[0].QMPStatus != "paused" || vms[0].CPU == nil || *vms[0].CPU != 0 || vms[1].CPU != nil || !vms[1].Template || vms[1].Status != "stopped" {
		t.Fatalf("unexpected observations: %+v", vms)
	}
	now := time.Now().UTC()
	snapshot := virt.Snapshot{Schema: 1, Provider: "pve", Host: "pve", CollectedAt: now, LastSuccessAt: &now, TTLSeconds: 90, Status: "ok", VMs: vms}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := applyCPU(vms, []byte(`[{"vmid":101,"node":"other","type":"qemu","status":"running","cpu":0.9},{"vmid":101,"node":"pve","type":"qemu","status":"running","cpu":0.25},{"vmid":102,"node":"pve","type":"lxc","status":"running","cpu":0.8}]`), "pve"); err != nil {
		t.Fatal(err)
	}
	if vms[0].CPU == nil || *vms[0].CPU != 0.25 || vms[1].CPU != nil {
		t.Fatalf("wrong CPU source: %+v", vms)
	}
	if err := applyCPU(vms, []byte(`[]`), "pve"); err != nil {
		t.Fatal(err)
	}
	if vms[0].CPU != nil {
		t.Fatal("missing CPU observation became zero")
	}
	for _, raw := range []string{`null`, `{}`, `[`, `[{"vmid":1,"status":"running","mem":-1}]`, `[{"vmid":1,"status":"running"},{"vmid":1,"status":"stopped"}]`} {
		vms, err := decode([]byte(raw))
		snapshot.VMs = vms
		if err == nil && snapshot.Validate() == nil {
			t.Fatalf("accepted invalid PVE payload %s", raw)
		}
	}
	vms, err = decode([]byte(`[]`))
	if err != nil || vms == nil || len(vms) != 0 {
		t.Fatalf("empty list: %v, %v", vms, err)
	}
}

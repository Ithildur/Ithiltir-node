//go:build linux

package pve

import (
	"context"
	"time"

	"Ithiltir-node/internal/virt"
)

func Refresh(ctx context.Context, host, output, dir string, gid int) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	vms, collectErr := Collect(ctx, host)
	now := time.Now().UTC()
	if collectErr == nil {
		AttachGuestIPs(dir, host, vms, now)
	}
	snapshot := virt.Snapshot{Schema: 1, Provider: "pve", Host: host, CollectedAt: now, TTLSeconds: 90, Status: "ok", LastSuccessAt: &now, VMs: vms}
	if collectErr == nil {
		collectErr = snapshot.Validate()
	}
	if collectErr != nil {
		snapshot.Status, snapshot.Error, snapshot.LastSuccessAt, snapshot.VMs = "error", virt.ErrorText(collectErr), nil, nil
		if previous, err := virt.Read(output); err == nil && previous.Host == host && !previous.CollectedAt.After(now) {
			snapshot.LastSuccessAt, snapshot.VMs = previous.LastSuccessAt, previous.VMs
		}
	}
	if err := Write(output, snapshot, gid); err != nil {
		return err
	}
	return collectErr
}

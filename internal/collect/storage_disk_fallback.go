package collect

import (
	"path/filepath"
	"sort"
	"strings"

	"Ithiltir-node/internal/metrics"
)

func fallbackStorages(fs []metrics.DiskUsage) []metrics.StorageUsage {
	type agg struct {
		total       uint64
		used        uint64
		free        uint64
		mountpoints map[string]bool
		kind        string
	}

	m := make(map[string]*agg)
	for _, u := range fs {
		key := strings.TrimSpace(u.Device)
		if key == "" {
			key = strings.TrimSpace(u.Mountpoint)
		}
		if key == "" {
			key = strings.TrimSpace(u.Path)
		}
		if key == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(u.FsType), "zfs") {
			continue
		}

		a := m[key]
		if a == nil {
			kind := "disk"
			base := strings.ToLower(filepath.Base(key))
			if strings.HasPrefix(base, "md") || strings.Contains(strings.ToLower(key), "/dev/md") {
				kind = "raid"
			}
			a = &agg{mountpoints: make(map[string]bool), kind: kind}
			m[key] = a
		}

		if mp := strings.TrimSpace(u.Mountpoint); mp != "" {
			a.mountpoints[mp] = true
		}

		if u.Total > a.total {
			a.total = u.Total
			a.used = u.Used
			a.free = u.Free
		}
	}

	out := make([]metrics.StorageUsage, 0, len(m))
	for name, a := range m {
		mps := make([]string, 0, len(a.mountpoints))
		for mp := range a.mountpoints {
			mps = append(mps, mp)
		}
		sort.Strings(mps)
		out = append(out, metrics.StorageUsage{
			Kind:        a.kind,
			Name:        name,
			Total:       a.total,
			Used:        a.used,
			Free:        a.free,
			UsedRatio:   ratioFromUsedFree(a.used, a.free),
			Mountpoints: mps,
		})
	}
	return out
}

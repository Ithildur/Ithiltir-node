//go:build linux

package collect

import (
	"sort"
	"strings"

	"Ithiltir-node/internal/metrics"
)

func isRaidNode(d lsblkDev) bool {
	k := strings.TrimSpace(d.KName)
	t := strings.ToLower(strings.TrimSpace(d.Type))
	if strings.HasPrefix(k, "md") {
		return true
	}
	if strings.HasPrefix(t, "raid") {
		return true
	}
	return false
}

func collectMounts(d lsblkDev, set map[string]bool) {
	if mp := strings.TrimSpace(d.Mountpoint); mp != "" && strings.TrimSpace(d.Fstype) != "" {
		set[mp] = true
	}
	for _, c := range d.Children {
		collectMounts(c, set)
	}
}

func collectBlockStorages(ls *lsblkOutput, byMount map[string]metrics.DiskUsage, raid raidSnapshot, usage *fsUsageReader) []metrics.StorageUsage {
	if ls == nil {
		return nil
	}

	raids := raidIndex(raid)
	var out []metrics.StorageUsage

	var walk func(d lsblkDev) bool
	walk = func(d lsblkDev) bool {
		isRaid := isRaidNode(d)
		hasRaid := isRaid

		for _, c := range d.Children {
			if walk(c) {
				hasRaid = true
			}
		}

		if isRaid {
			out = append(out, raidStorage(d, byMount, raids, usage))
		}

		if strings.ToLower(strings.TrimSpace(d.Type)) == "disk" {
			if hasRaid {
				out = append(out, memberDisk(d))
			} else {
				out = append(out, diskStorage(d, byMount, usage))
			}
		}

		return hasRaid
	}

	for _, d := range ls.Blockdevices {
		walk(d)
	}

	return out
}

func raidIndex(raid raidSnapshot) map[string]raidArraySnapshot {
	out := make(map[string]raidArraySnapshot)
	for _, a := range raid.Arrays {
		out[a.Name] = a
	}
	return out
}

func raidStorage(d lsblkDev, byMount map[string]metrics.DiskUsage, raids map[string]raidArraySnapshot, usage *fsUsageReader) metrics.StorageUsage {
	mps, used, free, total := mountUsage(d, byMount, usage)
	name := d.KName
	if name == "" {
		name = d.Name
	}
	if total == 0 {
		total = parseU64(d.Size)
	}

	e := metrics.StorageUsage{
		Kind:        "raid",
		Name:        name,
		Total:       total,
		Used:        used,
		Free:        free,
		UsedRatio:   ratioFromUsedFree(used, free),
		Mountpoints: mps,
	}
	if meta, ok := raids[name]; ok {
		e.Level = meta.Level
		e.Health = meta.Health
	}
	return e
}

func memberDisk(d lsblkDev) metrics.StorageUsage {
	name := d.KName
	if name == "" {
		name = d.Name
	}
	return metrics.StorageUsage{
		Kind:       "disk",
		Name:       name,
		Total:      parseU64(d.Size),
		Model:      strings.TrimSpace(d.Model),
		Serial:     strings.TrimSpace(d.Serial),
		Rotational: parseBool01(d.Rota),
		Used:       0,
		Free:       0,
		UsedRatio:  0,
	}
}

func diskStorage(d lsblkDev, byMount map[string]metrics.DiskUsage, usage *fsUsageReader) metrics.StorageUsage {
	mps, used, free, total := mountUsage(d, byMount, usage)
	name := d.KName
	if name == "" {
		name = d.Name
	}
	if total == 0 {
		total = parseU64(d.Size)
	}
	return metrics.StorageUsage{
		Kind:        "disk",
		Name:        name,
		Total:       total,
		Used:        used,
		Free:        free,
		UsedRatio:   ratioFromUsedFree(used, free),
		Mountpoints: mps,
		Model:       strings.TrimSpace(d.Model),
		Serial:      strings.TrimSpace(d.Serial),
		Rotational:  parseBool01(d.Rota),
	}
}

func mountUsage(d lsblkDev, byMount map[string]metrics.DiskUsage, usage *fsUsageReader) ([]string, uint64, uint64, uint64) {
	mpSet := make(map[string]bool)
	collectMounts(d, mpSet)

	mps := make([]string, 0, len(mpSet))
	var used, free, total uint64
	for mp := range mpSet {
		mps = append(mps, mp)
	}
	sort.Strings(mps)

	for _, mp := range mps {
		if u, ok := byMount[mp]; ok {
			used += u.Used
			free += u.Free
			total += u.Total
		} else if du, err := usage.read(mp); err == nil && du != nil {
			used += du.Used
			free += du.Free
			total += du.Total
		}
	}
	return mps, used, free, total
}

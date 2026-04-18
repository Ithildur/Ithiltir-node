package collect

import (
	"strings"

	"Ithiltir-node/internal/metrics"
)

func filterFilesystems(fs []metrics.DiskUsage, aliases mapperAliases) []metrics.DiskUsage {
	if len(fs) == 0 {
		return nil
	}
	out := make([]metrics.DiskUsage, 0, len(fs))
	for _, u := range fs {
		if isPseudoFS(u) {
			continue
		}
		if dropFS(u) {
			continue
		}
		if alias := mapperAlias(aliases, normalizeDeviceName(u.Device), u.Device); alias != "" {
			u.Device = alias
		}
		out = append(out, u)
	}
	return out
}

func buildMountMeta(fs []metrics.DiskUsage) map[string]metrics.DiskMountpoint {
	if len(fs) == 0 {
		return nil
	}
	out := make(map[string]metrics.DiskMountpoint, len(fs))
	for _, u := range fs {
		mp := strings.TrimSpace(u.Mountpoint)
		if mp == "" {
			continue
		}
		meta := metrics.DiskMountpoint{
			FsType: u.FsType,
		}
		if u.InodesTotal > 0 {
			meta.InodesTotal = u.InodesTotal
			meta.InodesUsed = u.InodesUsed
			meta.InodesFree = u.InodesFree
			meta.InodesUsedRatio = u.InodesUsedRatio
		}
		out[mp] = meta
	}
	return out
}

func buildMounts(mps []string, meta map[string]metrics.DiskMountpoint) map[string]metrics.DiskMountpoint {
	if len(mps) == 0 || len(meta) == 0 {
		return nil
	}
	out := make(map[string]metrics.DiskMountpoint)
	for _, mp := range mps {
		mp = strings.TrimSpace(mp)
		if mp == "" {
			continue
		}
		if v, ok := meta[mp]; ok {
			out[mp] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isPseudoFS(u metrics.DiskUsage) bool {
	ft := strings.ToLower(strings.TrimSpace(u.FsType))
	if ft == "" {
		return false
	}
	if isPseudoFsType(ft) {
		return true
	}
	mp := strings.TrimSpace(u.Mountpoint)
	return strings.HasPrefix(mp, "/proc") || strings.HasPrefix(mp, "/sys") || strings.HasPrefix(mp, "/dev")
}

func isPseudoFsType(ft string) bool {
	switch ft {
	case "proc", "sysfs", "devtmpfs", "devpts", "tmpfs", "cgroup", "cgroup2",
		"pstore", "bpf", "tracefs", "debugfs", "securityfs", "hugetlbfs",
		"configfs", "fusectl", "mqueue", "autofs", "rpc_pipefs",
		"binfmt_misc", "overlay", "aufs", "fuse.lxcfs",
		"squashfs", "efivarfs", "nsfs", "ramfs":
		return true
	default:
		return false
	}
}

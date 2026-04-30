package collect

import (
	"strings"

	"Ithiltir-node/internal/metrics"
)

const (
	rootRoleScore = 100
	pveRoleScore  = 50
)

func skipDisk(deviceName string, mountpoints []string) bool {
	return isFuseDevice(deviceName) || hasMountpoint(mountpoints, "/boot", "/boot/efi")
}

func dropFS(fs metrics.DiskUsage) bool {
	return dropPVEFS(fs)
}

func dropPVEFS(fs metrics.DiskUsage) bool {
	if strings.ToLower(strings.TrimSpace(fs.FsType)) != "zfs" {
		return false
	}
	dev := strings.TrimSpace(fs.Device)
	if dev == "" {
		return false
	}
	if dev != "rpool" && !strings.HasPrefix(dev, "rpool/") {
		return false
	}
	mp := strings.TrimSpace(fs.Mountpoint)
	return mp != "/" && mp != "/var/lib/vz"
}

func fsRoleScore(fs metrics.DiskUsage) (int, bool) {
	if score, ok := rootFSScore(fs); ok {
		return score, true
	}
	if score, ok := pveFSScore(fs); ok {
		return score, true
	}
	return 0, false
}

func rootFSScore(fs metrics.DiskUsage) (int, bool) {
	if strings.TrimSpace(fs.Mountpoint) == "/" {
		return rootRoleScore, true
	}
	return 0, false
}

func pveFSScore(fs metrics.DiskUsage) (int, bool) {
	mp := strings.TrimSpace(fs.Mountpoint)
	switch {
	case mp == "/var/lib/vz":
		return pveRoleScore, true
	case strings.HasPrefix(mp, "/mnt/pve/"):
		return pveRoleScore, true
	default:
		return 0, false
	}
}

func hasMountpoint(mountpoints []string, wants ...string) bool {
	for _, mp := range mountpoints {
		mp = strings.TrimSpace(mp)
		for _, want := range wants {
			if mp == want {
				return true
			}
		}
	}
	return false
}

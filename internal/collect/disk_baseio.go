package collect

import (
	"sort"
	"strings"

	"Ithiltir-node/internal/metrics"
)

func buildBaseIOCandidates(logical []logicalSnapshot) []metrics.StaticDiskBaseIO {
	candidates := make([]metrics.StaticDiskBaseIO, 0, len(logical))
	for _, l := range logical {
		if strings.TrimSpace(l.Name) == "" {
			continue
		}
		info := kindInfo(l.Kind)
		baseKind := info.baseKind
		if baseKind == "" {
			continue
		}

		devicePath := l.DevicePath
		if baseKind == "raid" {
			devicePath = buildDevicePath(l.Name)
		}
		candidates = append(candidates, metrics.StaticDiskBaseIO{
			Kind:       baseKind,
			Name:       l.Name,
			DevicePath: devicePath,
			Ref:        buildRef(info.ref, l.Name),
		})
	}
	return candidates
}

func sortBaseIO(candidates []metrics.StaticDiskBaseIO, logical []logicalSnapshot, filesystems []metrics.DiskUsage) {
	roleScores := buildRoleScores(logical, filesystems)
	applyRoles(candidates, roleScores)

	sort.Slice(candidates, func(i, j int) bool {
		ri := rolePriority(candidates[i].Role)
		rj := rolePriority(candidates[j].Role)
		if ri != rj {
			return ri < rj
		}
		ki := kindPriority(candidates[i].Kind)
		kj := kindPriority(candidates[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return candidates[i].Name < candidates[j].Name
	})
}

func buildBaseIO(logical []logicalSnapshot, physical []metrics.DiskPhysical, filesystems []metrics.DiskUsage, zfsRates map[string]zfsIORates) []metrics.DiskBaseIO {
	physMap := make(map[string]metrics.DiskIO)
	for _, p := range physical {
		name := normalizeDeviceName(p.Name)
		if name == "" {
			continue
		}
		physMap[name] = p.DiskIO
	}

	candidates := buildBaseIOCandidates(logical)
	if len(candidates) == 0 {
		for _, p := range physical {
			if strings.TrimSpace(p.Name) == "" {
				continue
			}
			candidates = append(candidates, metrics.StaticDiskBaseIO{
				Kind:       "physical",
				Name:       p.Name,
				DevicePath: buildDevicePath(p.Name),
				Ref:        buildRef("disk", p.Name),
			})
		}
	}

	sortBaseIO(candidates, logical, filesystems)
	out := make([]metrics.DiskBaseIO, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, fillBaseIO(c, physMap, zfsRates))
	}
	return out
}

func fillBaseIO(candidate metrics.StaticDiskBaseIO, physMap map[string]metrics.DiskIO, zfsRates map[string]zfsIORates) metrics.DiskBaseIO {
	b := metrics.DiskBaseIO{
		Kind:       candidate.Kind,
		Name:       candidate.Name,
		DevicePath: candidate.DevicePath,
		Ref:        candidate.Ref,
	}
	if b.Kind == "physical" || b.Kind == "raid" {
		if st, ok := physMap[normalizeDeviceName(b.Name)]; ok {
			b.ReadBytes = st.ReadBytes
			b.WriteBytes = st.WriteBytes
			b.ReadRateBytesPerSec = st.ReadRateBytesPerSec
			b.WriteRateBytesPerSec = st.WriteRateBytesPerSec
			b.ReadIOPS = st.ReadIOPS
			b.WriteIOPS = st.WriteIOPS
			b.IOPS = st.IOPS
			b.UtilRatio = st.UtilRatio
			b.QueueLength = st.QueueLength
			b.WaitMs = st.WaitMs
			b.ServiceMs = st.ServiceMs
			return b
		}
	}

	if b.Kind == "logical" {
		if r, ok := zfsRates[b.Name]; ok {
			b.ReadRateBytesPerSec = r.readBps
			b.WriteRateBytesPerSec = r.writeBps
			b.ReadIOPS = r.readIOPS
			b.WriteIOPS = r.writeIOPS
			b.IOPS = r.readIOPS + r.writeIOPS
		}
	}
	return b
}

func buildRoleScores(logical []logicalSnapshot, filesystems []metrics.DiskUsage) map[string]int {
	scores := make(map[string]int)
	for _, fs := range filesystems {
		score, ok := fsRoleScore(fs)
		if !ok {
			continue
		}
		if name := logicalNameForFS(fs, logical); name != "" {
			if score > scores[name] {
				scores[name] = score
			}
		}
	}
	return scores
}

func logicalNameForFS(fs metrics.DiskUsage, logical []logicalSnapshot) string {
	dev := strings.TrimSpace(fs.Device)
	if strings.ToLower(strings.TrimSpace(fs.FsType)) == "zfs" {
		if dev == "" {
			return ""
		}
		pool := strings.SplitN(dev, "/", 2)[0]
		if pool == "" {
			return ""
		}
		for _, l := range logical {
			if l.Kind == "zfs_pool" && l.Name == pool {
				return l.Name
			}
		}
		return ""
	}

	normDev := normalizeDeviceName(dev)
	if normDev != "" {
		for _, l := range logical {
			if l.Kind != "raid_md" && l.Kind != "disk" {
				continue
			}
			if normalizeDeviceName(l.Name) == normDev || l.Name == dev || l.DevicePath == dev {
				return l.Name
			}
		}
	}

	mp := strings.TrimSpace(fs.Mountpoint)
	if mp == "" {
		mp = strings.TrimSpace(fs.Path)
	}
	if mp == "" {
		return ""
	}

	bestName := ""
	bestScore := -2
	for _, l := range logical {
		name := strings.TrimSpace(l.Name)
		if name == "" {
			continue
		}
		if l.Mountpoint != mp {
			if len(l.Mountpoints) == 0 {
				continue
			}
			if _, ok := l.Mountpoints[mp]; !ok {
				continue
			}
		}
		score := kindInfo(l.Kind).roleScore
		if score < 0 {
			continue
		}
		if score > bestScore || (score == bestScore && (bestName == "" || name < bestName)) {
			bestScore = score
			bestName = name
		}
	}
	return bestName
}

func applyRoles(candidates []metrics.StaticDiskBaseIO, scores map[string]int) {
	bestIdx := 0
	bestScore := 0
	for i, c := range candidates {
		score := scores[c.Name]
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	for i := range candidates {
		if i == bestIdx {
			candidates[i].Role = "primary"
		} else {
			candidates[i].Role = "secondary"
		}
	}
}

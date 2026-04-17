package collect

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"Ithiltir-node/internal/metrics"
)

type logicalSnapshot struct {
	Kind        string
	Name        string
	DevicePath  string
	Ref         string
	Total       uint64
	Used        uint64
	Free        uint64
	UsedRatio   float64
	Health      string
	Level       string
	Mountpoint  string
	Mountpoints map[string]metrics.DiskMountpoint
	Devices     []string
}

type baseIOCandidate struct {
	Kind       string
	Name       string
	DevicePath string
	Ref        string
	Role       string

	ReadBytes            uint64
	WriteBytes           uint64
	ReadRateBytesPerSec  float64
	WriteRateBytesPerSec float64
	ReadIOPS             float64
	WriteIOPS            float64
	IOPS                 float64
	UtilRatio            float64
	QueueLength          float64
	WaitMs               float64
	ServiceMs            float64
}

type zfsIORates struct {
	readBps   float64
	writeBps  float64
	readIOPS  float64
	writeIOPS float64
}

func parseZFSIORates(s string) map[string]zfsIORates {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	type agg struct {
		rBpsSum  float64
		wBpsSum  float64
		rIOPSSum float64
		wIOPSSum float64
		n        float64
	}

	seenFirst := make(map[string]bool)
	acc := make(map[string]*agg)
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 7 {
			continue
		}
		pool := strings.TrimSpace(fields[0])
		if pool == "" {
			continue
		}
		if !seenFirst[pool] {
			seenFirst[pool] = true
			continue
		}

		a := acc[pool]
		if a == nil {
			a = &agg{}
			acc[pool] = a
		}
		a.rIOPSSum += parseFloat(fields[3])
		a.wIOPSSum += parseFloat(fields[4])
		a.rBpsSum += parseFloat(fields[5])
		a.wBpsSum += parseFloat(fields[6])
		a.n++
	}
	if len(acc) == 0 {
		return nil
	}

	out := make(map[string]zfsIORates, len(acc))
	for pool, a := range acc {
		if a.n == 0 {
			continue
		}
		out[pool] = zfsIORates{
			readBps:   ceilRate(a.rBpsSum / a.n),
			writeBps:  ceilRate(a.wBpsSum / a.n),
			readIOPS:  ceilRate(a.rIOPSSum / a.n),
			writeIOPS: ceilRate(a.wIOPSSum / a.n),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildPhysical(m map[string]metrics.DiskIO) []metrics.DiskPhysical {
	if len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]metrics.DiskPhysical, 0, len(names))
	for _, name := range names {
		out = append(out, metrics.DiskPhysical{
			Name:       name,
			DevicePath: buildDevicePath(name),
			Ref:        buildRef("disk", name),
			DiskIO:     m[name],
		})
	}
	return out
}

func buildLogicalMetrics(logical []logicalSnapshot) []metrics.DiskLogical {
	if len(logical) == 0 {
		return nil
	}
	out := make([]metrics.DiskLogical, 0, len(logical))
	for _, l := range logical {
		out = append(out, metrics.DiskLogical{
			Kind:       l.Kind,
			Name:       l.Name,
			DevicePath: l.DevicePath,
			Ref:        l.Ref,
			Used:       l.Used,
			Free:       l.Free,
			UsedRatio:  l.UsedRatio,
			Health:     l.Health,
		})
	}
	return out
}

func buildLogical(storages []metrics.StorageUsage, raid raidSnapshot, filesystems []metrics.DiskUsage, aliases mapperAliases) []logicalSnapshot {
	raidDevices := buildRaidDevices(raid)
	mountMeta := buildMountMeta(filesystems)
	seen := make(map[string]logicalSnapshot)
	var logical []logicalSnapshot
	var disks []logicalSnapshot

	add := func(l logicalSnapshot) {
		key := l.Kind + ":" + l.Name
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = l
		logical = append(logical, l)
	}

	for _, s := range storages {
		rawName := strings.TrimSpace(s.Name)
		name := normalizeDeviceName(rawName)
		if name == "" {
			continue
		}
		if s.Kind == "disk" && skipDisk(name, s.Mountpoints) {
			continue
		}
		kind := normalizeKind(s)
		devicePath := logicalPath(kind, name)
		if alias := mapperAlias(aliases, name, rawName); alias != "" {
			name = filepath.Base(alias)
			devicePath = alias
		}
		if strings.HasPrefix(rawName, "/dev/mapper/") {
			devicePath = rawName
		}
		ld := logicalSnapshot{
			Kind:       kind,
			Name:       name,
			DevicePath: devicePath,
			Ref:        buildRef(refKind(kind), name),
			Total:      s.Total,
			Used:       s.Used,
			Free:       s.Free,
			UsedRatio:  s.UsedRatio,
			Health:     s.Health,
			Level:      s.Level,
		}
		if supportsMounts(kind) {
			ld.Mountpoints = buildMounts(s.Mountpoints, mountMeta)
			ld.Mountpoint = firstMount(s.Mountpoints)
		}
		if len(s.Devices) > 0 {
			ld.Devices = uniqueStrings(s.Devices)
		}

		switch s.Kind {
		case "zfs_pool", "raid":
			if devs, ok := raidDevices[normalizeDeviceName(name)]; ok && len(ld.Devices) == 0 {
				ld.Devices = devs
			}
			add(ld)
		case "lvm_thinpool", "lvm_vg":
			add(ld)
		case "disk":
			disks = append(disks, ld)
		}
	}

	hasHigherLevel := false
	for _, l := range logical {
		if l.Kind == "zfs_pool" || l.Kind == "raid_md" || l.Kind == "lvm_vg" {
			hasHigherLevel = true
			break
		}
	}

	if !hasHigherLevel {
		for _, d := range disks {
			if d.Used == 0 && d.Free == 0 && d.UsedRatio == 0 {
				continue
			}
			add(d)
		}
	}

	sort.Slice(logical, func(i, j int) bool {
		if logical[i].Kind != logical[j].Kind {
			return logical[i].Kind < logical[j].Kind
		}
		return logical[i].Name < logical[j].Name
	})
	return logical
}

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

func buildBaseIOCandidates(logical []logicalSnapshot) []baseIOCandidate {
	candidates := make([]baseIOCandidate, 0)
	for _, l := range logical {
		switch l.Kind {
		case "zfs_pool", "ceph_pool":
			if strings.TrimSpace(l.Name) == "" {
				continue
			}
			candidates = append(candidates, baseIOCandidate{
				Kind:       "logical",
				Name:       l.Name,
				DevicePath: l.DevicePath,
				Ref:        buildRef(refKind(l.Kind), l.Name),
			})
		case "raid_md":
			if strings.TrimSpace(l.Name) == "" {
				continue
			}
			candidates = append(candidates, baseIOCandidate{
				Kind:       "raid",
				Name:       l.Name,
				DevicePath: buildDevicePath(l.Name),
				Ref:        buildRef("raid", l.Name),
			})
		}
	}
	return candidates
}

func sortBaseIO(candidates []baseIOCandidate, logical []logicalSnapshot, filesystems []metrics.DiskUsage) {
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
			candidates = append(candidates, baseIOCandidate{
				Kind:       "physical",
				Name:       p.Name,
				DevicePath: buildDevicePath(p.Name),
				Ref:        buildRef("disk", p.Name),
			})
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	sortBaseIO(candidates, logical, filesystems)
	for i := range candidates {
		candidates[i] = fillBaseIO(candidates[i], physMap, zfsRates)
	}
	out := make([]metrics.DiskBaseIO, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, metrics.DiskBaseIO{
			Kind:                 c.Kind,
			Name:                 c.Name,
			DevicePath:           c.DevicePath,
			Ref:                  c.Ref,
			ReadBytes:            c.ReadBytes,
			WriteBytes:           c.WriteBytes,
			ReadRateBytesPerSec:  c.ReadRateBytesPerSec,
			WriteRateBytesPerSec: c.WriteRateBytesPerSec,
			ReadIOPS:             c.ReadIOPS,
			WriteIOPS:            c.WriteIOPS,
			IOPS:                 c.IOPS,
			UtilRatio:            c.UtilRatio,
			QueueLength:          c.QueueLength,
			WaitMs:               c.WaitMs,
			ServiceMs:            c.ServiceMs,
		})
	}
	return out
}

func fillBaseIO(b baseIOCandidate, physMap map[string]metrics.DiskIO, zfsRates map[string]zfsIORates) baseIOCandidate {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return b
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
		if zfsRates != nil {
			if r, ok := zfsRates[b.Name]; ok {
				b.ReadRateBytesPerSec = r.readBps
				b.WriteRateBytesPerSec = r.writeBps
				b.ReadIOPS = r.readIOPS
				b.WriteIOPS = r.writeIOPS
				b.IOPS = r.readIOPS + r.writeIOPS
			}
		}
	}
	return b
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

func isFuseDevice(name string) bool {
	return normalizeDeviceName(name) == "fuse"
}

func buildRaidDevices(raid raidSnapshot) map[string][]string {
	out := make(map[string][]string)
	for _, a := range raid.Arrays {
		name := normalizeDeviceName(a.Name)
		if name == "" {
			continue
		}
		devs := make([]string, 0, len(a.MemberStates))
		for _, m := range a.MemberStates {
			d := normalizeDeviceName(m.Name)
			if d != "" {
				devs = append(devs, d)
			}
		}
		out[name] = uniqueStrings(devs)
	}
	return out
}

func normalizeDeviceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(name, `\\?\`) {
			name = strings.TrimPrefix(name, `\\?\`)
		}
		name = strings.TrimRight(name, `\/`)
		if vol := filepath.VolumeName(name); vol != "" {
			return vol
		}
		return filepath.Base(name)
	}
	if strings.HasPrefix(name, "/dev/") {
		if resolved, err := filepath.EvalSymlinks(name); err == nil {
			name = resolved
		}
		name = filepath.Base(name)
	} else {
		name = filepath.Base(name)
	}
	return name
}

func normalizeKind(s metrics.StorageUsage) string {
	switch s.Kind {
	case "raid":
		return "raid_md"
	case "disk":
		return "disk"
	case "zfs_pool":
		return "zfs_pool"
	case "lvm_thinpool":
		return "lvm_thinpool"
	case "lvm_vg":
		return "lvm_vg"
	default:
		return s.Kind
	}
}

func supportsMounts(kind string) bool {
	switch kind {
	case "disk", "raid_md", "zfs_pool", "lvm_vg", "lvm_thinpool":
		return true
	default:
		return false
	}
}

func firstMount(mps []string) string {
	if len(mps) == 0 {
		return ""
	}
	sorted := append([]string(nil), mps...)
	sort.Strings(sorted)
	return sorted[0]
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func buildDevicePath(name string) string {
	if name == "" {
		return ""
	}
	if runtime.GOOS == "windows" {
		return ""
	}
	if strings.Contains(name, "/") {
		return ""
	}
	return "/dev/" + name
}

func logicalPath(kind, name string) string {
	if kind == "zfs_pool" || kind == "ceph_pool" || kind == "lvm_vg" || kind == "lvm_thinpool" {
		return ""
	}
	return buildDevicePath(name)
}

type mapperAliases map[string]string

func buildMapperAliases() mapperAliases {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return nil
	}
	ents, err := os.ReadDir("/dev/mapper")
	if err != nil {
		return nil
	}
	out := make(map[string]string)
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		p := filepath.Join("/dev/mapper", e.Name())
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		if _, ok := out[resolved]; ok {
			continue
		}
		out[resolved] = p
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mapperAlias(aliases mapperAliases, kname, rawName string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return ""
	}
	if strings.HasPrefix(rawName, "/dev/mapper/") {
		return rawName
	}
	kname = strings.TrimSpace(kname)
	if kname == "" {
		return ""
	}
	target := "/dev/" + kname
	if aliases != nil {
		if alias, ok := aliases[target]; ok {
			return alias
		}
		return ""
	}
	return ""
}

func buildRef(kind, name string) string {
	name = strings.TrimSpace(name)
	if kind == "" || name == "" {
		return ""
	}
	return kind + ":" + name
}

func refKind(kind string) string {
	switch kind {
	case "raid_md":
		return "raid"
	case "disk":
		return "disk"
	case "zfs_pool":
		return "zfs"
	case "lvm_vg":
		return "lvm_vg"
	case "lvm_thinpool":
		return "lvm_thinpool"
	default:
		return kind
	}
}

func buildRoleScores(logical []logicalSnapshot, filesystems []metrics.DiskUsage) map[string]int {
	scores := make(map[string]int)
	for _, fs := range filesystems {
		mp := strings.TrimSpace(fs.Mountpoint)
		if mp == "" {
			continue
		}
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
			switch l.Kind {
			case "raid_md", "disk":
				if normalizeDeviceName(l.Name) == normDev || l.Name == dev || l.DevicePath == dev {
					return l.Name
				}
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

	kindScore := func(kind string) int {
		switch kind {
		case "zfs_pool", "ceph_pool":
			return 50
		case "raid_md":
			return 40
		case "disk":
			return 0
		default:
			return -1
		}
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
		score := kindScore(l.Kind)
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

func applyRoles(candidates []baseIOCandidate, scores map[string]int) {
	if len(candidates) == 0 {
		return
	}
	bestIdx := -1
	bestScore := 0
	for i, c := range candidates {
		score := scores[c.Name]
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		for i := range candidates {
			if i == bestIdx {
				candidates[i].Role = "primary"
			} else {
				candidates[i].Role = "secondary"
			}
		}
		return
	}

	for i := range candidates {
		if i == 0 {
			candidates[i].Role = "primary"
		} else {
			candidates[i].Role = "secondary"
		}
	}
}

func rolePriority(role string) int {
	switch role {
	case "primary":
		return 0
	case "secondary":
		return 1
	default:
		return 2
	}
}

func kindPriority(kind string) int {
	switch kind {
	case "raid":
		return 0
	case "logical":
		return 1
	case "physical":
		return 2
	default:
		return 9
	}
}

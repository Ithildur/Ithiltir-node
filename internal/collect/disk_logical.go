package collect

import (
	"path/filepath"
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
		kind := normalizeKind(s.Kind)
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

		switch logicalActionFor(s.Kind) {
		case logicalAdd:
			if usesRaidDevices(s.Kind) {
				if devs, ok := raidDevices[normalizeDeviceName(name)]; ok && len(ld.Devices) == 0 {
					ld.Devices = devs
				}
			}
			add(ld)
		case logicalDisk:
			disks = append(disks, ld)
		}
	}

	hasHigherLevel := false
	for _, l := range logical {
		if isHigherLevel(l.Kind) {
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

package collect

type logicalAction uint8

const (
	logicalSkip logicalAction = iota
	logicalAdd
	logicalDisk
)

type diskKindInfo struct {
	logical     string
	ref         string
	mounts      bool
	devicePath  bool
	action      logicalAction
	higherLevel bool
	raidDevices bool
	baseKind    string
	roleScore   int
}

var diskKindTable = map[string]diskKindInfo{
	"disk": {
		logical:    "disk",
		ref:        "disk",
		mounts:     true,
		devicePath: true,
		action:     logicalDisk,
		roleScore:  0,
	},
	"raid": {
		logical:     "raid_md",
		ref:         "raid",
		mounts:      true,
		devicePath:  true,
		action:      logicalAdd,
		raidDevices: true,
		roleScore:   -1,
	},
	"raid_md": {
		logical:     "raid_md",
		ref:         "raid",
		mounts:      true,
		devicePath:  true,
		higherLevel: true,
		baseKind:    "raid",
		roleScore:   40,
	},
	"zfs_pool": {
		logical:     "zfs_pool",
		ref:         "zfs",
		mounts:      true,
		action:      logicalAdd,
		higherLevel: true,
		raidDevices: true,
		baseKind:    "logical",
		roleScore:   50,
	},
	"ceph_pool": {
		logical:   "ceph_pool",
		ref:       "ceph_pool",
		baseKind:  "logical",
		roleScore: 50,
	},
	"lvm_vg": {
		logical:     "lvm_vg",
		ref:         "lvm_vg",
		mounts:      true,
		action:      logicalAdd,
		higherLevel: true,
		roleScore:   -1,
	},
	"lvm_thinpool": {
		logical:   "lvm_thinpool",
		ref:       "lvm_thinpool",
		mounts:    true,
		action:    logicalAdd,
		roleScore: -1,
	},
}

var baseIOKindPriority = map[string]int{
	"raid":     0,
	"logical":  1,
	"physical": 2,
}

var rolePriorityLookup = map[string]int{
	"primary":   0,
	"secondary": 1,
}

func kindInfo(kind string) diskKindInfo {
	if info, ok := diskKindTable[kind]; ok {
		return info
	}
	return diskKindInfo{
		logical:    kind,
		ref:        kind,
		devicePath: true,
		roleScore:  -1,
	}
}

func normalizeKind(kind string) string {
	return kindInfo(kind).logical
}

func supportsMounts(kind string) bool {
	return kindInfo(kind).mounts
}

func logicalActionFor(kind string) logicalAction {
	return kindInfo(kind).action
}

func isHigherLevel(kind string) bool {
	return kindInfo(kind).higherLevel
}

func usesRaidDevices(kind string) bool {
	return kindInfo(kind).raidDevices
}

func logicalPath(kind, name string) string {
	if !kindInfo(kind).devicePath {
		return ""
	}
	return buildDevicePath(name)
}

func refKind(kind string) string {
	return kindInfo(kind).ref
}

func baseKindFor(kind string) string {
	return kindInfo(kind).baseKind
}

func logicalKindScore(kind string) int {
	return kindInfo(kind).roleScore
}

func rolePriority(role string) int {
	if p, ok := rolePriorityLookup[role]; ok {
		return p
	}
	return 2
}

func kindPriority(kind string) int {
	if p, ok := baseIOKindPriority[kind]; ok {
		return p
	}
	return 9
}

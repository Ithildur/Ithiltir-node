package collect

import "Ithiltir-node/internal/metrics"

type raidArraySnapshot struct {
	Name         string
	Level        string
	Status       string
	Devices      int
	Active       int
	Working      int
	Failed       int
	Health       string
	MemberStates []metrics.RaidMember
	SyncStatus   string
	SyncProgress string
}

type raidSnapshot struct {
	Supported bool
	Available bool
	Arrays    []raidArraySnapshot
}

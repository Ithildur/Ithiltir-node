package nodeiface

import (
	"time"

	"Ithiltir-node/internal/metrics"
)

type ReportSource interface {
	Snapshot() *metrics.Snapshot
	Time() time.Time
	Version() string
	Hostname() string
}

type StaticSource interface {
	Static() *metrics.Static
}

type PushSource interface {
	ReportSource
	PushDelay() time.Duration
}

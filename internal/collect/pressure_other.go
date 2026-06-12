//go:build !linux

package collect

import "Ithiltir-node/internal/metrics"

func readPressure() metrics.Pressure {
	return metrics.Pressure{
		CPU:    unsupportedPressureResource(),
		Memory: unsupportedPressureResource(),
		IO:     unsupportedPressureResource(),
	}
}

func unsupportedPressureResource() metrics.PressureResource {
	return metrics.PressureResource{Status: metrics.StatusUnsupported}
}

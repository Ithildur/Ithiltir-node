package push

import (
	"fmt"
	"strings"

	"Ithiltir-node/internal/metrics"
)

type staticError struct {
	fields []string
}

func (e *staticError) Error() string {
	return fmt.Sprintf("static snapshot missing required fields: %s", strings.Join(e.fields, ", "))
}

func validateStatic(snap *metrics.Static) error {
	if snap == nil {
		return fmt.Errorf("static snapshot not ready")
	}

	missing := missingPublishFields(snap)
	if len(missing) > 0 {
		return &staticError{fields: missing}
	}
	return nil
}

func missingPublishFields(snap *metrics.Static) []string {
	missing := make([]string, 0, 3)
	if strings.TrimSpace(snap.Version) == "" {
		missing = append(missing, "version")
	}
	if snap.Timestamp.IsZero() {
		missing = append(missing, "timestamp")
	}
	if snap.ReportIntervalSeconds <= 0 {
		missing = append(missing, "report_interval_seconds")
	}
	return missing
}

func missingCompleteFields(snap *metrics.Static) []string {
	missing := make([]string, 0, 10)
	if strings.TrimSpace(snap.System.Hostname) == "" {
		missing = append(missing, "system.hostname")
	}
	if strings.TrimSpace(snap.System.OS) == "" {
		missing = append(missing, "system.os")
	}
	if strings.TrimSpace(snap.System.Platform) == "" {
		missing = append(missing, "system.platform")
	}
	if strings.TrimSpace(snap.System.PlatformVersion) == "" {
		missing = append(missing, "system.platform_version")
	}
	if strings.TrimSpace(snap.System.KernelVersion) == "" {
		missing = append(missing, "system.kernel_version")
	}
	if strings.TrimSpace(snap.System.Arch) == "" {
		missing = append(missing, "system.arch")
	}
	if snap.Memory.Total == 0 {
		missing = append(missing, "memory.total")
	}
	if snap.CPU.Info.CoresLogical <= 0 {
		missing = append(missing, "cpu.info.cores_logical")
	}
	if snap.CPU.Info.CoresPhysical <= 0 {
		missing = append(missing, "cpu.info.cores_physical")
	}
	if snap.CPU.Info.Sockets <= 0 {
		missing = append(missing, "cpu.info.sockets")
	}
	return missing
}

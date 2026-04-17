package collect

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func formatUptime(seconds uint64) string {
	dur := time.Duration(seconds) * time.Second
	days := dur / (24 * time.Hour)
	dur -= days * 24 * time.Hour
	hours := dur / time.Hour
	dur -= hours * time.Hour
	minutes := dur / time.Minute
	return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
}

func safeDiffUint64(curr, prev uint64) uint64 {
	if curr >= prev {
		return curr - prev
	}
	return 0
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCmd(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%s timeout", name)
	}
	if err != nil {
		argStr := strings.Join(args, " ")
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("%s %s failed: %w: %s", name, argStr, err, stderrStr)
		}
		return nil, fmt.Errorf("%s %s failed: %w", name, argStr, err)
	}
	return out, nil
}

func parseU64(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	u, _ := strconv.ParseUint(s, 10, 64)
	return u
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseBool01(s string) *bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if s == "1" {
		b := true
		return &b
	}
	if s == "0" {
		b := false
		return &b
	}
	return nil
}

func ratioFromUsedFree(used, free uint64) float64 {
	den := float64(used + free)
	if den <= 0 {
		return 0
	}
	return float64(used) / den
}

func percentToRatio(p100 float64) float64 {
	if p100 <= 0 {
		return 0
	}
	if p100 >= 100 {
		return 1
	}
	return p100 / 100.0
}

func minDuration(a, b time.Duration) time.Duration {
	if a <= b {
		return a
	}
	return b
}

func ceilRate(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return math.Ceil(v)
}

func ceilMs3(v float64) float64 {
	if v <= 0 {
		return 0
	}
	return math.Ceil(v*1000.0) / 1000.0
}

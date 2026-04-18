package collect

import "strings"

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

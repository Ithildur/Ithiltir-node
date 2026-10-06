package collect

import "strings"

type zfsIORates struct {
	readBps   float64
	writeBps  float64
	readIOPS  float64
	writeIOPS float64
}

func parseZFSIORates(s string) map[string]zfsIORates {
	type agg struct {
		rBpsSum  float64
		wBpsSum  float64
		rIOPSSum float64
		wIOPSSum float64
		n        float64
	}

	acc := make(map[string]*agg)
	for line := range strings.SplitSeq(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		pool := fields[0]
		a := acc[pool]
		if a == nil {
			// The first sample reports lifetime averages, not interval rates.
			acc[pool] = new(agg)
			continue
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

//go:build linux

package collect

import (
	"log"
	"strings"
	"time"

	"Ithiltir-node/internal/metrics"
)

func collectZFSPools(debug bool) []metrics.StorageUsage {
	if !commandExists("zpool") {
		return nil
	}

	out, err := runCmd(3*time.Second, "zpool", "list", "-H", "-p", "-o", "name,size,alloc,free,health")
	if err != nil {
		if debug {
			log.Printf("DEBUG: zpool list failed: %v", err)
		}
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	res := make([]metrics.StorageUsage, 0, len(lines))

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.Fields(ln)
		if len(parts) < 5 {
			continue
		}

		name := parts[0]
		size := parseU64(parts[1])
		alloc := parseU64(parts[2])
		free := parseU64(parts[3])
		health := parts[4]

		res = append(res, metrics.StorageUsage{
			Kind:      "zfs_pool",
			Name:      name,
			Total:     size,
			Used:      alloc,
			Free:      free,
			UsedRatio: ratioFromUsedFree(alloc, free),
			Health:    health,
		})
	}

	return res
}

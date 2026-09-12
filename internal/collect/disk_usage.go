package collect

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"Ithiltir-node/internal/metrics"

	"github.com/shirou/gopsutil/v3/disk"
)

const (
	defaultUsageTimeout = 800 * time.Millisecond
	defaultUsageWorkers = 4
)

type usageEntry struct {
	cached *disk.UsageStat
	probe  *usageProbe
	stuck  bool
}

type usageProbe struct {
	done  chan struct{}
	usage *disk.UsageStat
	err   error
}

type usageJob struct {
	partition  disk.PartitionStat
	mountpoint string
}

type fsUsageReader struct {
	timeout time.Duration
	workers int
	probe   func(string) (*disk.UsageStat, error)

	mu      sync.Mutex
	entries map[string]*usageEntry
}

func newFSUsageReader() *fsUsageReader {
	return &fsUsageReader{
		timeout: defaultUsageTimeout,
		workers: defaultUsageWorkers,
		probe:   disk.Usage,
		entries: map[string]*usageEntry{},
	}
}

func (r *fsUsageReader) read(path string) (*disk.UsageStat, error) {
	r.mu.Lock()
	entry := r.entries[path]
	if entry == nil {
		entry = &usageEntry{}
		r.entries[path] = entry
	}
	cached := entry.cached
	if entry.stuck {
		r.mu.Unlock()
		if cached != nil {
			return cached, nil
		}
		return nil, context.DeadlineExceeded
	}
	probe := entry.probe
	started := false
	if probe == nil {
		probe = &usageProbe{done: make(chan struct{})}
		entry.probe = probe
		started = true
	}
	r.mu.Unlock()

	if started {
		go r.runProbe(path, entry, probe)
	} else if cached != nil {
		return cached, nil
	}

	return r.waitProbe(entry, probe, cached)
}

func (r *fsUsageReader) runProbe(path string, entry *usageEntry, probe *usageProbe) {
	usage, err := r.probe(path)

	r.mu.Lock()
	probe.usage = usage
	probe.err = err
	if err == nil && usage != nil {
		entry.cached = usage
	}
	entry.probe = nil
	entry.stuck = false
	close(probe.done)
	r.mu.Unlock()
}

func (r *fsUsageReader) waitProbe(entry *usageEntry, probe *usageProbe, cached *disk.UsageStat) (*disk.UsageStat, error) {
	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case <-probe.done:
		if probe.err != nil {
			return nil, probe.err
		}
		if probe.usage != nil {
			return probe.usage, nil
		}
		if cached != nil {
			return cached, nil
		}
		return nil, context.DeadlineExceeded
	case <-timer.C:
		r.mu.Lock()
		if entry.probe == probe {
			entry.stuck = true
		}
		r.mu.Unlock()
		if cached != nil {
			return cached, nil
		}
		return nil, context.DeadlineExceeded
	}
}

func (r *fsUsageReader) collect(parts []disk.PartitionStat, skipPseudo bool) []metrics.DiskUsage {
	jobs := make([]usageJob, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, p := range parts {
		mp := strings.TrimSpace(p.Mountpoint)
		if mp == "" {
			continue
		}
		if _, ok := seen[mp]; ok {
			continue
		}
		seen[mp] = struct{}{}
		if skipPseudo && isPseudoFsType(p.Fstype) {
			continue
		}
		jobs = append(jobs, usageJob{partition: p, mountpoint: mp})
	}

	if len(jobs) == 0 {
		return nil
	}

	jobCh := make(chan usageJob)
	resultCh := make(chan metrics.DiskUsage, len(jobs))
	var wg sync.WaitGroup

	for range min(r.workers, len(jobs)) {
		wg.Go(func() {
			for job := range jobCh {
				usage, err := r.read(job.mountpoint)
				if err != nil || usage == nil {
					continue
				}
				resultCh <- buildUsage(job.partition, job.mountpoint, usage)
			}
		})
	}

	go func() {
		for _, job := range jobs {
			jobCh <- job
		}
		close(jobCh)
		wg.Wait()
		close(resultCh)
	}()

	out := make([]metrics.DiskUsage, 0, len(jobs))
	for metric := range resultCh {
		out = append(out, metric)
	}

	slices.SortFunc(out, func(a, b metrics.DiskUsage) int {
		return cmp.Compare(a.Mountpoint, b.Mountpoint)
	})

	return out
}

func buildUsage(partition disk.PartitionStat, mountpoint string, usage *disk.UsageStat) metrics.DiskUsage {
	var inodesTotal, inodesUsed, inodesFree uint64
	var inodesUsedRatio float64
	if usage.InodesTotal > 0 {
		inodesTotal = usage.InodesTotal
		inodesUsed = usage.InodesUsed
		inodesFree = usage.InodesFree
		inodesUsedRatio = percentToRatio(usage.InodesUsedPercent)
	}

	return metrics.DiskUsage{
		Path:       usage.Path,
		Device:     partition.Device,
		Mountpoint: mountpoint,

		Total:     usage.Total,
		Used:      usage.Used,
		Free:      usage.Free,
		UsedRatio: percentToRatio(usage.UsedPercent),
		FsType:    partition.Fstype,

		InodesTotal:     inodesTotal,
		InodesUsed:      inodesUsed,
		InodesFree:      inodesFree,
		InodesUsedRatio: inodesUsedRatio,
	}
}

package collect

import (
	"context"
	"sort"
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

func (r *fsUsageReader) probeTimeout() time.Duration {
	if r.timeout > 0 {
		return r.timeout
	}
	return defaultUsageTimeout
}

func (r *fsUsageReader) workerCount(jobCount int) int {
	workers := r.workers
	if workers < 1 {
		workers = 1
	}
	if workers > jobCount {
		workers = jobCount
	}
	return workers
}

func (r *fsUsageReader) read(path string) (*disk.UsageStat, error) {
	r.mu.Lock()
	if r.entries == nil {
		r.entries = map[string]*usageEntry{}
	}
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
	probeFn := r.probe
	if probeFn == nil {
		probeFn = disk.Usage
	}
	usage, err := probeFn(path)

	r.mu.Lock()
	probe.usage = usage
	probe.err = err
	if err == nil && usage != nil {
		entry.cached = usage
	}
	if entry.probe == probe {
		entry.probe = nil
	}
	entry.stuck = false
	close(probe.done)
	r.mu.Unlock()
}

func (r *fsUsageReader) waitProbe(entry *usageEntry, probe *usageProbe, cached *disk.UsageStat) (*disk.UsageStat, error) {
	timer := time.NewTimer(r.probeTimeout())
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

	workerCount := r.workerCount(len(jobs))

	jobCh := make(chan usageJob)
	resultCh := make(chan metrics.DiskUsage, len(jobs))
	var wg sync.WaitGroup

	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				usage, err := r.read(job.mountpoint)
				if err != nil || usage == nil {
					continue
				}
				resultCh <- buildUsage(job.partition, job.mountpoint, usage)
			}
		}()
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

	sort.Slice(out, func(i, j int) bool {
		a := out[i].Mountpoint
		if a == "" {
			a = out[i].Path
		}
		b := out[j].Mountpoint
		if b == "" {
			b = out[j].Path
		}
		return a < b
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

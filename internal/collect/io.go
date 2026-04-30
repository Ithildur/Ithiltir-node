package collect

import (
	"sort"
	"strings"
	"sync"
	"time"

	"Ithiltir-node/internal/metrics"

	"github.com/shirou/gopsutil/v3/disk"
	gnet "github.com/shirou/gopsutil/v3/net"
)

type ioSampler struct {
	mu              sync.Mutex
	lastDiskIO      map[string]disk.IOCountersStat
	lastNetIO       map[string]gnet.IOCountersStat
	lastIOTimestamp time.Time
}

func ignoredBlock(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return true
	}
	ignoredPrefixes := []string{
		"loop",
		"ram",
		"zram",
		"fd",
		"sr",
	}
	for _, p := range ignoredPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func mdDevice(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.HasPrefix(name, "md")
}

func (s *ioSampler) sampleIO(preferredNICs []string) (map[string]metrics.DiskIO, []metrics.NetIO) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	dt := now.Sub(s.lastIOTimestamp).Seconds()

	currDisk, err := disk.IOCounters()
	if err != nil {
		currDisk = map[string]disk.IOCountersStat{}
	}
	currDisk = filterDiskIOCounters(currDisk)

	currNet, err := gnet.IOCounters(true)
	if err != nil {
		currNet = []gnet.IOCountersStat{}
	}
	currNetMap := mergeNetCounters(currNet)

	nics := selectedNICs(preferredNICs...)

	if s.lastIOTimestamp.IsZero() || dt <= 0 {
		s.lastIOTimestamp = now
		s.lastDiskIO = currDisk
		s.lastNetIO = make(map[string]gnet.IOCountersStat)

		diskIO := make(map[string]metrics.DiskIO)
		for name, d := range currDisk {
			diskIO[name] = metrics.DiskIO{
				ReadBytes:            d.ReadBytes,
				WriteBytes:           d.WriteBytes,
				ReadRateBytesPerSec:  0,
				WriteRateBytesPerSec: 0,
				IOPS:                 0,
				ReadIOPS:             0,
				WriteIOPS:            0,
				UtilRatio:            0,
				QueueLength:          0,
				WaitMs:               0,
				ServiceMs:            0,
			}
		}

		netIO := make([]metrics.NetIO, 0, len(currNetMap))
		for _, name := range sortedNetKeys(currNetMap) {
			if nics != nil {
				if _, ok := nics[name]; !ok {
					continue
				}
			}
			n := currNetMap[name]
			s.lastNetIO[name] = n
			netIO = append(netIO, metrics.NetIO{
				Name:                  name,
				BytesRecv:             n.BytesRecv,
				BytesSent:             n.BytesSent,
				RecvRateBytesPerSec:   0,
				SentRateBytesPerSec:   0,
				PacketsRecv:           n.PacketsRecv,
				PacketsSent:           n.PacketsSent,
				RecvRatePacketsPerSec: 0,
				SentRatePacketsPerSec: 0,
				ErrIn:                 n.Errin,
				ErrOut:                n.Errout,
				DropIn:                n.Dropin,
				DropOut:               n.Dropout,
			})
		}

		return diskIO, netIO
	}

	diskIO := make(map[string]metrics.DiskIO)
	for name, curr := range currDisk {
		prev, ok := s.lastDiskIO[name]
		if !ok {
			prev = curr
		}
		readDiff := safeDiffUint64(curr.ReadBytes, prev.ReadBytes)
		writeDiff := safeDiffUint64(curr.WriteBytes, prev.WriteBytes)
		readIopsDiff := safeDiffUint64(curr.ReadCount, prev.ReadCount)
		writeIopsDiff := safeDiffUint64(curr.WriteCount, prev.WriteCount)
		iopsDiff := readIopsDiff + writeIopsDiff
		ioTimeDiff := safeDiffUint64(curr.IoTime, prev.IoTime)
		totalTimeDiff := safeDiffUint64(curr.ReadTime+curr.WriteTime, prev.ReadTime+prev.WriteTime)
		weightedDiff := safeDiffUint64(curr.WeightedIO, prev.WeightedIO)

		utilRatio := float64(ioTimeDiff) / (dt * 1000.0)
		if utilRatio < 0 {
			utilRatio = 0
		} else if utilRatio > 1 {
			utilRatio = 1
		}
		queueLength := float64(weightedDiff) / (dt * 1000.0)
		waitMs := 0.0
		serviceMs := 0.0
		if iopsDiff > 0 {
			if ioTimeDiff > 0 {
				serviceMs = ceilMs3(float64(ioTimeDiff) / float64(iopsDiff))
			}
			if ioTimeDiff > 0 && totalTimeDiff > 0 {
				waitDiff := float64(totalTimeDiff) - float64(ioTimeDiff)
				if waitDiff > 0 {
					waitMs = ceilMs3(waitDiff / float64(iopsDiff))
				}
			}
		}

		diskIO[name] = metrics.DiskIO{
			ReadBytes:            curr.ReadBytes,
			WriteBytes:           curr.WriteBytes,
			ReadRateBytesPerSec:  ceilRate(float64(readDiff) / dt),
			WriteRateBytesPerSec: ceilRate(float64(writeDiff) / dt),
			IOPS:                 ceilRate(float64(iopsDiff) / dt),
			ReadIOPS:             ceilRate(float64(readIopsDiff) / dt),
			WriteIOPS:            ceilRate(float64(writeIopsDiff) / dt),
			UtilRatio:            utilRatio,
			QueueLength:          queueLength,
			WaitMs:               waitMs,
			ServiceMs:            serviceMs,
		}
	}

	netIO := make([]metrics.NetIO, 0, len(currNetMap))
	newLastNet := make(map[string]gnet.IOCountersStat)

	for _, name := range sortedNetKeys(currNetMap) {
		if nics != nil {
			if _, ok := nics[name]; !ok {
				continue
			}
		}
		curr := currNetMap[name]
		prev, ok := s.lastNetIO[name]
		if !ok {
			prev = curr
		}
		recvDiff := safeDiffUint64(curr.BytesRecv, prev.BytesRecv)
		sentDiff := safeDiffUint64(curr.BytesSent, prev.BytesSent)
		recvPacketsDiff := safeDiffUint64(curr.PacketsRecv, prev.PacketsRecv)
		sentPacketsDiff := safeDiffUint64(curr.PacketsSent, prev.PacketsSent)

		netIO = append(netIO, metrics.NetIO{
			Name:                  name,
			BytesRecv:             curr.BytesRecv,
			BytesSent:             curr.BytesSent,
			RecvRateBytesPerSec:   ceilRate(float64(recvDiff) / dt),
			SentRateBytesPerSec:   ceilRate(float64(sentDiff) / dt),
			PacketsRecv:           curr.PacketsRecv,
			PacketsSent:           curr.PacketsSent,
			RecvRatePacketsPerSec: ceilRate(float64(recvPacketsDiff) / dt),
			SentRatePacketsPerSec: ceilRate(float64(sentPacketsDiff) / dt),
			ErrIn:                 curr.Errin,
			ErrOut:                curr.Errout,
			DropIn:                curr.Dropin,
			DropOut:               curr.Dropout,
		})
		newLastNet[name] = curr
	}

	s.lastDiskIO = currDisk
	s.lastNetIO = newLastNet
	s.lastIOTimestamp = now

	return diskIO, netIO
}

func mergeNetCounters(in []gnet.IOCountersStat) map[string]gnet.IOCountersStat {
	out := make(map[string]gnet.IOCountersStat, len(in))
	for _, n := range in {
		if n.Name == "" {
			continue
		}
		if prev, ok := out[n.Name]; ok {
			prev.BytesRecv += n.BytesRecv
			prev.BytesSent += n.BytesSent
			prev.PacketsRecv += n.PacketsRecv
			prev.PacketsSent += n.PacketsSent
			prev.Errin += n.Errin
			prev.Errout += n.Errout
			prev.Dropin += n.Dropin
			prev.Dropout += n.Dropout
			out[n.Name] = prev
		} else {
			out[n.Name] = n
		}
	}
	return out
}

func sortedNetKeys(m map[string]gnet.IOCountersStat) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

//go:build linux

package collect

import (
	"encoding/json"
	"os"

	"Ithiltir-node/internal/metrics"
)

type thinpoolCache struct {
	UpdatedAt string              `json:"updated_at"`
	Pools     []thinpoolCachePool `json:"pools"`
	VGs       []thinpoolCacheVG   `json:"vgs,omitempty"`
}

type thinpoolCachePool struct {
	Name      string  `json:"name"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Free      uint64  `json:"free"`
	DataRatio float64 `json:"data_ratio"`
	MetaRatio float64 `json:"meta_ratio"`
}

type thinpoolCacheVG struct {
	Name      string   `json:"name"`
	Total     uint64   `json:"total"`
	Used      uint64   `json:"used"`
	Free      uint64   `json:"free"`
	UsedRatio float64  `json:"used_ratio"`
	Devices   []string `json:"devices,omitempty"`
}

func readLVMCache(cachePath string) []metrics.StorageUsage {
	b, err := os.ReadFile(cachePath)
	if err != nil || len(b) == 0 {
		return nil
	}
	var c thinpoolCache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil
	}
	if len(c.Pools) == 0 && len(c.VGs) == 0 {
		return nil
	}

	out := make([]metrics.StorageUsage, 0, len(c.VGs)+len(c.Pools))
	for _, vg := range c.VGs {
		if vg.Total == 0 {
			continue
		}
		used := vg.Used
		free := vg.Free
		if used == 0 && free == 0 && vg.Total > 0 {
			if vg.Total > used {
				free = vg.Total - used
			}
		}
		usedRatio := vg.UsedRatio
		if usedRatio == 0 {
			usedRatio = ratioFromUsedFree(used, free)
		}
		out = append(out, metrics.StorageUsage{
			Kind:      "lvm_vg",
			Name:      vg.Name,
			Total:     vg.Total,
			Used:      used,
			Free:      free,
			UsedRatio: usedRatio,
			Devices:   vg.Devices,
		})
	}
	for _, p := range c.Pools {
		if p.Total == 0 {
			continue
		}
		used := p.Used
		free := p.Free
		if used == 0 && free == 0 && p.Total > 0 {
			if p.Total > used {
				free = p.Total - used
			}
		}
		out = append(out, metrics.StorageUsage{
			Kind:      "lvm_thinpool",
			Name:      p.Name,
			Total:     p.Total,
			Used:      used,
			Free:      free,
			UsedRatio: ratioFromUsedFree(used, free),
			DataRatio: p.DataRatio,
			MetaRatio: p.MetaRatio,
		})
	}
	return out
}

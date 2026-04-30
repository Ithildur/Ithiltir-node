package push

import (
	"sync"

	"Ithiltir-node/internal/metrics"
)

type Cache struct {
	mu     sync.RWMutex
	report *metrics.NodeReport
}

func NewCache() *Cache {
	return &Cache{}
}

func (c *Cache) Set(report *metrics.NodeReport) {
	c.mu.Lock()
	c.report = report.Clone()
	c.mu.Unlock()
}

func (c *Cache) Get() *metrics.NodeReport {
	c.mu.RLock()
	report := c.report.Clone()
	c.mu.RUnlock()
	return report
}

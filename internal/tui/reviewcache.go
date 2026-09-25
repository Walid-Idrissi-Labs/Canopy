package tui

import (
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The review screen's overlap pane runs git at least three times per agent to answer, and the cost
// pane queries the history database. Both were asked on every frame. What they report changes on
// the scale of seconds, so each answer is kept briefly and the screen draws from that.
type reviewCache struct {
	mu sync.Mutex

	overlapsAt  time.Time
	overlaps    []core.Overlap
	overlapsErr error

	costsAt  time.Time
	costs    CostOutcomeHistory
	costsErr error
}

const (
	overlapFreshness = 2 * time.Second
	costFreshness    = 5 * time.Second
)

func (c *reviewCache) Overlaps(source ReviewSource) ([]core.Overlap, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.overlapsAt) > overlapFreshness {
		c.overlaps, c.overlapsErr = source.Overlaps()
		c.overlapsAt = time.Now()
	}
	return c.overlaps, c.overlapsErr
}

func (c *reviewCache) CostOutcomes(source CostOutcomeSource) (CostOutcomeHistory, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.costsAt) > costFreshness {
		c.costs, c.costsErr = source.CostOutcomes()
		c.costsAt = time.Now()
	}
	return c.costs, c.costsErr
}

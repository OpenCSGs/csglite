package inference

import (
	"context"
	"sync"
)

type cacheUsageContextKey struct{}

// CacheUsage contains prompt-cache metrics aggregated across inference calls
// made while handling one API request.
type CacheUsage struct {
	ReadInputTokens     int64
	CreationInputTokens int64
	EligibleInputTokens int64
}

// CacheUsageCollector safely aggregates cache usage for concurrent inference
// work associated with one request.
type CacheUsageCollector struct {
	mu    sync.Mutex
	usage CacheUsage
}

// WithCacheUsageCollector attaches a request-scoped cache usage collector.
func WithCacheUsageCollector(ctx context.Context) (context.Context, *CacheUsageCollector) {
	collector := &CacheUsageCollector{}
	return context.WithValue(ctx, cacheUsageContextKey{}, collector), collector
}

func cacheUsageCollectorFromContext(ctx context.Context) *CacheUsageCollector {
	collector, _ := ctx.Value(cacheUsageContextKey{}).(*CacheUsageCollector)
	return collector
}

func (c *CacheUsageCollector) add(read, creation, eligible int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.usage.ReadInputTokens += maxInt64(0, read)
	c.usage.CreationInputTokens += maxInt64(0, creation)
	c.usage.EligibleInputTokens += maxInt64(0, eligible)
}

// Snapshot returns the cache usage recorded so far.
func (c *CacheUsageCollector) Snapshot() CacheUsage {
	if c == nil {
		return CacheUsage{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

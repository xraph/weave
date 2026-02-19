package middleware

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/xraph/weave/pipeline"
)

// Cache provides a simple in-memory cache middleware for pipeline steps.
// It caches step results keyed by the step name and a hash of the
// StepContext contents.
type Cache struct {
	mu    sync.RWMutex
	store map[string]map[string]any
}

// NewCache creates a new cache middleware.
func NewCache() *Cache {
	return &Cache{store: make(map[string]map[string]any)}
}

// Middleware returns a Middleware that caches step outputs.
// Only steps whose names match one of the given names are cached.
// If no names are provided, all steps are cached.
func (c *Cache) Middleware(names ...string) Middleware {
	nameSet := make(map[string]struct{}, len(names))
	for _, n := range names {
		nameSet[n] = struct{}{}
	}

	return func(next StepHandler) StepHandler {
		return func(ctx context.Context, sc *pipeline.StepContext) error {
			stepName := sc.StepName()

			// Skip caching if not in the name set.
			if len(nameSet) > 0 {
				if _, ok := nameSet[stepName]; !ok {
					return next(ctx, sc)
				}
			}

			key := c.cacheKey(sc)

			// Check cache.
			c.mu.RLock()
			if cached, ok := c.store[stepName]; ok {
				if data, hit := cached[key]; hit {
					c.mu.RUnlock()
					sc.SetCacheHit(stepName, data)
					return nil
				}
			}
			c.mu.RUnlock()

			// Execute step.
			if err := next(ctx, sc); err != nil {
				return err
			}

			// Store result.
			c.mu.Lock()
			if _, ok := c.store[stepName]; !ok {
				c.store[stepName] = make(map[string]any)
			}
			c.store[stepName][key] = sc.Snapshot()
			c.mu.Unlock()

			return nil
		}
	}
}

// Clear empties the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	c.store = make(map[string]map[string]any)
	c.mu.Unlock()
}

func (c *Cache) cacheKey(sc *pipeline.StepContext) string {
	h := sha256.New()
	for _, k := range sc.Keys() {
		v, _ := sc.Get(k)
		fmt.Fprintf(h, "%s=%v;", k, v)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// Package cache implements a Redis-backed semantic response cache for LLM calls.
// When two requests produce the same hash (model + messages), the second one
// returns the cached response without touching the LLM API — saving money
// and reducing latency.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Karcsihack/lattice-cost/types"
	"github.com/redis/go-redis/v9"
)

const keyPrefix = "lc:cache:"

// Cache is a Redis-backed LLM response cache.
type Cache struct {
	rdb  *redis.Client
	ttl  time.Duration
	hits uint64 // atomic
	miss uint64 // atomic
}

// New returns a Cache using the given Redis client and TTL.
func New(rdb *redis.Client, ttl time.Duration) *Cache {
	return &Cache{rdb: rdb, ttl: ttl}
}

// Get looks up a cached response for req.
// Returns (response, true, nil) on cache hit, (nil, false, nil) on miss.
func (c *Cache) Get(ctx context.Context, req *types.ChatRequest) (*types.ChatResponse, bool, error) {
	key := c.hashKey(req)

	data, err := c.rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		atomic.AddUint64(&c.miss, 1)
		return nil, false, nil
	}
	if err != nil {
		atomic.AddUint64(&c.miss, 1)
		return nil, false, fmt.Errorf("cache get: %w", err)
	}

	var resp types.ChatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		atomic.AddUint64(&c.miss, 1)
		return nil, false, fmt.Errorf("cache unmarshal: %w", err)
	}

	atomic.AddUint64(&c.hits, 1)
	return &resp, true, nil
}

// Set stores a response in the cache.
func (c *Cache) Set(ctx context.Context, req *types.ChatRequest, resp *types.ChatResponse) error {
	key := c.hashKey(req)

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("cache marshal: %w", err)
	}

	if err := c.rdb.Set(ctx, key, data, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}

	return nil
}

// Stats returns (hits, misses, hit-rate-percent).
func (c *Cache) Stats() (hits, misses uint64, hitRate float64) {
	h := atomic.LoadUint64(&c.hits)
	m := atomic.LoadUint64(&c.miss)
	total := h + m
	if total > 0 {
		hitRate = float64(h) / float64(total) * 100
	}
	return h, m, hitRate
}

// hashKey generates a deterministic Redis key for the given request.
// The key is based on the model and the full messages array, so different
// models or conversation histories always produce different cache entries.
func (c *Cache) hashKey(req *types.ChatRequest) string {
	type cacheKey struct {
		Model    string          `json:"m"`
		Messages []types.Message `json:"msg"`
	}

	payload, _ := json.Marshal(cacheKey{
		Model:    req.Model,
		Messages: req.Messages,
	})

	h := sha256.Sum256(payload)
	return keyPrefix + fmt.Sprintf("%x", h)
}

// Flush removes all cache entries (useful for testing / cache invalidation).
func (c *Cache) Flush(ctx context.Context) (int64, error) {
	var cursor uint64
	var total int64

	for {
		keys, newCursor, err := c.rdb.Scan(ctx, cursor, keyPrefix+"*", 100).Result()
		if err != nil {
			return total, err
		}
		if len(keys) > 0 {
			n, err := c.rdb.Del(ctx, keys...).Result()
			if err != nil {
				return total, err
			}
			total += n
		}
		cursor = newCursor
		if cursor == 0 {
			break
		}
	}

	return total, nil
}

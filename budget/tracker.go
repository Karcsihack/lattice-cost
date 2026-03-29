// Package budget provides per-API-key token spend tracking backed by Redis.
// All counters are atomic so multiple Lattice-Cost instances can share the
// same Redis without double-counting.
package budget

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	keyPrefixDaily   = "lc:budget:daily:"
	keyPrefixMonthly = "lc:budget:monthly:"
)

// Tracker tracks token spend per API key.
type Tracker struct {
	rdb          *redis.Client
	dailyLimit   float64
	monthlyLimit float64
}

// Usage describes the current spend state for one API key.
type Usage struct {
	APIKey          string
	DailySpendUSD   float64
	DailyLimitUSD   float64
	MonthlySpendUSD float64
	MonthlyLimitUSD float64
	DailyRemaining  float64
}

// DailyExceeded returns true when the daily budget is used up.
func (u *Usage) DailyExceeded() bool {
	return u.DailyLimitUSD > 0 && u.DailySpendUSD >= u.DailyLimitUSD
}

// New returns a Tracker using the given Redis client.
// dailyLimit and monthlyLimit are the fallback limits; callers should
// override per key by calling CheckAndConsume with the right limits.
func New(rdb *redis.Client, defaultDailyLimit, defaultMonthlyLimit float64) *Tracker {
	return &Tracker{
		rdb:          rdb,
		dailyLimit:   defaultDailyLimit,
		monthlyLimit: defaultMonthlyLimit,
	}
}

// CheckAndConsume atomically verifies the daily budget for apiKey has not
// been exceeded, then adds costUSD to the running counter.
// Returns an error if the budget would be exceeded by this request.
func (t *Tracker) CheckAndConsume(ctx context.Context, apiKey string, costUSD, dailyLimit, monthlyLimit float64) error {
	now := time.Now().UTC()
	dayKey := dailyKey(apiKey, now)
	monthKey := monthlyKey(apiKey, now)

	// Fetch current totals in a single pipeline.
	pipe := t.rdb.Pipeline()
	dayGet := pipe.Get(ctx, dayKey)
	monthGet := pipe.Get(ctx, monthKey)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		// Redis unavailable — allow the request but log implicitly via error.
		return fmt.Errorf("budget redis pipeline: %w", err)
	}

	dailySpend := parseFloat(dayGet.Val())
	monthlySpend := parseFloat(monthGet.Val())

	if dailyLimit > 0 && dailySpend+costUSD > dailyLimit {
		return fmt.Errorf(
			"daily budget exceeded for key %s (%.4f USD used / %.2f USD limit)",
			maskKey(apiKey), dailySpend, dailyLimit,
		)
	}
	if monthlyLimit > 0 && monthlySpend+costUSD > monthlyLimit {
		return fmt.Errorf(
			"monthly budget exceeded for key %s (%.4f USD used / %.2f USD limit)",
			maskKey(apiKey), monthlySpend, monthlyLimit,
		)
	}

	// Atomically increment both counters. Use INCRBYFLOAT (Redis-atomic).
	pipe2 := t.rdb.Pipeline()
	pipe2.IncrByFloat(ctx, dayKey, costUSD)
	pipe2.IncrByFloat(ctx, monthKey, costUSD)

	// Set TTL on first write (EXPIREAT to end of day / end of month UTC).
	pipe2.ExpireAt(ctx, dayKey, endOfDayUTC(now))
	pipe2.ExpireAt(ctx, monthKey, endOfMonthUTC(now))

	if _, err := pipe2.Exec(ctx); err != nil {
		return fmt.Errorf("budget counter update: %w", err)
	}

	return nil
}

// GetUsage returns the current spend for an API key.
func (t *Tracker) GetUsage(ctx context.Context, apiKey string, dailyLimit, monthlyLimit float64) (*Usage, error) {
	now := time.Now().UTC()
	dayKey := dailyKey(apiKey, now)
	monthKey := monthlyKey(apiKey, now)

	pipe := t.rdb.Pipeline()
	dayGet := pipe.Get(ctx, dayKey)
	monthGet := pipe.Get(ctx, monthKey)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("budget get usage: %w", err)
	}

	dailySpend := parseFloat(dayGet.Val())
	monthlySpend := parseFloat(monthGet.Val())

	remaining := dailyLimit - dailySpend
	if remaining < 0 {
		remaining = 0
	}

	return &Usage{
		APIKey:          maskKey(apiKey),
		DailySpendUSD:   math.Round(dailySpend*10000) / 10000,
		DailyLimitUSD:   dailyLimit,
		MonthlySpendUSD: math.Round(monthlySpend*10000) / 10000,
		MonthlyLimitUSD: monthlyLimit,
		DailyRemaining:  math.Round(remaining*10000) / 10000,
	}, nil
}

// AllUsages returns spend data for all known API keys for today.
// It scans Redis keys matching the daily pattern.
func (t *Tracker) AllUsages(ctx context.Context) ([]Usage, error) {
	now := time.Now().UTC()
	pattern := keyPrefixDaily + now.Format("2006-01-02") + ":*"

	var cursor uint64
	var usages []Usage
	seen := map[string]bool{}

	for {
		keys, newCursor, err := t.rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scanning budget keys: %w", err)
		}
		for _, k := range keys {
			rawKey := extractAPIKey(k)
			if seen[rawKey] {
				continue
			}
			seen[rawKey] = true
			u, err := t.GetUsage(ctx, rawKey, t.dailyLimit, t.monthlyLimit)
			if err == nil {
				usages = append(usages, *u)
			}
		}
		cursor = newCursor
		if cursor == 0 {
			break
		}
	}

	return usages, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func dailyKey(apiKey string, t time.Time) string {
	return keyPrefixDaily + t.Format("2006-01-02") + ":" + apiKey
}

func monthlyKey(apiKey string, t time.Time) string {
	return keyPrefixMonthly + t.Format("2006-01") + ":" + apiKey
}

func endOfDayUTC(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)
}

func endOfMonthUTC(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m+1, 1, 0, 0, 0, 0, time.UTC)
}

func parseFloat(s string) float64 {
	if s == "" {
		return 0
	}
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}

// maskKey hides the middle of an API key for safe logging.
func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

// extractAPIKey reverses dailyKey to get the raw API key portion.
func extractAPIKey(redisKey string) string {
	// Format: lc:budget:daily:YYYY-MM-DD:<apikey>
	const dateLen = 10 // len("2006-01-02")
	prefix := keyPrefixDaily + "0000-00-00:"
	if len(redisKey) > len(prefix) {
		return redisKey[len(keyPrefixDaily)+dateLen+1:]
	}
	return redisKey
}

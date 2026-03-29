// Package config loads Lattice-Cost configuration from environment variables.
// All settings have sensible defaults so the tool works out of the box.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// BudgetRule defines a spending limit for an API key (or the default).
type BudgetRule struct {
	// APIKey is the key this rule applies to. Empty means "default for all".
	APIKey string
	// DailyLimitUSD caps spending per calendar day (UTC).
	DailyLimitUSD float64
	// MonthlyLimitUSD caps spending per calendar month. 0 = disabled.
	MonthlyLimitUSD float64
}

// ModelConfig configures the cheap/expensive model pair for the smart router.
type ModelConfig struct {
	// CheapModel is used for SIMPLE and MODERATE prompts.
	CheapModel string
	// PowerfulModel is used for COMPLEX prompts.
	PowerfulModel string
	// ForceModel overrides routing and always uses this model. Empty = disabled.
	ForceModel string
	// ComplexTokenThreshold: prompts with more estimated tokens are COMPLEX.
	ComplexTokenThreshold int
}

// Config is the full Lattice-Cost runtime configuration.
type Config struct {
	// ListenAddr is the address the middleware HTTP server listens on.
	ListenAddr string

	// UpstreamURL is the real LLM API base URL (e.g. https://api.openai.com).
	UpstreamURL string

	// LatticeProxyURL is the lattice-proxy for PII scrubbing (Lattice-Shield layer).
	LatticeProxyURL string

	// Redis connection details.
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// CacheTTL controls how long a cached LLM response is reused.
	CacheTTL time.Duration

	// CacheEnabled turns the Redis response cache on or off.
	CacheEnabled bool

	// SmartRoutingEnabled activates the cheap/expensive model router.
	SmartRoutingEnabled bool

	// BudgetEnabled activates per-API-key spend tracking and limits.
	BudgetEnabled bool

	// DefaultBudget applies to any API key without a specific rule.
	DefaultBudget BudgetRule

	// Budgets overrides DefaultBudget for specific API keys.
	// Key: API key prefix (first 10 chars). Value: rule.
	Budgets map[string]BudgetRule

	// Models configures the smart router.
	Models ModelConfig

	// MetricsListenAddr exposes a /metrics endpoint (empty = disabled).
	MetricsListenAddr string
}

// Load reads configuration primarily from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:          getenv("LATTICE_COST_ADDR", ":8081"),
		UpstreamURL:         getenv("UPSTREAM_URL", "https://api.openai.com"),
		LatticeProxyURL:     getenv("LATTICE_PROXY_URL", "http://localhost:8080"),
		RedisAddr:           getenv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:       getenv("REDIS_PASSWORD", ""),
		RedisDB:             getenvInt("REDIS_DB", 0),
		CacheTTL:            getenvDuration("CACHE_TTL", 1*time.Hour),
		CacheEnabled:        getenvBool("CACHE_ENABLED", true),
		SmartRoutingEnabled: getenvBool("SMART_ROUTING_ENABLED", true),
		BudgetEnabled:       getenvBool("BUDGET_ENABLED", true),
		MetricsListenAddr:   getenv("METRICS_ADDR", ":9091"),
		DefaultBudget: BudgetRule{
			DailyLimitUSD:   getenvFloat("DEFAULT_DAILY_LIMIT_USD", 50.0),
			MonthlyLimitUSD: getenvFloat("DEFAULT_MONTHLY_LIMIT_USD", 1000.0),
		},
		Models: ModelConfig{
			CheapModel:            getenv("CHEAP_MODEL", "gpt-4o-mini"),
			PowerfulModel:         getenv("POWERFUL_MODEL", "gpt-4o"),
			ForceModel:            getenv("FORCE_MODEL", ""),
			ComplexTokenThreshold: getenvInt("COMPLEX_TOKEN_THRESHOLD", 500),
		},
		Budgets: make(map[string]BudgetRule),
	}

	// Parse per-key budgets from LATTICE_BUDGET_<KEYPREFIX>=<daily>:<monthly>
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "LATTICE_BUDGET_") {
			continue
		}
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		keyPrefix := strings.TrimPrefix(parts[0], "LATTICE_BUDGET_")
		limits := strings.SplitN(parts[1], ":", 2)
		if len(limits) < 1 {
			continue
		}
		daily, err := strconv.ParseFloat(limits[0], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid LATTICE_BUDGET_%s: %w", keyPrefix, err)
		}
		var monthly float64
		if len(limits) == 2 {
			monthly, _ = strconv.ParseFloat(limits[1], 64)
		}
		cfg.Budgets[keyPrefix] = BudgetRule{
			APIKey:          keyPrefix,
			DailyLimitUSD:   daily,
			MonthlyLimitUSD: monthly,
		}
	}

	return cfg, nil
}

// BudgetFor returns the budget rule that applies to the given API key.
func (c *Config) BudgetFor(apiKey string) BudgetRule {
	// Try prefix match (first 10 chars of the key).
	prefix := apiKey
	if len(prefix) > 10 {
		prefix = prefix[:10]
	}
	if rule, ok := c.Budgets[prefix]; ok {
		return rule
	}
	return c.DefaultBudget
}

// ── helpers ──────────────────────────────────────────────────────────────────

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getenvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(v) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

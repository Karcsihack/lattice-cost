// Package metrics collects real-time cost and savings data for every
// LLM request that passes through the Lattice-Cost middleware.
package metrics

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// RequestRecord holds the data from a single LLM request.
type RequestRecord struct {
	Timestamp       time.Time
	APIKey          string
	ModelRequested  string
	ModelUsed       string
	InputTokens     int
	OutputTokens    int
	CostUSD         float64
	CacheHit        bool
	SavingsUSD      float64 // non-zero only on cache hit
	ComplexityLevel string
	DurationMs      int64
}

// Report is a snapshot of aggregated metrics.
type Report struct {
	GeneratedAt         time.Time
	TotalRequests       int
	CacheHits           int
	CacheMisses         int
	HitRatePct          float64
	TotalCostUSD        float64
	TotalSavingsUSD     float64
	AvgCostPerReq       float64
	AvgDurationMs       float64
	PerKey              []KeyReport
	PerModel            []ModelReport
	ComplexityBreakdown map[string]int
	RoutingDowngrades   int // times a cheap model replaced an expensive one
}

// KeyReport aggregates metrics for one API key.
type KeyReport struct {
	APIKey      string
	Requests    int
	CostUSD     float64
	SavingsUSD  float64
	CacheHitPct float64
}

// ModelReport aggregates metrics for one model.
type ModelReport struct {
	Model    string
	Requests int
	CostUSD  float64
}

// Collector is a thread-safe in-process metrics aggregator.
type Collector struct {
	mu      sync.RWMutex
	records []RequestRecord

	// fast-path counters (avoid locking for read-heavy paths)
	totalReqs    int
	cacheHits    int
	totalCostUSD float64
	savingsUSD   float64
}

// NewCollector returns a ready Collector.
func NewCollector() *Collector {
	return &Collector{
		records: make([]RequestRecord, 0, 1000),
	}
}

// Record adds a completed request to the collector.
func (c *Collector) Record(r RequestRecord) {
	c.mu.Lock()
	c.records = append(c.records, r)
	c.totalReqs++
	if r.CacheHit {
		c.cacheHits++
	}
	c.totalCostUSD += r.CostUSD
	c.savingsUSD += r.SavingsUSD
	c.mu.Unlock()
}

// QuickStats returns (totalRequests, cacheHits, totalCostUSD, totalSavingsUSD)
// without scanning all records — safe to call on every request.
func (c *Collector) QuickStats() (reqs, hits int, cost, savings float64) {
	c.mu.RLock()
	reqs = c.totalReqs
	hits = c.cacheHits
	cost = c.totalCostUSD
	savings = c.savingsUSD
	c.mu.RUnlock()
	return
}

// GenerateReport builds a full aggregated report from all recorded requests.
func (c *Collector) GenerateReport() *Report {
	c.mu.RLock()
	records := make([]RequestRecord, len(c.records))
	copy(records, c.records)
	c.mu.RUnlock()

	report := &Report{
		GeneratedAt:         time.Now().UTC(),
		TotalRequests:       len(records),
		ComplexityBreakdown: make(map[string]int),
	}

	if len(records) == 0 {
		return report
	}

	keyMap := make(map[string]*KeyReport)
	modelMap := make(map[string]*ModelReport)
	var totalDuration int64

	for _, r := range records {
		if r.CacheHit {
			report.CacheHits++
		} else {
			report.CacheMisses++
		}
		report.TotalCostUSD += r.CostUSD
		report.TotalSavingsUSD += r.SavingsUSD
		totalDuration += r.DurationMs
		report.ComplexityBreakdown[r.ComplexityLevel]++

		if r.ModelRequested != r.ModelUsed && r.ModelUsed != "" {
			report.RoutingDowngrades++
		}

		// Per-key aggregation
		masked := maskKey(r.APIKey)
		kr, ok := keyMap[masked]
		if !ok {
			kr = &KeyReport{APIKey: masked}
			keyMap[masked] = kr
		}
		kr.Requests++
		kr.CostUSD += r.CostUSD
		kr.SavingsUSD += r.SavingsUSD
		if r.CacheHit {
			kr.CacheHitPct++ // count hits; convert to % below
		}

		// Per-model aggregation (use model actually called)
		model := r.ModelUsed
		if model == "" {
			model = r.ModelRequested
		}
		mr, ok := modelMap[model]
		if !ok {
			mr = &ModelReport{Model: model}
			modelMap[model] = mr
		}
		mr.Requests++
		mr.CostUSD += r.CostUSD
	}

	if report.TotalRequests > 0 {
		report.HitRatePct = float64(report.CacheHits) / float64(report.TotalRequests) * 100
		report.AvgCostPerReq = report.TotalCostUSD / float64(report.TotalRequests)
		report.AvgDurationMs = float64(totalDuration) / float64(report.TotalRequests)
	}

	// Finalise per-key hit rate
	for _, kr := range keyMap {
		if kr.Requests > 0 {
			kr.CacheHitPct = kr.CacheHitPct / float64(kr.Requests) * 100
		}
		kr.CostUSD = round4(kr.CostUSD)
		kr.SavingsUSD = round4(kr.SavingsUSD)
		report.PerKey = append(report.PerKey, *kr)
	}
	sort.Slice(report.PerKey, func(i, j int) bool {
		return report.PerKey[i].CostUSD > report.PerKey[j].CostUSD
	})

	for _, mr := range modelMap {
		mr.CostUSD = round4(mr.CostUSD)
		report.PerModel = append(report.PerModel, *mr)
	}
	sort.Slice(report.PerModel, func(i, j int) bool {
		return report.PerModel[i].CostUSD > report.PerModel[j].CostUSD
	})

	report.TotalCostUSD = round4(report.TotalCostUSD)
	report.TotalSavingsUSD = round4(report.TotalSavingsUSD)
	report.AvgCostPerReq = round6(report.AvgCostPerReq)

	return report
}

// FormatReport renders a human-readable report string.
func FormatReport(r *Report) string {
	var b strings.Builder

	line := strings.Repeat("─", 58)
	fmt.Fprintf(&b, "\n  Lattice-Cost — FinOps Report  (%s)\n", r.GeneratedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintln(&b, "  "+line)
	fmt.Fprintf(&b, "  Total Requests  : %d\n", r.TotalRequests)
	fmt.Fprintf(&b, "  Cache Hits      : %d  (%.1f%% hit rate)\n", r.CacheHits, r.HitRatePct)
	fmt.Fprintf(&b, "  Cache Misses    : %d\n", r.CacheMisses)
	fmt.Fprintf(&b, "  Total Cost      : $%.4f USD\n", r.TotalCostUSD)
	fmt.Fprintf(&b, "  Total Savings   : $%.4f USD  (cache hits)\n", r.TotalSavingsUSD)
	fmt.Fprintf(&b, "  Avg Cost/Req    : $%.6f USD\n", r.AvgCostPerReq)
	fmt.Fprintf(&b, "  Avg Latency     : %.0f ms\n", r.AvgDurationMs)
	fmt.Fprintf(&b, "  Routing Saves   : %d requests downgraded to cheaper model\n", r.RoutingDowngrades)

	if len(r.ComplexityBreakdown) > 0 {
		fmt.Fprintln(&b, "\n  Complexity Breakdown:")
		for level, count := range r.ComplexityBreakdown {
			fmt.Fprintf(&b, "    %-12s %d\n", level, count)
		}
	}

	if len(r.PerModel) > 0 {
		fmt.Fprintln(&b, "\n  Cost by Model:")
		for _, m := range r.PerModel {
			fmt.Fprintf(&b, "    %-40s %d req   $%.4f\n", m.Model, m.Requests, m.CostUSD)
		}
	}

	if len(r.PerKey) > 0 {
		fmt.Fprintln(&b, "\n  Cost by API Key:")
		for _, k := range r.PerKey {
			fmt.Fprintf(&b, "    %-20s %d req   $%-8.4f  saved $%.4f  (%.0f%% cache)\n",
				k.APIKey, k.Requests, k.CostUSD, k.SavingsUSD, k.CacheHitPct)
		}
	}

	fmt.Fprintln(&b, "  "+line+"\n")
	return b.String()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func maskKey(k string) string {
	if len(k) <= 8 {
		return "****"
	}
	return k[:4] + "****" + k[len(k)-4:]
}

func round4(f float64) float64 { return math.Round(f*10000) / 10000 }
func round6(f float64) float64 { return math.Round(f*1000000) / 1000000 }

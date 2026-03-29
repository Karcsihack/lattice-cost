// Package middleware is the core HTTP handler for Lattice-Cost.
// It wires together the budget tracker, smart router, Redis cache,
// and metrics collector into a single reverse-proxy interceptor that
// plugs in front of any OpenAI-compatible LLM endpoint.
//
// Request lifecycle:
//  1. Extract API key from Authorization header.
//  2. Parse request body as ChatRequest.
//  3. Check daily budget (reject with 429 if exceeded).
//  4. Look up response in Redis cache → return immediately on hit.
//  5. Route to cheapest viable model (Smart Router).
//  6. Forward sanitised request to upstream LLM API.
//  7. Store response in cache.
//  8. Deduct actual cost from budget.
//  9. Record metrics and return response with X-Lattice-* headers.
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Karcsihack/lattice-cost/budget"
	"github.com/Karcsihack/lattice-cost/cache"
	"github.com/Karcsihack/lattice-cost/config"
	"github.com/Karcsihack/lattice-cost/metrics"
	"github.com/Karcsihack/lattice-cost/router"
	"github.com/Karcsihack/lattice-cost/types"
)

const (
	maxBodyBytes = 4 << 20 // 4 MB hard cap on incoming request body
)

// Middleware is the Lattice-Cost HTTP handler.
type Middleware struct {
	cfg        *config.Config
	budget     *budget.Tracker
	cache      *cache.Cache
	router     *router.Router
	collector  *metrics.Collector
	httpClient *http.Client
}

// New creates a Middleware from the given components.
// Pass nil for budget/cache to disable those layers.
func New(
	cfg *config.Config,
	bud *budget.Tracker,
	cch *cache.Cache,
	rtr *router.Router,
	col *metrics.Collector,
) *Middleware {
	return &Middleware{
		cfg:       cfg,
		budget:    bud,
		cache:     cch,
		router:    rtr,
		collector: col,
		httpClient: &http.Client{
			Timeout: 90 * time.Second,
		},
	}
}

// ServeHTTP implements http.Handler.
func (m *Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Health check shortcut.
	if r.URL.Path == "/health" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok","service":"lattice-cost"}`)
		return
	}

	// Only intercept chat completions; pass everything else straight through.
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		m.proxyRaw(w, r)
		return
	}

	apiKey := extractAPIKey(r)

	// Read and parse body.
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	r.Body.Close()

	var req types.ChatRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request body")
		return
	}

	// ── Smart Router ──────────────────────────────────────────────────────────
	originalModel := req.Model
	var complexity types.ComplexityLevel
	var estimatedTokens int

	if m.cfg.SmartRoutingEnabled {
		req.Model, complexity, estimatedTokens = m.router.Route(&req)
	} else {
		estimatedTokens = estimateTokens(&req)
	}

	// ── Budget pre-check (estimate) ───────────────────────────────────────────
	var budgetRule config.BudgetRule
	if m.cfg.BudgetEnabled && m.budget != nil {
		budgetRule = m.cfg.BudgetFor(apiKey)
		// Estimate cost before the call using average output length heuristic.
		estCost := router.EstimateCost(req.Model, estimatedTokens, estimatedTokens/3)
		ctx := r.Context()
		if err := m.budget.CheckAndConsume(ctx, apiKey, estCost,
			budgetRule.DailyLimitUSD, budgetRule.MonthlyLimitUSD); err != nil {
			writeError(w, http.StatusTooManyRequests, fmt.Sprintf("budget limit: %v", err))
			return
		}
	}

	// ── Cache lookup ──────────────────────────────────────────────────────────
	if m.cfg.CacheEnabled && m.cache != nil {
		if cached, hit, _ := m.cache.Get(r.Context(), &req); hit {
			// Compute savings as what a fresh call would have cost.
			savings := router.SavingsIfCached(req.Model,
				cached.Usage.PromptTokens, cached.Usage.CompletionTokens)

			m.collector.Record(metrics.RequestRecord{
				Timestamp:       start,
				APIKey:          apiKey,
				ModelRequested:  originalModel,
				ModelUsed:       req.Model,
				InputTokens:     cached.Usage.PromptTokens,
				OutputTokens:    cached.Usage.CompletionTokens,
				CostUSD:         0, // no cost — cache hit
				CacheHit:        true,
				SavingsUSD:      savings,
				ComplexityLevel: complexity.String(),
				DurationMs:      time.Since(start).Milliseconds(),
			})

			cached.Model = req.Model // reflect actual model used
			setLatticeHeaders(w, originalModel, req.Model, complexity, 0, savings, true)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(cached)
			return
		}
	}

	// ── Forward to upstream LLM API ───────────────────────────────────────────
	rewrittenBody, _ := json.Marshal(req)
	resp, respBody, err := m.forwardRequest(r, rewrittenBody, apiKey)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("upstream error: %v", err))
		return
	}
	if resp.StatusCode != http.StatusOK {
		// Relay the upstream error as-is.
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	// Parse usage from response.
	var chatResp types.ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		writeError(w, http.StatusBadGateway, "failed to parse upstream response")
		return
	}

	actualCost := router.EstimateCost(req.Model,
		chatResp.Usage.PromptTokens, chatResp.Usage.CompletionTokens)

	// ── Cache store ───────────────────────────────────────────────────────────
	if m.cfg.CacheEnabled && m.cache != nil {
		_ = m.cache.Set(r.Context(), &req, &chatResp) // best-effort
	}

	// ── Record metrics ────────────────────────────────────────────────────────
	m.collector.Record(metrics.RequestRecord{
		Timestamp:       start,
		APIKey:          apiKey,
		ModelRequested:  originalModel,
		ModelUsed:       req.Model,
		InputTokens:     chatResp.Usage.PromptTokens,
		OutputTokens:    chatResp.Usage.CompletionTokens,
		CostUSD:         actualCost,
		CacheHit:        false,
		SavingsUSD:      0,
		ComplexityLevel: complexity.String(),
		DurationMs:      time.Since(start).Milliseconds(),
	})

	// ── Return response ───────────────────────────────────────────────────────
	setLatticeHeaders(w, originalModel, req.Model, complexity, actualCost, 0, false)
	for k, vs := range resp.Header {
		if strings.ToLower(k) == "content-length" {
			continue // body may differ after re-encoding
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBody)
}

// forwardRequest sends rewrittenBody to the upstream LLM API, preserving the
// original Authorization header and path.
func (m *Middleware) forwardRequest(r *http.Request, body []byte, apiKey string) (*http.Response, []byte, error) {
	upstreamURL := m.cfg.UpstreamURL + r.URL.Path
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	ctx, cancel := context.WithTimeout(r.Context(), 85*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-Lattice-Cost-Version", "1.0.0")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("reading upstream response: %w", err)
	}

	return resp, respBody, nil
}

// proxyRaw forwards non-intercepted requests to the upstream unchanged.
func (m *Middleware) proxyRaw(w http.ResponseWriter, r *http.Request) {
	upstreamURL := m.cfg.UpstreamURL + r.URL.RequestURI()
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusBadGateway, "proxy error")
		return
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream unreachable")
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func extractAPIKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return auth
}

func setLatticeHeaders(w http.ResponseWriter, originalModel, usedModel string,
	complexity types.ComplexityLevel, costUSD, savingsUSD float64, cacheHit bool) {

	w.Header().Set("X-Lattice-Model-Requested", originalModel)
	w.Header().Set("X-Lattice-Model-Used", usedModel)
	w.Header().Set("X-Lattice-Complexity", complexity.String())
	w.Header().Set("X-Lattice-Cost-USD", fmt.Sprintf("%.6f", costUSD))
	w.Header().Set("X-Lattice-Savings-USD", fmt.Sprintf("%.6f", savingsUSD))
	if cacheHit {
		w.Header().Set("X-Lattice-Cache", "HIT")
	} else {
		w.Header().Set("X-Lattice-Cache", "MISS")
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	var errResp types.ErrorResponse
	errResp.Error.Message = msg
	errResp.Error.Type = "lattice_cost_error"
	errResp.Error.Code = fmt.Sprintf("%d", code)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(errResp)
}

func estimateTokens(req *types.ChatRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(m.Content) / 4
	}
	return total
}

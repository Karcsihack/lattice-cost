// Package router implements the Smart Router: it classifies prompt complexity
// and selects the most cost-effective model for each request.
//
// Rule of thumb:
//
//	SIMPLE   (<150 tokens, simple keywords) → cheap model  (e.g. gpt-4o-mini)
//	MODERATE (150-500 tokens)               → cheap model
//	COMPLEX  (>500 tokens or hard keywords) → powerful model (e.g. gpt-4o)
package router

import (
	"strings"

	"github.com/Karcsihack/lattice-cost/types"
)

// ModelPrice stores the cost per 1 million tokens for a model.
type ModelPrice struct {
	InputPer1M  float64 // cost per 1M input tokens in USD
	OutputPer1M float64 // cost per 1M output tokens in USD
}

// Pricing is the master model → pricing table.
// Prices are approximate as of Q1 2026; update as providers change them.
var Pricing = map[string]ModelPrice{
	"gpt-4o":                     {InputPer1M: 2.50, OutputPer1M: 10.00},
	"gpt-4o-mini":                {InputPer1M: 0.15, OutputPer1M: 0.60},
	"gpt-4-turbo":                {InputPer1M: 10.00, OutputPer1M: 30.00},
	"gpt-4":                      {InputPer1M: 30.00, OutputPer1M: 60.00},
	"gpt-3.5-turbo":              {InputPer1M: 0.50, OutputPer1M: 1.50},
	"claude-3-5-sonnet-20241022": {InputPer1M: 3.00, OutputPer1M: 15.00},
	"claude-3-opus-20240229":     {InputPer1M: 15.00, OutputPer1M: 75.00},
	"claude-3-haiku-20240307":    {InputPer1M: 0.25, OutputPer1M: 1.25},
	"claude-3-5-haiku-20241022":  {InputPer1M: 0.80, OutputPer1M: 4.00},
	"gemini-1.5-pro":             {InputPer1M: 3.50, OutputPer1M: 10.50},
	"gemini-1.5-flash":           {InputPer1M: 0.075, OutputPer1M: 0.30},
	"mistral-large":              {InputPer1M: 2.00, OutputPer1M: 6.00},
	"mistral-8x7b":               {InputPer1M: 0.70, OutputPer1M: 0.70},
}

// complexKeywords force COMPLEX classification regardless of length.
var complexKeywords = []string{
	"analyze", "analyse", "architect", "design", "implement", "refactor",
	"optimize", "debug", "evaluate", "compare", "differentiate", "summarize",
	"summarise", "elaborate", "explain in detail", "write a complete",
	"generate a", "create a full", "step by step", "algorithm", "pseudocode",
	"review the following code", "review the code", "audit", "security analysis",
	"business logic", "scalability", "trade-offs", "tradeoffs",
}

// simpleKeywords push toward SIMPLE classification.
var simpleKeywords = []string{
	"what is", "who is", "when was", "where is", "define ", "list ",
	"how many", "what are", "give me a list", "spell", "translate",
	"convert ", "calculate ", "what does", "in one sentence",
}

// Router selects the cheapest viable model for each request.
type Router struct {
	CheapModel            string
	PowerfulModel         string
	ForceModel            string
	ComplexTokenThreshold int
}

// New returns a Router with the given model configuration.
func New(cheap, powerful, force string, complexThreshold int) *Router {
	return &Router{
		CheapModel:            cheap,
		PowerfulModel:         powerful,
		ForceModel:            force,
		ComplexTokenThreshold: complexThreshold,
	}
}

// Route returns the model that should handle req, along with the
// complexity classification and estimated input token count.
func (r *Router) Route(req *types.ChatRequest) (chosenModel string, complexity types.ComplexityLevel, estimatedTokens int) {
	// If the operator has locked a specific model, honour it.
	if r.ForceModel != "" {
		return r.ForceModel, types.ComplexityModerate, r.estimateTokens(req)
	}

	tokens := r.estimateTokens(req)
	complexity = r.classify(req, tokens)

	switch complexity {
	case types.ComplexityComplex:
		chosenModel = r.PowerfulModel
	default:
		chosenModel = r.CheapModel
	}

	// Preserve the original model if it is not in our routing table
	// (i.e. a niche / fine-tuned model the user explicitly asked for).
	if _, known := Pricing[req.Model]; !known && req.Model != "" {
		chosenModel = req.Model
		complexity = types.ComplexityModerate
	}

	return chosenModel, complexity, tokens
}

// EstimateCost computes the approximate USD cost for a request.
// inputTokens and outputTokens come from the LLM's usage stats (or estimates).
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	price, ok := Pricing[model]
	if !ok {
		// Unknown model — use a mid-range estimate.
		price = ModelPrice{InputPer1M: 2.50, OutputPer1M: 10.00}
	}
	inputCost := (float64(inputTokens) / 1_000_000) * price.InputPer1M
	outputCost := (float64(outputTokens) / 1_000_000) * price.OutputPer1M
	return inputCost + outputCost
}

// SavingsIfCached returns how much USD would have been spent had the request
// not been served from cache.
func SavingsIfCached(model string, inputTokens, outputTokens int) float64 {
	return EstimateCost(model, inputTokens, outputTokens)
}

// PriceFor returns the price entry for a model, or a default if unknown.
func PriceFor(model string) ModelPrice {
	p, ok := Pricing[model]
	if !ok {
		return ModelPrice{InputPer1M: 2.50, OutputPer1M: 10.00}
	}
	return p
}

// ── internal helpers ──────────────────────────────────────────────────────────

// estimateTokens gives an approximate input token count (4 chars ≈ 1 token).
func (r *Router) estimateTokens(req *types.ChatRequest) int {
	total := 0
	for _, m := range req.Messages {
		total += len(m.Content) / 4
	}
	return total
}

// classify returns the complexity level for a request.
func (r *Router) classify(req *types.ChatRequest, tokens int) types.ComplexityLevel {
	combined := strings.ToLower(allContent(req))

	// Explicit complexity signals override token count.
	for _, kw := range complexKeywords {
		if strings.Contains(combined, kw) {
			return types.ComplexityComplex
		}
	}

	// Token-count threshold.
	if tokens >= r.ComplexTokenThreshold {
		return types.ComplexityComplex
	}

	// Simple keyword signals.
	for _, kw := range simpleKeywords {
		if strings.Contains(combined, kw) {
			return types.ComplexitySimple
		}
	}

	if tokens < 150 {
		return types.ComplexitySimple
	}

	return types.ComplexityModerate
}

// allContent concatenates all message contents for keyword scanning.
func allContent(req *types.ChatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		b.WriteString(m.Content)
		b.WriteByte(' ')
	}
	return b.String()
}

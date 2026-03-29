# Lattice-Cost — The AI Economy & FinOps Monitor

> **"Stop the unexpected AI bill before it stops your business."**

[![Go Version](https://img.shields.io/badge/go-1.22+-00ADD8?logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/license-Apache%202.0-green)](LICENSE)
[![Part of](https://img.shields.io/badge/part%20of-Lattice%20Suite-blueviolet)](https://github.com/Karcsihack)

---

## The Problem: The Unexpected AI Bill

A developer leaves a loop calling GPT-4 overnight. Finance receives a $10,000 invoice on Monday morning. No warning. No cap. No explanation.

This is the **second great fear** of enterprise AI adoption — right next to data privacy (solved by [Lattice-Shield](https://github.com/Karcsihack/lattice-shield)).

**Lattice-Cost is the financial firewall.** It sits between your developers and the LLM APIs, tracking every dollar, caching every redundant call, and routing every prompt to the cheapest model that can do the job.

---

## Three Pillars of AI Cost Control

### 1. Budget Enforcement (Per API Key)

```
Marketing team  → $50/day limit
Engineering     → $200/day limit
Intern keys     → $5/day limit
```

When the limit is hit: `HTTP 429 — daily budget exceeded ($50.0000 limit)`.
No surprise invoices. Ever.

### 2. Redis Response Cache — 30-40% Savings, Immediately

```
Developer A: "Summarize microservices best practices"   → LLM called  → cached
Developer B: "Summarize microservices best practices"   → cache HIT   → $0.00
```

If two people ask the same thing, the second answer is free. Responses are stored in Redis with a configurable TTL (default 1 hour).

### 3. Smart Router — Right Model, Right Task

```
"What is REST?"        → SIMPLE   → gpt-4o-mini   ($0.00015/1M vs $2.50/1M)
"List Docker commands" → SIMPLE   → gpt-4o-mini
"Architect a CQRS system with event sourcing..."
                       → COMPLEX  → gpt-4o
```

Simple prompts are automatically downgraded to a 16x cheaper model. Complex prompts still get the best model. The developer never notices the difference.

---

## Architecture: 6th Pillar of the Lattice Suite

```
┌─────────────────────────────────────────────────────────────────────────┐
│                          LATTICE SUITE                                  │
├──────────┬────────────┬─────────────┬──────────┬──────────┬────────────┤
│ Proxy    │ Automate   │ Dashboard   │ SDK      │ Shield   │ Cost ◄HERE │
│ (Data)   │ (Rules)    │ (Visibility)│ (Access) │ (Code)   │ (FinOps)   │
│ :8080    │ Webhooks   │ Web UI      │ Unified  │ CLI/Hook │ :8081      │
└──────────┴────────────┴─────────────┴──────────┴──────────┴────────────┘
```

**Data flow:**

```
Developer IDE
      │
      ▼
Lattice-Shield (:hook)   ← Secret scan + IP anonymization
      │
      ▼
Lattice-Cost (:8081)     ← Budget • Cache • Smart Routing   ← THIS REPO
      │
      ▼
Lattice-Proxy (:8080)    ← PII scrubbing + audit log
      │
      ▼
LLM API (OpenAI / Anthropic / Gemini)
```

---

## Quick Start

```bash
# 1. Start Redis (required for cache + budget)
docker run -d -p 6379:6379 redis:7-alpine

# 2. Build and start Lattice-Cost
git clone https://github.com/Karcsihack/lattice-cost.git
cd lattice-cost
go mod tidy
go build -o lattice-cost .
./lattice-cost server

# 3. Point your LLM client at Lattice-Cost instead of OpenAI
export OPENAI_BASE_URL=http://localhost:8081/v1
```

That's it. All your LLM calls now go through the cost control layer.

---

## Configuration

All configuration is done via environment variables — no YAML files required.

| Variable                    | Default                  | Description                                 |
| --------------------------- | ------------------------ | ------------------------------------------- |
| `LATTICE_COST_ADDR`         | `:8081`                  | Server listen address                       |
| `UPSTREAM_URL`              | `https://api.openai.com` | Real LLM API base URL                       |
| `REDIS_ADDR`                | `localhost:6379`         | Redis for cache + budgets                   |
| `REDIS_PASSWORD`            | _(empty)_                | Redis auth password                         |
| `CACHE_ENABLED`             | `true`                   | Enable response cache                       |
| `CACHE_TTL`                 | `1h`                     | Cache entry lifetime                        |
| `BUDGET_ENABLED`            | `true`                   | Enable spend tracking                       |
| `DEFAULT_DAILY_LIMIT_USD`   | `50.0`                   | Daily cap per API key                       |
| `DEFAULT_MONTHLY_LIMIT_USD` | `1000.0`                 | Monthly cap per API key                     |
| `SMART_ROUTING_ENABLED`     | `true`                   | Enable smart model routing                  |
| `CHEAP_MODEL`               | `gpt-4o-mini`            | Model for simple prompts                    |
| `POWERFUL_MODEL`            | `gpt-4o`                 | Model for complex prompts                   |
| `COMPLEX_TOKEN_THRESHOLD`   | `500`                    | Token count above which prompts are COMPLEX |

### Per-Key Budget Overrides

```bash
# Format: LATTICE_BUDGET_<KEY_PREFIX>=<daily_limit>:<monthly_limit>
export LATTICE_BUDGET_sk-test=5.00:50.00    # test keys: $5/day, $50/month
export LATTICE_BUDGET_sk-prod=500.00        # prod keys: $500/day (no monthly cap)
```

---

## CLI Commands

```bash
# Start the middleware server
lattice-cost server

# Display the real-time FinOps report
lattice-cost report

# Preview which model would be selected for a prompt
lattice-cost route "Explain the CAP theorem in distributed systems"

# List all models and their pricing
lattice-cost models
```

---

## Real-Time FinOps Report

While the server runs, visit `http://localhost:8081/lattice/report` or run `lattice-cost report`:

```
  Lattice-Cost — FinOps Report  (2026-03-29 14:22:10 UTC)
  ──────────────────────────────────────────────────────────
  Total Requests  : 1,247
  Cache Hits      : 489  (39.2% hit rate)
  Cache Misses    : 758
  Total Cost      : $4.2381 USD
  Total Savings   : $3.1204 USD  (cache hits)
  Avg Cost/Req    : $0.003397 USD
  Avg Latency     : 812 ms
  Routing Saves   : 634 requests downgraded to cheaper model

  Complexity Breakdown:
    SIMPLE       412
    MODERATE     601
    COMPLEX      234

  Cost by Model:
    gpt-4o-mini                                       1013 req   $0.8271
    gpt-4o                                             234 req   $3.4110

  Cost by API Key:
    sk-p****Key1     512 req   $1.8320    saved $1.2100  (38% cache)
    sk-p****Key2     421 req   $1.5891    saved $0.9804  (41% cache)
    sk-t****Key3     314 req   $0.8170    saved $0.9300  (42% cache)
  ──────────────────────────────────────────────────────────
```

---

## Response Headers

Every intercepted request gets Lattice-Cost metadata headers:

| Header                      | Example         | Description                      |
| --------------------------- | --------------- | -------------------------------- |
| `X-Lattice-Cache`           | `HIT` or `MISS` | Whether cache was used           |
| `X-Lattice-Model-Requested` | `gpt-4o`        | What the client asked for        |
| `X-Lattice-Model-Used`      | `gpt-4o-mini`   | What was actually called         |
| `X-Lattice-Complexity`      | `SIMPLE`        | Prompt complexity classification |
| `X-Lattice-Cost-USD`        | `0.000142`      | Cost of this request             |
| `X-Lattice-Savings-USD`     | `0.002800`      | Savings vs. naive approach       |

---

## Supported Models & Pricing

Pricing is pre-loaded for 13 models. Run `lattice-cost models` for the full table:

| Model                      | Input/1M | Output/1M |
| -------------------------- | -------- | --------- |
| gpt-4o-mini                | $0.150   | $0.600    |
| gpt-4o                     | $2.500   | $10.000   |
| claude-3-haiku-20240307    | $0.250   | $1.250    |
| claude-3-5-sonnet-20241022 | $3.000   | $15.000   |
| gemini-1.5-flash           | $0.075   | $0.300    |
| gemini-1.5-pro             | $3.500   | $10.500   |
| _(and 7 more)_             |          |           |

---

## Integration with Lattice-Proxy

Lattice-Cost integrates natively with [Lattice-Proxy](https://github.com/Karcsihack/lattice-proxy):

```bash
# In lattice-cost, set upstream to lattice-proxy instead of OpenAI directly
UPSTREAM_URL=http://localhost:8080 ./lattice-cost server
```

This gives you: **Budget → Cache → Routing → PII Scrubbing → LLM** in one chain.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).

---

_Sixth pillar of the [Lattice Suite](https://github.com/Karcsihack) — Enterprise AI Governance Platform._

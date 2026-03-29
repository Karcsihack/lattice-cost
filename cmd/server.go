package cmd

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Karcsihack/lattice-cost/budget"
	"github.com/Karcsihack/lattice-cost/cache"
	"github.com/Karcsihack/lattice-cost/config"
	"github.com/Karcsihack/lattice-cost/metrics"
	"github.com/Karcsihack/lattice-cost/middleware"
	"github.com/Karcsihack/lattice-cost/router"
	"github.com/fatih/color"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Lattice-Cost middleware server",
	Long: `Start the HTTP server that intercepts LLM API traffic.

Point your LLM client at this server instead of the upstream API:
  export OPENAI_BASE_URL=http://localhost:8081/v1

The server will:
  1. Enforce per-key daily/monthly budgets
  2. Return Redis-cached responses for duplicate prompts
  3. Route simple prompts to cheaper models automatically
  4. Forward all other requests to the real LLM API

Environment variables (see README for full list):
  LATTICE_COST_ADDR         Listen address (default :8081)
  UPSTREAM_URL              LLM API base URL (default https://api.openai.com)
  REDIS_ADDR                Redis address (default localhost:6379)
  DEFAULT_DAILY_LIMIT_USD   Default daily budget per key (default 50.0)
  CHEAP_MODEL               Model for simple prompts (default gpt-4o-mini)
  POWERFUL_MODEL            Model for complex prompts (default gpt-4o)`,
	RunE: runServer,
}

func init() {
	rootCmd.AddCommand(serverCmd)
}

func runServer(_ *cobra.Command, _ []string) error {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen, color.Bold)
	cyan := color.New(color.FgCyan)
	yellow := color.New(color.FgYellow)

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	bold.Println(banner)
	fmt.Println()

	// ── Redis ──────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	redisOK := true
	if err := rdb.Ping(ctx).Err(); err != nil {
		yellow.Printf("  ⚠  Redis not reachable at %s — cache and budget disabled\n", cfg.RedisAddr)
		redisOK = false
	} else {
		green.Printf("  ✓  Redis connected at %s\n", cfg.RedisAddr)
	}

	// ── Components ─────────────────────────────────────────────────────────────
	var bud *budget.Tracker
	var cch *cache.Cache

	if redisOK {
		bud = budget.New(rdb, cfg.DefaultBudget.DailyLimitUSD, cfg.DefaultBudget.MonthlyLimitUSD)
		cch = cache.New(rdb, cfg.CacheTTL)
	}

	rtr := router.New(
		cfg.Models.CheapModel,
		cfg.Models.PowerfulModel,
		cfg.Models.ForceModel,
		cfg.Models.ComplexTokenThreshold,
	)

	col := metrics.NewCollector()
	mw := middleware.New(cfg, bud, cch, rtr, col)

	// ── HTTP mux ───────────────────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/", mw)
	mux.HandleFunc("/lattice/report", func(w http.ResponseWriter, _ *http.Request) {
		report := col.GenerateReport()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, metrics.FormatReport(report))
	})

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 95 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ── Status ─────────────────────────────────────────────────────────────────
	fmt.Println()
	bold.Println("  Configuration:")
	cyan.Printf("    Listen          : %s\n", cfg.ListenAddr)
	cyan.Printf("    Upstream        : %s\n", cfg.UpstreamURL)
	cyan.Printf("    Cheap model     : %s\n", cfg.Models.CheapModel)
	cyan.Printf("    Powerful model  : %s\n", cfg.Models.PowerfulModel)
	cyan.Printf("    Cache TTL       : %s\n", cfg.CacheTTL)
	cyan.Printf("    Daily limit     : $%.2f USD\n", cfg.DefaultBudget.DailyLimitUSD)
	fmt.Println()

	featureStatus := func(enabled bool, label string) {
		if enabled && redisOK {
			green.Printf("  ✓  %-20s enabled\n", label)
		} else {
			yellow.Printf("  ○  %-20s disabled\n", label)
		}
	}
	featureStatus(cfg.CacheEnabled, "Response Cache")
	featureStatus(cfg.BudgetEnabled, "Budget Tracking")
	if cfg.SmartRoutingEnabled {
		green.Printf("  ✓  %-20s enabled\n", "Smart Routing")
	} else {
		yellow.Printf("  ○  %-20s disabled\n", "Smart Routing")
	}
	fmt.Println()

	// ── Start ──────────────────────────────────────────────────────────────────
	go func() {
		green.Printf("  Lattice-Cost listening on %s\n\n", cfg.ListenAddr)
		log.Printf("  Report endpoint: http://localhost%s/lattice/report\n", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\n  Shutting down Lattice-Cost...")
	report := col.GenerateReport()
	fmt.Print(metrics.FormatReport(report))

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	return srv.Shutdown(shutCtx)
}

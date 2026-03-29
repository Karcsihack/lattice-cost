package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Karcsihack/lattice-cost/config"
	"github.com/Karcsihack/lattice-cost/router"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var reportCmd = &cobra.Command{
	Use:   "report",
	Short: "Fetch and display the real-time FinOps cost report",
	Long: `Fetch the live cost report from a running Lattice-Cost server.

Examples:
  lattice-cost report
  lattice-cost report --addr http://localhost:8081
  lattice-cost report --json`,
	RunE: runReport,
}

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List all supported models with their pricing",
	RunE:  runModels,
}

var routeCmd = &cobra.Command{
	Use:   "route <prompt>",
	Short: "Preview which model Lattice-Cost would select for a given prompt",
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRoute,
}

var (
	reportAddr string
	reportJSON bool
)

func init() {
	rootCmd.AddCommand(reportCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(routeCmd)

	reportCmd.Flags().StringVar(&reportAddr, "addr", "http://localhost:8081", "Lattice-Cost server address")
	reportCmd.Flags().BoolVar(&reportJSON, "json", false, "Output raw JSON")
}

func runReport(_ *cobra.Command, _ []string) error {
	url := reportAddr + "/lattice/report"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach Lattice-Cost at %s: %w\n  Is the server running? Start it with: lattice-cost server", reportAddr, err)
	}
	defer resp.Body.Close()

	if reportJSON {
		// Re-fetch as JSON from a different endpoint (fallback: just print raw)
		fmt.Printf(`{"note":"for JSON output run lattice-cost server and read /lattice/report"}` + "\n")
		return nil
	}

	// The report endpoint returns plain text.
	buf := make([]byte, 64*1024)
	n, _ := resp.Body.Read(buf)
	fmt.Print(string(buf[:n]))
	return nil
}

func runModels(_ *cobra.Command, _ []string) error {
	bold := color.New(color.Bold)
	cyan := color.New(color.FgCyan)
	green := color.New(color.FgGreen)

	bold.Println("\n  Lattice-Cost — Supported Models & Pricing (per 1M tokens)")
	fmt.Println()
	bold.Printf("  %-45s %12s %13s\n", "Model", "Input USD", "Output USD")
	fmt.Println("  " + fmt.Sprintf("%s", "─────────────────────────────────────────────────────────────────────────"))

	for model, price := range router.Pricing {
		cfg, _ := config.Load()
		cheap := model == cfg.Models.CheapModel
		powerful := model == cfg.Models.PowerfulModel

		label := ""
		if cheap {
			label = " ← cheap"
		} else if powerful {
			label = " ← powerful"
		}

		if cheap {
			green.Printf("  %-45s $%11.3f  $%12.3f%s\n", model, price.InputPer1M, price.OutputPer1M, label)
		} else if powerful {
			cyan.Printf("  %-45s $%11.3f  $%12.3f%s\n", model, price.InputPer1M, price.OutputPer1M, label)
		} else {
			fmt.Printf("  %-45s $%11.3f  $%12.3f\n", model, price.InputPer1M, price.OutputPer1M)
		}
	}
	fmt.Println()
	return nil
}

func runRoute(_ *cobra.Command, args []string) error {
	bold := color.New(color.Bold)
	green := color.New(color.FgGreen, color.Bold)
	yellow := color.New(color.FgYellow, color.Bold)
	red := color.New(color.FgRed, color.Bold)

	cfg, _ := config.Load()

	promptText := args[0]
	tokenEst := len(promptText) / 4

	var chosenModel, complexityLabel string
	switch {
	case tokenEst >= cfg.Models.ComplexTokenThreshold:
		chosenModel = cfg.Models.PowerfulModel
		complexityLabel = "COMPLEX"
	case tokenEst < 150:
		chosenModel = cfg.Models.CheapModel
		complexityLabel = "SIMPLE"
	default:
		chosenModel = cfg.Models.CheapModel
		complexityLabel = "MODERATE"
	}

	powerPrice := router.PriceFor(cfg.Models.PowerfulModel)
	chosenPrice := router.PriceFor(chosenModel)

	estOutput := tokenEst / 3
	estCost := router.EstimateCost(chosenModel, tokenEst, estOutput)
	fullCost := router.EstimateCost(cfg.Models.PowerfulModel, tokenEst, estOutput)
	savings := fullCost - estCost

	bold.Println("\n  Lattice-Cost — Route Preview")
	fmt.Println()
	fmt.Printf("  Prompt length    : %d chars (~%d tokens)\n", len(promptText), tokenEst)
	fmt.Printf("  Complexity       : ")
	switch complexityLabel {
	case "SIMPLE", "MODERATE":
		green.Println(complexityLabel)
	default:
		red.Println(complexityLabel)
	}
	fmt.Printf("  Model requested  : gpt-4o  ($%.3f / $%.3f per 1M tokens)\n",
		powerPrice.InputPer1M, powerPrice.OutputPer1M)
	fmt.Printf("  Model selected   : ")
	if chosenModel == cfg.Models.CheapModel {
		green.Printf("%s", chosenModel)
	} else {
		yellow.Printf("%s", chosenModel)
	}
	fmt.Printf("  ($%.3f / $%.3f per 1M tokens)\n", chosenPrice.InputPer1M, chosenPrice.OutputPer1M)
	fmt.Printf("  Est. cost        : $%.6f USD\n", estCost)
	if savings > 0 {
		green.Printf("  Est. saving      : $%.6f USD vs. full model\n", savings)
	}
	fmt.Println()
	os.Exit(0)
	return nil
}

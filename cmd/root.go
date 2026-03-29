// Package cmd provides the Lattice-Cost CLI.
package cmd

import (
	"github.com/spf13/cobra"
)

const banner = `
  ┌──────────────────────────────────────────────────────────┐
  │   LATTICE-COST  v1.0.0  —  The AI Economy Monitor        │
  │                                                          │
  │   Budget · Cache · Smart Routing · Real-time FinOps      │
  └──────────────────────────────────────────────────────────┘`

var rootCmd = &cobra.Command{
	Use:     "lattice-cost",
	Short:   "The AI Economy & FinOps Monitor for LLM cost control",
	Long:    banner + "\n\n  Stop the unexpected AI bill. Budget, cache, and route smarter.",
	Version: "1.0.0",
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().String("redis", "localhost:6379", "Redis address for cache and budget")
	rootCmd.PersistentFlags().String("upstream", "https://api.openai.com", "Upstream LLM API base URL")
}

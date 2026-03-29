// Lattice-Cost — The AI Economy & FinOps Monitor
// Sixth pillar of the Lattice Suite: Enterprise AI Governance Platform.
// Copyright (c) 2026 Lattice Suite. All rights reserved.
package main

import (
	"fmt"
	"os"

	"github.com/Karcsihack/lattice-cost/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

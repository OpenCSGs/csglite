package main

import (
	"context"
	"fmt"
	"os"

	"github.com/opencsgs/csglite/internal/cli"
	"github.com/opencsgs/csglite/internal/upgrade"
)

var version = "dev"

func main() {
	// Set version for upgrade module
	upgrade.SetVersion(version)

	ctx := context.Background()
	if err := cli.NewRootCmd(version).ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

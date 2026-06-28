package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Search for models on CSGHub",
		Args:  cobra.ExactArgs(1),
		RunE:  runSearch,
	}
	return cmd
}

func runSearch(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	client := csghub.NewClient(cfg.ServerURL, cfg.Token)
	models, total, err := client.SearchModels(cmd.Context(), args[0], 1, 20)
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if len(models) == 0 {
		fmt.Println("No models found.")
		return nil
	}

	fmt.Printf("Found %d models (showing top %d):\n\n", total, len(models))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tDOWNLOADS\tLICENSE\tDESCRIPTION")
	for _, m := range models {
		desc := m.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n",
			m.Path, m.Downloads, m.License, desc)
	}
	return w.Flush()
}

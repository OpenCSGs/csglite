package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/spf13/cobra"
)

func newShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show MODEL",
		Short: "Show details of a local model",
		Long:  "Display detailed information about a locally downloaded model.",
		Args:  cobra.ExactArgs(1),
		RunE:  runShow,
	}
	return cmd
}

func runShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := model.NewManager(cfg)
	modelID := args[0]

	lm, err := mgr.Get(modelID)
	if err != nil {
		return fmt.Errorf("model %q not found locally", modelID)
	}

	modelDir, _ := mgr.ModelPath(modelID)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Name:\t%s\n", lm.FullName())
	fmt.Fprintf(w, "Format:\t%s\n", lm.Format)
	fmt.Fprintf(w, "Size:\t%s\n", formatBytes(lm.Size))
	fmt.Fprintf(w, "Downloaded:\t%s\n", lm.DownloadedAt.Format("2006-01-02 15:04:05"))
	if lm.License != "" {
		fmt.Fprintf(w, "License:\t%s\n", lm.License)
	}
	if lm.Description != "" {
		fmt.Fprintf(w, "Description:\t%s\n", truncate(lm.Description, 120))
	}
	fmt.Fprintf(w, "Path:\t%s\n", modelDir)
	fmt.Fprintf(w, "Files:\t%s\n", strings.Join(lm.Files, ", "))
	w.Flush()

	return nil
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

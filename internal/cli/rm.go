package cli

import (
	"fmt"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a locally downloaded model or dataset",
		Long: `Remove a locally downloaded model or dataset.

By default it removes a model. Use --dataset to remove a dataset instead.

Examples:
  csghub-lite rm Qwen/Qwen3-0.6B-GGUF
  csghub-lite rm --dataset wikitext/wikitext-2-raw-v1`,
		Args: cobra.ExactArgs(1),
		RunE: runRm,
	}
	cmd.Flags().BoolP("dataset", "d", false, "Remove a dataset instead of a model")
	return cmd
}

func runRm(cmd *cobra.Command, args []string) error {
	isDataset, _ := cmd.Flags().GetBool("dataset")

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	name := args[0]

	if isDataset {
		mgr := dataset.NewManager(cfg)
		if err := mgr.Remove(name); err != nil {
			return fmt.Errorf("removing dataset: %w", err)
		}
		fmt.Printf("Removed dataset %s\n", name)
	} else {
		mgr := model.NewManager(cfg)
		if err := mgr.Remove(name); err != nil {
			return fmt.Errorf("removing model: %w", err)
		}
		fmt.Printf("Removed model %s\n", name)
	}
	return nil
}

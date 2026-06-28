package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List locally available models or datasets",
		Long: `List locally available models or datasets.

By default it lists models. Use --dataset to list datasets instead.

Examples:
  csghub-lite list
  csghub-lite list --dataset`,
		RunE: runList,
	}
	cmd.Flags().BoolP("dataset", "d", false, "List datasets instead of models")
	return cmd
}

func runList(cmd *cobra.Command, args []string) error {
	isDataset, _ := cmd.Flags().GetBool("dataset")
	if isDataset {
		return runListDatasets(cmd)
	}
	return runListModels(cmd)
}

func runListModels(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := model.NewManager(cfg)
	models, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing models: %w", err)
	}

	if len(models) == 0 {
		fmt.Println("No models downloaded. Use 'csghub-lite pull' to download a model.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tFORMAT\tSIZE\tDOWNLOADED")
	for _, m := range models {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			m.FullName(),
			m.Format,
			formatBytes(m.Size),
			m.DownloadedAt.Format("2006-01-02 15:04"),
		)
	}
	return w.Flush()
}

func runListDatasets(cmd *cobra.Command) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := dataset.NewManager(cfg)
	datasets, err := mgr.List()
	if err != nil {
		return fmt.Errorf("listing datasets: %w", err)
	}

	if len(datasets) == 0 {
		fmt.Println("No datasets downloaded. Use 'csghub-lite pull --dataset' to download a dataset.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tFILES\tSIZE\tDOWNLOADED")
	for _, d := range datasets {
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\n",
			d.FullName(),
			len(d.Files),
			formatBytes(d.Size),
			d.DownloadedAt.Format("2006-01-02 15:04"),
		)
	}
	return w.Flush()
}

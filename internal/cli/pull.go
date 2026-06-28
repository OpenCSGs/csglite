package cli

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/csghub"
	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/ggufpick"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	quantHelp := "GGUF quantization to download when multiple weight variants exist. " +
		"Common choice: Q4_K_M for smaller local models, Q8_0 for higher quality. " +
		"Ignored for non-GGUF models. Supported labels: " + strings.Join(ggufpick.KnownQuantLabels(), ", ")

	cmd := &cobra.Command{
		Use:   "pull NAME",
		Short: "Download a model or dataset from CSGHub",
		Long: `Download a model or dataset from the CSGHub platform.
NAME should be in the format namespace/name.

By default it downloads a model. Use --dataset to download a dataset instead.

For GGUF repositories that publish several quantization variants, use --quant to pick one
(for example Q4_K_M or Q8_0). Other model formats ignore --quant.

Examples:
  csghub-lite pull Qwen/Qwen3-0.6B-GGUF
  csghub-lite pull Qwen/Qwen3-0.6B-GGUF --quant Q4_K_M
  csghub-lite pull --dataset wikitext/wikitext-2-raw-v1`,
		Args: cobra.ExactArgs(1),
		RunE: runPull,
	}
	cmd.Flags().BoolP("dataset", "d", false, "Download a dataset instead of a model")
	cmd.Flags().String("quant", "", quantHelp)
	return cmd
}

func runPull(cmd *cobra.Command, args []string) error {
	isDataset, _ := cmd.Flags().GetBool("dataset")
	if isDataset {
		return runPullDataset(cmd, args)
	}
	return runPullModel(cmd, args)
}

func runPullModel(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := model.NewManager(cfg)
	modelID := args[0]
	quant, _ := cmd.Flags().GetString("quant")

	fmt.Printf("Pulling model %s from %s...\n", modelID, cfg.ServerURL)

	progress := snapshotProgress()

	lm, err := mgr.Pull(cmd.Context(), modelID, quant, progress)
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}

	fmt.Printf("\nSuccessfully pulled model %s (%s, %s)\n",
		lm.FullName(), lm.Format, formatBytes(lm.Size))
	return nil
}

func runPullDataset(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := dataset.NewManager(cfg)
	datasetID := args[0]

	fmt.Printf("Pulling dataset %s from %s...\n", datasetID, cfg.ServerURL)

	progress := snapshotProgress()

	ld, err := mgr.Pull(cmd.Context(), datasetID, progress)
	if err != nil {
		return fmt.Errorf("pull failed: %w", err)
	}

	fmt.Printf("\nSuccessfully pulled dataset %s (%d files, %s)\n",
		ld.FullName(), len(ld.Files), formatBytes(ld.Size))
	return nil
}

func snapshotProgress() csghub.SnapshotProgressFunc {
	var mu sync.Mutex
	completed := make(map[int]bool)
	active := make(map[int]csghub.SnapshotProgress)
	prevActiveCount := 0
	lastRender := time.Time{}

	return func(p csghub.SnapshotProgress) {
		mu.Lock()
		defer mu.Unlock()

		done := p.BytesTotal > 0 && p.BytesCompleted >= p.BytesTotal

		if done {
			if completed[p.FileIndex] {
				return
			}
			completed[p.FileIndex] = true
			delete(active, p.FileIndex)
		} else {
			active[p.FileIndex] = p
			if time.Since(lastRender) < 150*time.Millisecond {
				return
			}
		}

		// Move cursor up to erase the previous active lines
		for i := 0; i < prevActiveCount; i++ {
			fmt.Print("\033[A\033[2K")
		}

		if done {
			fmt.Printf("  [%d/%d] %s (%s)\n",
				p.FileIndex+1, p.TotalFiles, p.FileName,
				formatBytes(p.BytesTotal))
		}

		indices := make([]int, 0, len(active))
		for idx := range active {
			indices = append(indices, idx)
		}
		sort.Ints(indices)

		for _, idx := range indices {
			ap := active[idx]
			if ap.BytesTotal > 0 {
				pct := float64(ap.BytesCompleted) / float64(ap.BytesTotal) * 100
				fmt.Printf("  [%d/%d] %s  %.1f%% (%s / %s)\n",
					ap.FileIndex+1, ap.TotalFiles, ap.FileName,
					pct, formatBytes(ap.BytesCompleted), formatBytes(ap.BytesTotal))
			} else {
				fmt.Printf("  [%d/%d] %s\n",
					ap.FileIndex+1, ap.TotalFiles, ap.FileName)
			}
		}
		prevActiveCount = len(indices)
		lastRender = time.Now()
	}
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

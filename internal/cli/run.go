package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var numCtx int
	var numParallel int
	var nGPULayers int
	var cacheTypeK string
	var cacheTypeV string
	var dtype string
	var keepAlive string

	cmd := &cobra.Command{
		Use:   "run MODEL",
		Short: "Download (if needed) and chat with a model",
		Long: `Download a model from CSGHub if not already present, then start an interactive
chat session. Type your message and press Enter to send. Use '/bye' to exit.

Multiline input: end a line with '\' to continue on the next line.

Use --num-ctx, --num-parallel, --n-gpu-layers, --cache-type-k, and --cache-type-v to override
llama-server runtime settings for this run only.

Use --dtype to control SafeTensors -> GGUF conversion output type when a model
needs conversion.

Use --keep-alive to control how long the model stays loaded after you exit
(-1 keeps it loaded until you stop it manually).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRun(cmd, args, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, keepAlive)
		},
	}
	cmd.Flags().IntVar(&numCtx, "num-ctx", 0, "set the per-model context length for this run only (for example 131072)")
	cmd.Flags().IntVar(&numParallel, "num-parallel", 0, "set the llama-server parallel slots for this run only (use 1 to maximize single-session context)")
	cmd.Flags().IntVar(&nGPULayers, "n-gpu-layers", -1, "set llama-server --n-gpu-layers for this run only (for example 40; use 0 to disable GPU offload)")
	cmd.Flags().StringVar(&cacheTypeK, "cache-type-k", "", "set llama-server --cache-type-k for this run only ("+llamaRuntimeCacheTypeHelp()+")")
	cmd.Flags().StringVar(&cacheTypeV, "cache-type-v", "", "set llama-server --cache-type-v for this run only ("+llamaRuntimeCacheTypeHelp()+")")
	cmd.Flags().StringVar(&dtype, "dtype", "", "set SafeTensors -> GGUF conversion dtype for this run only ("+convertDTypeHelp()+")")
	cmd.Flags().StringVar(&keepAlive, "keep-alive", "", "keep the model loaded after exit for this run (for example 5m, 1h, or -1 to keep it loaded until stopped)")
	return cmd
}

func llamaRuntimeCacheTypeHelp() string {
	return "allowed: " + strings.Join(inference.AllowedCacheTypes(), ", ")
}

func convertDTypeHelp() string {
	return "allowed: " + strings.Join(convert.AllowedDTypes(), ", ")
}

func convertStatusMessage(dtype string) string {
	effectiveDType, err := convert.ResolveDType(dtype)
	if err != nil {
		effectiveDType = dtype
		if strings.TrimSpace(effectiveDType) == "" {
			effectiveDType = "unknown"
		}
	}
	return fmt.Sprintf(
		"Converting model to GGUF format (output dtype: %s, first time only, this may take a moment)...",
		effectiveDType,
	)
}

func validateInteractiveModelOverrides(numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) error {
	if numCtx > 0 && numCtx < 1024 {
		return fmt.Errorf("--num-ctx must be at least 1024 when set")
	}
	if numParallel < 0 {
		return fmt.Errorf("--num-parallel must be at least 1 when set")
	}
	if _, err := inference.NormalizeNGPULayers(nGPULayers); err != nil {
		return fmt.Errorf("--n-gpu-layers %w", err)
	}
	if _, err := inference.NormalizeCacheType(cacheTypeK); err != nil {
		return fmt.Errorf("--cache-type-k %w", err)
	}
	if _, err := inference.NormalizeCacheType(cacheTypeV); err != nil {
		return fmt.Errorf("--cache-type-v %w", err)
	}
	if _, err := convert.NormalizeDType(dtype); err != nil {
		return fmt.Errorf("--dtype %w", err)
	}
	return nil
}

func validateRunOverrides(numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype, keepAlive string) error {
	if err := validateInteractiveModelOverrides(numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype); err != nil {
		return err
	}
	if _, _, err := api.ParseKeepAlive(keepAlive); err != nil {
		return fmt.Errorf("--keep-alive %w", err)
	}
	return nil
}

func runRun(cmd *cobra.Command, args []string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype, keepAlive string) error {
	if err := validateRunOverrides(numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, keepAlive); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := model.NewManager(cfg)
	modelID := args[0]

	// Pull model if not present
	if !mgr.Exists(modelID) {
		fmt.Printf("Model %s not found locally. Pulling from %s...\n", modelID, cfg.DisplayURL())
		if _, err := mgr.Pull(cmd.Context(), modelID, "", snapshotProgress()); err != nil {
			return fmt.Errorf("pull failed: %w", err)
		}
		fmt.Println("Pull complete.")
	}

	fmt.Printf("Loading %s...\n", modelID)

	if modelDir, err := mgr.ModelPath(modelID); err == nil {
		needsConversion, err := convert.NeedsConversionForDType(modelDir, dtype)
		if err != nil {
			return err
		}
		if needsConversion {
			fmt.Println(convertStatusMessage(dtype))
		}
	}

	serverURL, err := ensureServer(cfg)
	if err != nil {
		return fmt.Errorf("starting server: %w", err)
	}

	if err := preloadModel(serverURL, modelID, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, keepAlive); err != nil {
		return fmt.Errorf("loading model: %w", err)
	}

	eng := inference.NewRemoteEngine(serverURL, modelID, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)

	fmt.Printf("Model %s ready. Type '/bye' to exit, '/clear' to reset context.\n\n", modelID)

	opts := inference.DefaultOptions()
	session := inference.NewSession(eng, opts)

	return chatLoop(cmd.Context(), session)
}

func chatLoop(ctx context.Context, session *Session) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print(">>> ")
		input, ok := readMultilineInput(scanner)
		if !ok {
			fmt.Println()
			return nil
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "/bye", "/exit", "/quit":
			fmt.Println("Goodbye!")
			return nil
		case "/clear":
			session = inference.NewSession(session.Engine(), session.Options())
			fmt.Println("Context cleared.")
			continue
		case "/help":
			printHelp()
			continue
		}

		onToken := func(token string) {
			fmt.Print(token)
		}

		_, err := session.Send(ctx, input, onToken)
		if err != nil {
			fmt.Printf("\nError: %v\n", err)
			continue
		}
		fmt.Println()
		fmt.Println()
	}
}

// Session wraps inference.Session for the chat loop, providing mutable re-creation.
type Session = inference.Session

func readMultilineInput(scanner *bufio.Scanner) (string, bool) {
	var lines []string
	for {
		if !scanner.Scan() {
			if len(lines) > 0 {
				return strings.Join(lines, "\n"), true
			}
			return "", false
		}
		line := scanner.Text()
		if strings.HasSuffix(line, "\\") {
			lines = append(lines, strings.TrimSuffix(line, "\\"))
			fmt.Print("... ")
			continue
		}
		lines = append(lines, line)
		return strings.Join(lines, "\n"), true
	}
}

func printHelp() {
	fmt.Println(`Commands:
  /bye, /exit, /quit  Exit the chat
  /clear              Clear conversation context
  /help               Show this help

Tips:
  - End a line with '\' for multiline input
  - Press Ctrl+D to exit`)
}

package cli

import (
	"fmt"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/convert"
	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/model"
	"github.com/spf13/cobra"
)

func newChatCmd() *cobra.Command {
	var systemPrompt string
	var numCtx int
	var numParallel int
	var nGPULayers int
	var cacheTypeK string
	var cacheTypeV string
	var dtype string

	cmd := &cobra.Command{
		Use:   "chat MODEL",
		Short: "Start an interactive chat with a local model",
		Long: `Start an interactive chat session with a locally downloaded model.
Unlike 'run', this command does not auto-download missing models.

Type your message and press Enter to send. Use '/bye' to exit.
Multiline input: end a line with '\' to continue on the next line.

Use --num-ctx, --num-parallel, --n-gpu-layers, --cache-type-k, and --cache-type-v to override
llama-server runtime settings for this chat session only.

Use --dtype to control SafeTensors -> GGUF conversion output type when a model
needs conversion.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd, args, systemPrompt, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)
		},
	}

	cmd.Flags().StringVar(&systemPrompt, "system", "", "set a custom system prompt")
	cmd.Flags().IntVar(&numCtx, "num-ctx", 0, "set the per-model context length for this chat session only (for example 131072)")
	cmd.Flags().IntVar(&numParallel, "num-parallel", 0, "set the llama-server parallel slots for this chat session only (use 1 to maximize single-session context)")
	cmd.Flags().IntVar(&nGPULayers, "n-gpu-layers", -1, "set llama-server --n-gpu-layers for this chat session only (for example 40; use 0 to disable GPU offload)")
	cmd.Flags().StringVar(&cacheTypeK, "cache-type-k", "", "set llama-server --cache-type-k for this chat session only ("+llamaRuntimeCacheTypeHelp()+")")
	cmd.Flags().StringVar(&cacheTypeV, "cache-type-v", "", "set llama-server --cache-type-v for this chat session only ("+llamaRuntimeCacheTypeHelp()+")")
	cmd.Flags().StringVar(&dtype, "dtype", "", "set SafeTensors -> GGUF conversion dtype for this chat session only ("+convertDTypeHelp()+")")
	return cmd
}

func runChat(cmd *cobra.Command, args []string, systemPrompt string, numCtx, numParallel, nGPULayers int, cacheTypeK, cacheTypeV, dtype string) error {
	if err := validateInteractiveModelOverrides(numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	mgr := model.NewManager(cfg)
	modelID := args[0]

	if !mgr.Exists(modelID) {
		return fmt.Errorf("model %q not found locally. Use 'csghub-lite pull %s' to download it first", modelID, modelID)
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

	if err := preloadModel(serverURL, modelID, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype, ""); err != nil {
		return fmt.Errorf("loading model: %w", err)
	}

	eng := inference.NewRemoteEngine(serverURL, modelID, numCtx, numParallel, nGPULayers, cacheTypeK, cacheTypeV, dtype)

	fmt.Printf("Model %s ready. Type '/bye' to exit, '/clear' to reset context, '/help' for more.\n\n", modelID)

	opts := inference.DefaultOptions()
	session := inference.NewSession(eng, opts)

	if systemPrompt != "" {
		session.SetSystemPrompt(systemPrompt)
	}

	return chatLoop(cmd.Context(), session)
}

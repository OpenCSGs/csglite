package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newModelConfigCmd() *cobra.Command {
	var ctx, parallel int
	var keepAlive, dtype, backend string
	cmd := &cobra.Command{
		Use:     "model-config MODEL",
		Example: "  csghub-lite model-config Qwen/Qwen3-0.6B-GGUF --num-ctx 8192",
		Short:   "Read or update a local model's saved runtime settings",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Pass the entire storage or public ID as one escaped segment. This
			// also addresses registry-qualified IDs with two slashes.
			id := strings.TrimSpace(args[0])
			for _, segment := range strings.Split(id, "/") {
				if segment == "" || segment == "." || segment == ".." {
					return fmt.Errorf("invalid model ID %q", id)
				}
			}
			path := "/api/models/" + url.PathEscape(id) + "/config"
			var patch api.ModelConfigUpdateRequest
			flags := cmd.Flags()
			if flags.Changed("num-ctx") {
				patch.NumCtx = &ctx
			}
			if flags.Changed("num-parallel") {
				patch.NumParallel = &parallel
			}
			if flags.Changed("keep-alive") {
				patch.KeepAlive = &keepAlive
			}
			if flags.Changed("dtype") {
				patch.DType = &dtype
			}
			if flags.Changed("backend") {
				patch.Backend = &backend
			}
			if patch == (api.ModelConfigUpdateRequest{}) {
				return printLocalResponse(cmd, http.MethodGet, path, nil)
			}
			return printLocalResponse(cmd, http.MethodPut, path, patch)
		},
	}
	cmd.Flags().IntVar(&ctx, "num-ctx", 0, "context length (0 clears)")
	cmd.Flags().IntVar(&parallel, "num-parallel", 0, "parallel slots (0 clears)")
	cmd.Flags().StringVar(&keepAlive, "keep-alive", "", "idle duration (empty clears)")
	cmd.Flags().StringVar(&dtype, "dtype", "", "GGUF conversion dtype (empty clears)")
	cmd.Flags().StringVar(&backend, "backend", "", "inference backend, llama or sglang (empty clears)")
	return cmd
}

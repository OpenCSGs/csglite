package cli

import (
	"net/http"

	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newLoadCmd() *cobra.Command {
	var keepAlive string
	cmd := &cobra.Command{
		Use:     "load MODEL",
		Example: "  csghub-lite load Qwen/Qwen-Image-2.1 --keep-alive 5m",
		Short:   "Load a downloaded model and wait until it is ready",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return printLocalResponse(cmd, http.MethodPost, "/api/load", api.LoadRequest{Model: args[0], KeepAlive: keepAlive})
		},
	}
	cmd.Flags().StringVar(&keepAlive, "keep-alive", "", "idle duration (e.g. 5m or -1 for indefinite)")
	return cmd
}

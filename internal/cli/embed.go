package cli

import (
	"net/http"

	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newEmbedCmd() *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:     "embed MODEL [TEXT...]",
		Example: "  csghub-lite embed BAAI/bge-m3 \"first text\" \"second text\"",
		Short:   "Embed one or more texts and print OpenAI JSON (reads one input from stdin when omitted)",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := inferenceRoute(source, "embeddings")
			if err != nil {
				return err
			}
			inputs, err := inferenceInputs(cmd, args[1:])
			if err != nil {
				return err
			}
			return printLocalResponse(cmd, http.MethodPost, path, api.OpenAIEmbeddingsRequest{Model: args[0], Input: inputs})
		},
	}
	cmd.Flags().StringVar(&source, "source", "local", "inference provider ID (local, cloud, cluster, or a configured provider)")
	return cmd
}

package cli

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func inferenceInputs(cmd *cobra.Command, args []string) ([]string, error) {
	if len(args) == 0 {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		args = []string{string(data)}
	}
	for _, s := range args {
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("provide non-empty text as an argument or on stdin")
		}
	}
	return args, nil
}

func inferenceRoute(source, endpoint string) (string, error) {
	if source == "" || source == "." || source == ".." || strings.ContainsAny(source, "/\\?#") {
		return "", fmt.Errorf("invalid inference source %q", source)
	}
	return "/ai-gateway/providers/" + url.PathEscape(source) + "/v1/" + endpoint, nil
}

func newGenerateCmd() *cobra.Command {
	var source string
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "generate MODEL [TEXT]",
		Example: "  csghub-lite generate Qwen/Qwen3-0.6B-GGUF \"Hello\"",
		Short:   "Generate one text response (reads stdin when TEXT is omitted)",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := inferenceRoute(source, "chat/completions")
			if err != nil {
				return err
			}
			inputs, err := inferenceInputs(cmd, args[1:])
			if err != nil {
				return err
			}
			req := api.OpenAIChatRequest{Model: args[0], Messages: []api.Message{{Role: "user", Content: inputs[0]}}}
			if asJSON {
				return printLocalResponse(cmd, http.MethodPost, path, req)
			}
			var result api.OpenAIChatResponse
			if err := requestLocalJSON(cmd, http.MethodPost, path, req, &result); err != nil {
				return err
			}
			if len(result.Choices) == 0 || result.Choices[0].Message == nil {
				return fmt.Errorf("server returned no text choice")
			}
			content, ok := result.Choices[0].Message.Content.(string)
			if !ok {
				return fmt.Errorf("server returned a non-text choice")
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), content)
			return err
		},
	}
	cmd.Flags().StringVar(&source, "source", "local", "inference provider ID (local, cloud, cluster, or a configured provider)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the complete OpenAI response")
	return cmd
}

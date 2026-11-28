package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/opencsgs/csglite/ee/edge"
	"github.com/spf13/cobra"
)

func newEdgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edge",
		Short: "Contribute compute to CSGHub and inspect applications and earnings",
	}
	cmd.AddCommand(
		localAction("status", "Show connection, quota and available model IDs", http.MethodGet, "/api/edge", nil),
		localAction("enable", "Enable the edge connector", http.MethodPut, "/api/edge", map[string]bool{"enabled": true}),
		localAction("disable", "Disable the edge connector", http.MethodPut, "/api/edge", map[string]bool{"enabled": false}),
		newEdgeApplicationsCmd(), newEdgeApplyCmd(), newEdgeWithdrawCmd(), newEdgeEarningsCmd(),
		localAction("balance", "Show the signed-in account's balance", http.MethodGet, "/api/edge/balance", nil),
		localAction("statements", "List settlement statements", http.MethodGet, "/api/edge/statements", nil),
	)
	return cmd
}

func newEdgeApplicationsCmd() *cobra.Command {
	var page, per int
	cmd := &cobra.Command{
		Use:   "applications",
		Short: "List contributions and review status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if page < 1 || per < 1 {
				return fmt.Errorf("--page and --per must be positive")
			}
			return printLocalResponse(cmd, http.MethodGet, fmt.Sprintf("/api/edge/applications?page=%d&per=%d", page, per), nil)
		},
	}
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&per, "per", 20, "applications per page")
	return cmd
}

var edgePublicName = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")

func newEdgeApplyCmd() *cobra.Command {
	var req edge.SubmitRequest
	cmd := &cobra.Command{
		Use:     "apply MODEL",
		Example: "  csghub-lite edge apply MODEL --name my-model --prompt-price 100 --completion-price 200",
		Short:   "Apply to serve a model ID from 'edge status'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !edgePublicName.MatchString(req.ModelName) {
				return fmt.Errorf("invalid public model name %q", req.ModelName)
			}
			if req.PromptPrice < 0 || req.CompletionPrice < 0 || req.MaxConcurrency < 1 {
				return fmt.Errorf("prices must be non-negative and concurrency must be positive")
			}
			req.LocalModelID = args[0]
			return printLocalResponse(cmd, http.MethodPost, "/api/edge/applications", req)
		},
	}
	cmd.Flags().StringVar(&req.ModelName, "name", "", "public model name")
	cmd.Flags().StringVar(&req.Description, "description", "", "description for reviewers")
	cmd.Flags().Int64Var(&req.PromptPrice, "prompt-price", 0, "input price in fen per million tokens (100 fen = 1 yuan)")
	cmd.Flags().Int64Var(&req.CompletionPrice, "completion-price", 0, "output price in fen per million tokens")
	cmd.Flags().IntVar(&req.MaxConcurrency, "max-concurrency", 4, "maximum concurrent requests")
	for _, flag := range []string{"name", "prompt-price", "completion-price"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}

func newEdgeWithdrawCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw ID",
		Short: "Withdraw a contribution by application ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("expected a positive application ID")
			}
			return printLocalResponse(cmd, http.MethodPost, fmt.Sprintf("/api/edge/applications/%d/withdraw", id), nil)
		},
	}
}

func newEdgeEarningsCmd() *cobra.Command {
	var month string
	cmd := &cobra.Command{
		Use:   "earnings",
		Short: "Show monthly earnings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/edge/earnings"
			if month != "" {
				if _, err := time.Parse("2006-01", month); err != nil {
					return fmt.Errorf("--month must be YYYY-MM")
				}
				path += "?month=" + url.QueryEscape(month)
			}
			return printLocalResponse(cmd, http.MethodGet, path, nil)
		},
	}
	cmd.Flags().StringVar(&month, "month", "", "YYYY-MM (defaults to this month)")
	return cmd
}

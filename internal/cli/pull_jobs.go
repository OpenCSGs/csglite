package cli

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "download", Short: "Start and manage background model downloads"}
	cmd.AddCommand(newDownloadStartCmd(false), newDownloadJobAction("status", http.MethodGet), newDownloadJobAction("cancel", http.MethodDelete))
	return cmd
}

func newDownloadStartCmd(onNode bool) *cobra.Command {
	use, short := "start REPOSITORY", "Queue a model download on the local server"
	nargs := 1
	if onNode {
		use, short, nargs = "pull NODE REPOSITORY", "Queue a model download on a cluster node", 2
	}
	var req api.PullRequest
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/api/pull/jobs"
			if onNode {
				node, err := uuid.Parse(args[0])
				if err != nil {
					return fmt.Errorf("invalid node UUID: %w", err)
				}
				path = "/api/cluster/nodes/" + node.String() + "/models/pull"
			}
			req.Model = args[len(args)-1]
			return printLocalResponse(cmd, http.MethodPost, path, req)
		},
	}
	cmd.Flags().StringVar(&req.ArtifactSource, "source", "opencsg", "artifact registry (opencsg, huggingface or modelscope)")
	cmd.Flags().StringVar(&req.Revision, "revision", "", "branch, tag or commit")
	cmd.Flags().StringSliceVar(&req.Quants, "quant", nil, "GGUF quantizations to download")
	return cmd
}

func newDownloadJobAction(name, method string) *cobra.Command {
	return &cobra.Command{Use: name + " JOB_ID", Short: name + " a local background download", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pathID(args[0])
			if err != nil {
				return err
			}
			return printLocalResponse(cmd, method, "/api/pull/jobs/"+id, nil)
		},
	}
}

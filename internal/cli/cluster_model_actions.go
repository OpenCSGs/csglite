package cli

import (
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/spf13/cobra"
)

func newNodeModelAction(action string) *cobra.Command {
	var source string
	cmd := &cobra.Command{Use: action + " NODE MODEL REPOSITORY", Short: action + " an exact model artifact on a cluster node", Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			node, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invalid node UUID: %w", err)
			}
			body := cluster.ModelAction{Action: action, Model: args[1], Repository: args[2], ArtifactSource: source}
			return printLocalResponse(cmd, http.MethodPost, "/api/cluster/nodes/"+node.String()+"/models/action", body)
		},
	}
	cmd.Flags().StringVar(&source, "source", "opencsg", "artifact registry shown by the node")
	return cmd
}

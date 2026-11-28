package cli

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/spf13/cobra"
)

const deploymentsPath = "/api/cluster/deployments"

func newClusterDeploymentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deployments",
		Short: "Manage deployments on the cluster controller",
	}
	list := localAction("list", "List deployments and assignments", http.MethodGet, deploymentsPath+"?include_deleted=false", nil)
	cmd.AddCommand(list, newDeploymentCreateCmd(),
		deploymentAction("start", "running"), deploymentAction("stop", "stopped"), deploymentAction("delete", "deleted"))
	return cmd
}

func newDeploymentCreateCmd() *cobra.Command {
	var spec cluster.DeploymentSpec
	cmd := &cobra.Command{
		Use:     "create NAME REPOSITORY",
		Example: "  csghub-lite cluster deployments create lab-chat Qwen/Qwen3-0.6B-GGUF --node auto",
		Short:   "Create a deployment on an eligible or specified node",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			spec.Name = args[0]
			spec.Repository = args[1]
			return printLocalResponse(cmd, http.MethodPost, deploymentsPath, spec)
		},
	}
	cmd.Flags().StringVar(&spec.NodeUUID, "node", "auto", "target node UUID or auto")
	cmd.Flags().StringVar(&spec.ArtifactSource, "source", "opencsg", "registry: opencsg, huggingface or modelscope")
	cmd.Flags().StringVar(&spec.Revision, "revision", "main", "repository revision")
	cmd.Flags().StringVar(&spec.Kind, "kind", "chat", "chat or embedding")
	cmd.Flags().StringVar(&spec.Quant, "quant", "", "GGUF quantization")
	cmd.Flags().StringVar(&spec.Backend, "backend", "", "inference backend (auto, llama or sglang)")
	cmd.Flags().IntVar(&spec.NumCtx, "num-ctx", 4096, "context length")
	cmd.Flags().IntVar(&spec.NumParallel, "num-parallel", 1, "parallel slots")
	cmd.Flags().IntVar(&spec.NGPULayers, "n-gpu-layers", -1, "GPU layers (-1 for automatic, 0 for CPU)")
	cmd.Flags().StringVar(&spec.KeepAlive, "keep-alive", "", "idle duration")
	return cmd
}

func deploymentAction(action, desired string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " ID",
		Short: fmt.Sprintf("Request deployment state %q", desired),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := uuid.Parse(args[0]); err != nil {
				return fmt.Errorf("invalid deployment ID: %w", err)
			}
			path := deploymentsPath + "/" + url.PathEscape(args[0])
			if action == "delete" {
				return printLocalResponse(cmd, http.MethodDelete, path, nil)
			}
			return printLocalResponse(cmd, http.MethodPut, path, map[string]string{"desired": desired})
		},
	}
}

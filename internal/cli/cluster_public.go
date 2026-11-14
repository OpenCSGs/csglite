package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

// Public provisioning extends config.json.cluster; membership and sharing stay
// in the existing cluster store, with one writer for each setting.
func newClusterPublicConfigureCmd() *cobra.Command {
	var role, listen, gateway, cert, key, ca string
	cmd := &cobra.Command{Use: "configure-public", Short: "Configure a public gateway or outbound worker (restart the server afterwards)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if role != "gateway" && role != "worker" && role != "lan" {
				return fmt.Errorf("--role must be gateway, worker or lan")
			}
			if role == "worker" {
				u, err := url.Parse(gateway)
				if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
					return fmt.Errorf("--gateway must be an HTTPS origin")
				}
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if role != "lan" && cfg.Cluster.Secret != "" {
				return fmt.Errorf("unset cluster_secret before configuring public mode")
			}
			store, err := cluster.OpenStore(filepath.Join(cfg.StorageDir(), "cluster"))
			if err != nil {
				return err
			}
			if c := store.Cluster(); c != nil && (c.Topology == "public") != (role != "lan") {
				return fmt.Errorf("leave the existing cluster before changing its topology")
			}
			if store.InCluster() && role != cfg.Cluster.Mode && !(role == "lan" && cfg.Cluster.Mode == "") {
				return fmt.Errorf("leave the existing cluster before changing its role")
			}
			for _, value := range []*string{&cert, &key, &ca} {
				if *value != "" {
					absolute, err := filepath.Abs(*value)
					if err != nil {
						return err
					}
					*value = absolute
				}
			}
			cfg.Cluster.Mode = role
			if role != "lan" && (cfg.ListenAddr == "" || cfg.ListenAddr == config.DefaultListenAddr) {
				cfg.ListenAddr = "127.0.0.1:11435"
			}
			cfg.Cluster.PublicListenAddr = listen
			cfg.Cluster.GatewayURL = strings.TrimRight(gateway, "/")
			cfg.Cluster.TLSCertFile = cert
			cfg.Cluster.TLSKeyFile = key
			cfg.Cluster.TLSCAFile = ca
			if err := config.Save(cfg); err != nil {
				return err
			}
			cmd.Printf("Configured cluster role %s. Restart csghub-lite to apply.\n", role)
			return nil
		}}
	cmd.Flags().StringVar(&role, "role", "", "gateway, worker or lan")
	cmd.Flags().StringVar(&listen, "listen", "127.0.0.1:11439", "public listener; non-loopback requires --cert-file and --key-file")
	cmd.Flags().StringVar(&gateway, "gateway", "", "worker's public HTTPS gateway URL")
	cmd.Flags().StringVar(&cert, "cert-file", "", "gateway TLS certificate file")
	cmd.Flags().StringVar(&key, "key-file", "", "gateway TLS private key file")
	cmd.Flags().StringVar(&ca, "ca-file", "", "trusted private CA file for the worker")
	return cmd
}

func newClusterShareCmd(remove bool) *cobra.Command {
	name, description := "share", "Allow a local model to serve requests from the public gateway"
	if remove {
		name, description = "unshare", "Stop sharing a local model with the public gateway"
	}
	return &cobra.Command{Use: name + " <model>", Short: description, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := newClusterClient()
		if err != nil {
			return err
		}
		var view cluster.ClusterView
		if err = c.do(http.MethodGet, "/api/cluster", nil, &view); err != nil {
			return err
		}
		if view.Mode != "worker" {
			return fmt.Errorf("model sharing is configured on a public worker")
		}
		model := strings.TrimSpace(args[0])
		if model == "" {
			return fmt.Errorf("model is required")
		}
		models := make([]string, 0, len(view.Settings.SharedModels)+1)
		for _, id := range view.Settings.SharedModels {
			if id != model {
				models = append(models, id)
			}
		}
		if !remove {
			models = append(models, model)
		}
		return c.do(http.MethodPut, "/api/cluster/settings", map[string]any{"shared_models": models}, nil)
	}}
}

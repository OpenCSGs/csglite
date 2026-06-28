package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencsgs/csglite/internal/cloud"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

type configKeySpec struct {
	name        string
	description string
}

var configKeySpecs = []configKeySpec{
	{
		name:        "server_url",
		description: "CSGHub server URL for model marketplace (default: " + config.DefaultServerURL + ")",
	},
	{
		name:        "ai_gateway_url",
		description: "AI Gateway URL for cloud inference models (default: " + cloud.DefaultBaseURL + ")",
	},
	{
		name:        "storage_dir",
		description: "Root storage directory (default: ~/.csghub-lite; sets both model_dir and dataset_dir)",
	},
	{
		name:        "model_dir",
		description: "Directory for downloaded models (default: ~/.csghub-lite/models)",
	},
	{
		name:        "dataset_dir",
		description: "Directory for downloaded datasets (default: ~/.csghub-lite/datasets)",
	},
	{
		name:        "listen_addr",
		description: "Local server listen address (default: " + config.DefaultListenAddr + ")",
	},
	{
		name:        "token",
		description: "Access token for CSGHub authentication (default: not set)",
	},
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage csghub-lite configuration",
		Long: strings.Join([]string{
			"Manage csghub-lite configuration.",
			"Configurable keys:\n" + configKeysHelpText(),
			"Examples:\n" +
				"  csghub-lite config show\n" +
				"  csghub-lite config get server_url\n" +
				"  csghub-lite config unset ai_gateway_url\n" +
				"  csghub-lite config set storage_dir /data/csghub-lite\n" +
				"  csghub-lite config set listen_addr :8080",
		}, "\n\n"),
	}

	cmd.AddCommand(newConfigSetCmd(), newConfigUnsetCmd(), newConfigGetCmd(), newConfigShowCmd())
	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Set a configuration value",
		Long: strings.Join([]string{
			"Set a configuration value.",
			"Available keys:\n" + configKeysHelpText(),
			"Examples:\n" +
				"  csghub-lite config set server_url https://my-csghub.example.com\n" +
				"  csghub-lite config set ai_gateway_url https://my-gateway.example.com\n" +
				"  csghub-lite config set storage_dir /data/csghub-lite\n" +
				"  csghub-lite config set listen_addr :8080",
		}, "\n\n"),
		Args: cobra.ExactArgs(2),
		RunE: runConfigSet,
	}
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unset KEY",
		Short: "Unset a configuration value",
		Long: strings.Join([]string{
			"Unset a configuration value and fall back to its default.",
			"Available keys:\n" + configKeysHelpText(),
			"Examples:\n" +
				"  csghub-lite config unset server_url\n" +
				"  csghub-lite config unset ai_gateway_url",
		}, "\n\n"),
		Args: cobra.ExactArgs(1),
		RunE: runConfigUnset,
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get KEY",
		Short: "Get a configuration value",
		Long: strings.Join([]string{
			"Get a configuration value.",
			"Available keys:\n" + configKeysHelpText(),
			"Examples:\n" +
				"  csghub-lite config get server_url\n" +
				"  csghub-lite config get ai_gateway_url",
		}, "\n\n"),
		Args: cobra.ExactArgs(1),
		RunE: runConfigGet,
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show all configuration",
		RunE:  runConfigShow,
	}
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	key, value := strings.TrimSpace(args[0]), args[1]
	syncToken := false
	switch key {
	case "server_url":
		serverURL := strings.TrimSpace(value)
		if serverURL != strings.TrimSpace(cfg.ServerURL) && strings.TrimSpace(cfg.Token) != "" {
			cfg.Token = ""
			syncToken = true
		}
		cfg.ServerURL = serverURL
	case "ai_gateway_url":
		cfg.AIGatewayURL = strings.TrimSpace(value)
	case "storage_dir":
		dir, err := requiredPathValue(value)
		if err != nil {
			return fmt.Errorf("invalid storage_dir: %w", err)
		}
		cfg.ModelDir = config.ModelDirForStorage(dir)
		cfg.DatasetDir = config.DatasetDirForStorage(dir)
		if err := ensureDir(cfg.ModelDir); err != nil {
			return fmt.Errorf("creating model directory: %w", err)
		}
		if err := ensureDir(cfg.DatasetDir); err != nil {
			return fmt.Errorf("creating dataset directory: %w", err)
		}
	case "model_dir":
		dir, err := requiredPathValue(value)
		if err != nil {
			return fmt.Errorf("invalid model_dir: %w", err)
		}
		cfg.ModelDir = dir
		if err := ensureDir(cfg.ModelDir); err != nil {
			return fmt.Errorf("creating model directory: %w", err)
		}
	case "dataset_dir":
		dir, err := requiredPathValue(value)
		if err != nil {
			return fmt.Errorf("invalid dataset_dir: %w", err)
		}
		cfg.DatasetDir = dir
		if err := ensureDir(cfg.DatasetDir); err != nil {
			return fmt.Errorf("creating dataset directory: %w", err)
		}
	case "listen_addr":
		cfg.ListenAddr = strings.TrimSpace(value)
	case "token":
		cfg.Token = strings.TrimSpace(value)
	default:
		return fmt.Errorf("unknown config key %q (valid: %s)", key, supportedConfigKeys())
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	if key == "token" || syncToken {
		warnIfTokenSyncFailed(cfg)
	}

	fmt.Printf("Set %s = %s\n", key, displayConfigValue(cfg, key))
	return nil
}

func runConfigUnset(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	key := strings.TrimSpace(args[0])
	syncToken := false
	switch key {
	case "server_url":
		if strings.TrimSpace(cfg.ServerURL) != config.DefaultServerURL && strings.TrimSpace(cfg.Token) != "" {
			cfg.Token = ""
			syncToken = true
		}
		cfg.ServerURL = config.DefaultServerURL
	case "ai_gateway_url":
		cfg.AIGatewayURL = ""
	default:
		return fmt.Errorf("config key %q cannot be unset (valid: server_url, ai_gateway_url)", key)
	}

	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	if syncToken {
		warnIfTokenSyncFailed(cfg)
	}

	fmt.Printf("Unset %s; using %s\n", key, displayConfigValue(cfg, key))
	return nil
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	key := args[0]
	switch key {
	case "server_url":
		fmt.Println(cfg.ServerURL)
	case "ai_gateway_url":
		fmt.Println(effectiveAIGatewayURL(cfg))
	case "storage_dir":
		fmt.Println(cfg.StorageDir())
	case "model_dir":
		fmt.Println(cfg.ModelDir)
	case "dataset_dir":
		fmt.Println(cfg.DatasetDir)
	case "listen_addr":
		fmt.Println(cfg.ListenAddr)
	case "token":
		fmt.Println(maskedToken(cfg.Token))
	default:
		return fmt.Errorf("unknown config key %q (valid: %s)", key, supportedConfigKeys())
	}
	return nil
}

func runConfigShow(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	fmt.Printf("server_url:      %s\n", cfg.ServerURL)
	fmt.Printf("ai_gateway_url:  %s\n", effectiveAIGatewayURL(cfg))
	fmt.Printf("storage_dir:     %s\n", cfg.StorageDir())
	fmt.Printf("model_dir:       %s\n", cfg.ModelDir)
	fmt.Printf("dataset_dir:     %s\n", cfg.DatasetDir)
	fmt.Printf("listen_addr:     %s\n", cfg.ListenAddr)
	fmt.Printf("token:           %s\n", maskedToken(cfg.Token))
	return nil
}

func requiredPathValue(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("path cannot be empty")
	}
	return filepath.Clean(trimmed), nil
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func maskedToken(token string) string {
	if token == "" {
		return "(not set)"
	}
	if len(token) <= 4 {
		return token + "****"
	}
	return token[:4] + "****"
}

func supportedConfigKeys() string {
	names := make([]string, 0, len(configKeySpecs))
	for _, spec := range configKeySpecs {
		names = append(names, spec.name)
	}
	return strings.Join(names, ", ")
}

func configKeysHelpText() string {
	var builder strings.Builder
	for i, spec := range configKeySpecs {
		if i > 0 {
			builder.WriteByte('\n')
		}
		fmt.Fprintf(&builder, "  %-15s %s", spec.name, spec.description)
	}
	return builder.String()
}

func effectiveAIGatewayURL(cfg *config.Config) string {
	if strings.TrimSpace(cfg.AIGatewayURL) == "" {
		return cloud.DefaultBaseURL
	}
	return strings.TrimSpace(cfg.AIGatewayURL)
}

func displayConfigValue(cfg *config.Config, key string) string {
	switch key {
	case "storage_dir":
		return cfg.StorageDir()
	case "model_dir":
		return cfg.ModelDir
	case "dataset_dir":
		return cfg.DatasetDir
	case "listen_addr":
		return cfg.ListenAddr
	case "server_url":
		return cfg.ServerURL
	case "ai_gateway_url":
		return effectiveAIGatewayURL(cfg)
	case "token":
		return maskedToken(cfg.Token)
	default:
		return ""
	}
}

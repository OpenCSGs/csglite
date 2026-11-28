package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

const servicesPath = "/api/provider-pools"

func newGatewayCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "gateway", Short: "Publish model services and query API usage"}
	cmd.AddCommand(localAction("services", "List model services and their management IDs", http.MethodGet, servicesPath, nil),
		newPublishServiceCmd(), newServiceSwitchCmd("enable", true), newServiceSwitchCmd("unpublish", false), newGatewayUsageCmd())
	return cmd
}

func newPublishServiceCmd() *cobra.Command {
	var req api.ProviderPoolCreateRequest
	var members []string
	cmd := &cobra.Command{
		Use: "publish SERVICE", Short: "Create and enable a model service",
		Example: "  csghub-lite gateway publish lab-chat --member local=org/chat-model",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Model = args[0]
			if !cmd.Flags().Changed("name") {
				req.Name = req.Model
			}
			req.Members = nil
			for i, binding := range members {
				source, model, ok := strings.Cut(binding, "=")
				if !ok || strings.TrimSpace(source) == "" || strings.TrimSpace(model) == "" {
					return fmt.Errorf("expected --member SOURCE=MODEL, got %q", binding)
				}
				req.Members = append(req.Members, api.ProviderPoolMember{ID: uuid.NewString(), Source: source, Model: model, Priority: i, Weight: 100})
			}
			return printLocalResponse(cmd, http.MethodPost, servicesPath, req)
		},
	}
	cmd.Flags().StringArrayVar(&members, "member", nil, "source=model binding, repeat for multiple members")
	_ = cmd.MarkFlagRequired("member")
	cmd.Flags().StringVar(&req.Name, "name", "", "display name (defaults to the service ID)")
	cmd.Flags().StringVar(&req.Category, "category", "chat", "chat, embedding, asr, tts, image or decision")
	cmd.Flags().StringVar(&req.Policy, "policy", "failover", "routing policy: fixed, failover or weighted")
	return cmd
}

func newServiceSwitchCmd(action string, enabled bool) *cobra.Command {
	return &cobra.Command{Use: action + " ID", Short: action + " a model service by management ID", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := pathID(args[0])
			if err != nil {
				return err
			}
			return printLocalResponse(cmd, http.MethodPut, servicesPath+"/"+id, api.ProviderPoolUpdateRequest{Enabled: &enabled})
		},
	}
}

func newGatewayUsageCmd() *cobra.Command {
	var period, provider, service string
	cmd := &cobra.Command{Use: "usage", Short: "Show rolling API usage by provider or model service", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch period {
			case "week", "month", "year":
			default:
				return fmt.Errorf("--period must be week, month or year")
			}
			q := url.Values{"period": {period}}
			if provider != "" {
				q.Set("provider", provider)
			}
			if service != "" {
				q.Set("pool", service)
			}
			return printLocalResponse(cmd, http.MethodGet, "/api/api-usage?"+q.Encode(), nil)
		},
	}
	cmd.Flags().StringVar(&period, "period", "week", "rolling window: week, month or year")
	cmd.Flags().StringVar(&provider, "provider", "", "filter by provider name")
	cmd.Flags().StringVar(&service, "service", "", "filter by service ID, name or public model")
	return cmd
}

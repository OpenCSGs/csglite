package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/opencsgs/csglite/ee/cluster"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/spf13/cobra"
)

// newClusterCmd manages the LAN compute cluster through the local server's
// API. Every subcommand needs a running csghub-lite on this machine, because
// pairing and discovery live in the server process.
func newClusterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Discover, pair and manage CSGLite nodes on the local network",
		Long: strings.Join([]string{
			"Manage the LAN compute cluster this node belongs to.",
			"",
			"Nodes on the same network discover each other automatically. One node",
			"creates the cluster and hands out a join token; every other machine joins",
			"with that token (or is invited with the admission code it shows). Members",
			"recognise each other by certificate, so an IP change after a reboot needs",
			"no action. Requests for a model this node lacks are routed to a member",
			"that holds it.",
			"",
			"A plain single machine keeps all of this off: no listener, no multicast,",
			"no polling. Networking starts when the machine is given a cluster secret",
			"or token, or when one of these commands pairs it.",
			"",
			"The Community edition may pair two nodes; an Enterprise license lifts the cap.",
			"",
			"Examples:",
			"  csghub-lite cluster create --name lab",
			"  csghub-lite cluster join csgl1-<cluster-uuid>-<secret>",
			"  csghub-lite cluster status",
			"  csghub-lite cluster code            # show this node's admission code",
			"  csghub-lite cluster invite <uuid> <code>",
			"  csghub-lite cluster models",
			"  csghub-lite cluster sync Qwen3-8B-GGUF --all",
			"  csghub-lite cluster drain           # stop taking new work on this node",
			"  csghub-lite cluster leave",
		}, "\n"),
	}
	cmd.AddCommand(
		newClusterStatusCmd(),
		newClusterCreateCmd(),
		newClusterJoinCmd(),
		newClusterLeaveCmd(),
		newClusterTokenCmd(),
		newClusterCodeCmd(),
		newClusterNodesCmd(),
		newClusterDiscoveredCmd(),
		newClusterInviteCmd(),
		newClusterRemoveCmd(),
		newClusterModelsCmd(),
		newClusterSyncCmd(),
		newClusterExplainCmd(),
		newClusterToggleCmd("enable", "/api/cluster/enable", "Switch cluster networking on (listener and discovery) without joining yet"),
		newClusterToggleCmd("disable", "/api/cluster/disable", "Switch cluster networking off; the node goes dormant (leave first if it is a member)"),
		newClusterStateCmd("drain", cluster.NodeStateDrain, "Finish in-flight requests on this node and take no new ones"),
		newClusterStateCmd("activate", cluster.NodeStateActive, "Return this node to active duty"),
		newClusterStateCmd("maintenance", cluster.NodeStateMaintenance, "Take this node out of routing and model sync"),
	)
	return cmd
}

type clusterClient struct {
	baseURL string
}

func newClusterClient() (*clusterClient, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	base := serverBaseURL(cfg)
	if !serverHealthy(base) {
		return nil, fmt.Errorf("csghub-lite is not running at %s; start it with 'csghub-lite serve' first", base)
	}
	return &clusterClient{baseURL: base}, nil
}

// clusterAPIError carries the cluster error envelope, including the license
// cap fields, so the CLI can print an actionable message.
type clusterAPIError struct {
	Status  int
	Message string
	Code    string
	Limit   int
	Current int
}

func (e *clusterAPIError) Error() string {
	if e.Code == "feature_not_licensed" {
		return fmt.Sprintf("%s\nThe Community edition supports up to %d nodes (currently %d). Import a CSGLite Enterprise license with 'csghub-lite license install' to add more.", e.Message, e.Limit, e.Current)
	}
	if e.Code == "cluster_disabled" {
		return e.Message
	}
	return e.Message
}

func (c *clusterClient) do(method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("contacting server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		apiErr := &clusterAPIError{Status: resp.StatusCode}
		var envelope struct {
			Error   json.RawMessage `json:"error"`
			Code    string          `json:"code"`
			Limit   int             `json:"limit"`
			Current int             `json:"current"`
		}
		if json.Unmarshal(data, &envelope) == nil {
			apiErr.Code, apiErr.Limit, apiErr.Current = envelope.Code, envelope.Limit, envelope.Current
			var msg string
			if json.Unmarshal(envelope.Error, &msg) != nil {
				var nested struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(envelope.Error, &nested) == nil {
					msg = nested.Message
				}
			}
			apiErr.Message = msg
		}
		if apiErr.Message == "" {
			apiErr.Message = fmt.Sprintf("server returned %s", resp.Status)
		}
		return apiErr
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *clusterClient) view() (cluster.ClusterView, error) {
	var v cluster.ClusterView
	err := c.do(http.MethodGet, "/api/cluster", nil, &v)
	return v, err
}

func newClusterStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show this node, the cluster and every member",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			v, err := c.view()
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(v)
			}
			printClusterView(v)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON view")
	return cmd
}

func printClusterView(v cluster.ClusterView) {
	fmt.Printf("Node:     %s (%s)\n", v.Node.Name, v.Node.UUID)
	fmt.Printf("Version:  %s   cluster port %d   api port %d\n", v.Node.Version, v.Node.ClusterPort, v.Node.APIPort)
	if v.Cluster == nil {
		if !v.Active {
			fmt.Println("Cluster:  none; cluster networking is off (dormant). 'cluster create', 'cluster join <token>' or 'cluster enable' switches it on.")
			return
		}
		fmt.Println("Cluster:  none (create one with 'cluster create' or join with 'cluster join <token>')")
		if v.DiscoveredCount > 0 {
			fmt.Printf("Seen on the network: %d unpaired node(s); run 'cluster discovered'\n", v.DiscoveredCount)
		}
		return
	}
	limit := "unlimited"
	if v.NodeLimit > 0 {
		limit = fmt.Sprintf("%d", v.NodeLimit)
	}
	edition := "Community"
	if v.Licensed {
		edition = "Enterprise"
	}
	fmt.Printf("Cluster:  %s (%s)\n", v.Cluster.Name, v.Cluster.UUID)
	fmt.Printf("Nodes:    %d / %s (%s edition)\n", len(v.Members), limit, edition)
	fmt.Printf("Routing:  %s, prefer local %v, state %s\n", v.Settings.RoutingMode, v.Settings.PreferLocal, v.Settings.State)
	if v.ModelSourceMixed {
		fmt.Println("Warning:  members use different model sources; model ids may not mean the same files")
	}
	fmt.Println()
	printClusterMembers(v.Members)
}

func printClusterMembers(members []cluster.NodeView) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tHEALTH\tSTATE\tADDRESS\tGPU\tVRAM\tLOADED\tINFLIGHT\tVERSION\tUUID")
	for _, m := range members {
		name := m.Name
		if m.Local {
			name += " (this node)"
		}
		state, gpu, vram, loaded, inflight, version := "-", "-", "-", "-", "-", "-"
		if st := m.Status; st != nil {
			state = string(st.State)
			if !st.Licensed {
				state += " (unlicensed)"
			}
			if len(st.GPUs) > 0 {
				gpu = st.GPUs[0].Name
				if len(st.GPUs) > 1 {
					gpu = fmt.Sprintf("%s x%d", gpu, len(st.GPUs))
				}
			}
			if total := st.VRAMTotal(); total > 0 {
				vram = fmt.Sprintf("%.1f/%.1f GB", float64(total-st.VRAMFree())/(1<<30), float64(total)/(1<<30))
			}
			n := 0
			for _, ms := range st.Models {
				if ms.Loaded || ms.Loading {
					n++
				}
			}
			loaded = fmt.Sprintf("%d/%d", n, len(st.Models))
			inflight = fmt.Sprintf("%d", st.Inflight)
			version = st.Version
		}
		health := string(m.Health)
		if m.LastError != "" && !m.Online {
			health += " (" + truncateText(m.LastError, 40) + ")"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, health, state, m.Addr, gpu, vram, loaded, inflight, version, m.UUID)
	}
	_ = w.Flush()
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func newClusterCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Found a new cluster on this node and print the join token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var out struct {
				Cluster   cluster.ClusterInfo `json:"cluster"`
				JoinToken string              `json:"join_token"`
			}
			if err := c.do(http.MethodPost, "/api/cluster", map[string]string{"name": name}, &out); err != nil {
				return err
			}
			fmt.Printf("Created cluster %q (%s).\n\n", out.Cluster.Name, out.Cluster.UUID)
			fmt.Printf("Join token (shown once; rotate with 'cluster token --rotate'):\n  %s\n\n", out.JoinToken)
			fmt.Println("On each other machine run:")
			fmt.Printf("  csghub-lite cluster join %s\n", out.JoinToken)
			fmt.Printf("or set %s before its first start.\n", "CSGHUB_LITE_CLUSTER_JOIN_TOKEN")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "display name for the cluster")
	return cmd
}

func newClusterJoinCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "join <token>",
		Short: "Join the cluster named by a join token",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var v cluster.ClusterView
			if err := c.do(http.MethodPost, "/api/cluster/join", map[string]string{"token": strings.TrimSpace(args[0]), "address": address}, &v); err != nil {
				return err
			}
			if v.Cluster != nil {
				fmt.Printf("Joined cluster %q (%s) with %d member(s).\n", v.Cluster.Name, v.Cluster.UUID, len(v.Members))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "cluster endpoint (host or host:port) of a member when it cannot be discovered")
	return cmd
}

func newClusterLeaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "leave",
		Short: "Leave the cluster (the node identity is kept)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			if err := c.do(http.MethodDelete, "/api/cluster", nil, nil); err != nil {
				return err
			}
			fmt.Println("Left the cluster.")
			return nil
		},
	}
}

func newClusterTokenCmd() *cobra.Command {
	var rotate bool
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Show the join token, or mint a new one with --rotate",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var out struct {
				Token     string `json:"token"`
				Available bool   `json:"available"`
			}
			method, path := http.MethodGet, "/api/cluster/token"
			if rotate {
				method, path = http.MethodPost, "/api/cluster/token/rotate"
			}
			if err := c.do(method, path, nil, &out); err != nil {
				return err
			}
			if !out.Available {
				fmt.Println("The join token is only shown in the server process that minted it. Run 'cluster token --rotate' to mint a new one; paired members are unaffected.")
				return nil
			}
			fmt.Println(out.Token)
			return nil
		},
	}
	cmd.Flags().BoolVar(&rotate, "rotate", false, "mint a new join token and invalidate the old one")
	return cmd
}

func newClusterCodeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "code",
		Short: "Show this unpaired node's admission code for 'cluster invite' on a member",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var out struct {
				Code      string    `json:"code"`
				ExpiresAt time.Time `json:"expires_at"`
			}
			if err := c.do(http.MethodGet, "/api/cluster/code", nil, &out); err != nil {
				return err
			}
			fmt.Printf("Admission code: %s (valid until %s, single use)\n", out.Code, out.ExpiresAt.Local().Format("15:04:05"))
			fmt.Println("On a cluster member run: csghub-lite cluster invite <this node's uuid> " + out.Code)
			return nil
		},
	}
}

func newClusterNodesCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List cluster members",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			v, err := c.view()
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(v.Members)
			}
			printClusterMembers(v.Members)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON list")
	return cmd
}

func newClusterDiscoveredCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "discovered",
		Short: "List unpaired nodes seen on the network",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var out struct {
				Nodes []cluster.DiscoveredView `json:"nodes"`
			}
			if err := c.do(http.MethodGet, "/api/cluster/discovered", nil, &out); err != nil {
				return err
			}
			if asJSON {
				return printJSON(out.Nodes)
			}
			if len(out.Nodes) == 0 {
				fmt.Println("No unpaired nodes seen. Nodes announce themselves over multicast DNS; on networks without multicast set CSGHUB_LITE_CLUSTER_SEEDS or pass --address to 'cluster invite'.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tADDRESS\tVERSION\tCLUSTER\tSEEN\tUUID")
			for _, n := range out.Nodes {
				clusterCol := "-"
				if n.ClusterUUID != "" {
					clusterCol = "other cluster"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s ago\t%s\n", n.Name, n.Addr, n.Version, clusterCol, time.Since(n.Seen).Round(time.Second), n.UUID)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON list")
	return cmd
}

func newClusterInviteCmd() *cobra.Command {
	var address string
	cmd := &cobra.Command{
		Use:   "invite <node-uuid> <code>",
		Short: "Invite a discovered node using the admission code shown on it",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var out cluster.NodeView
			if err := c.do(http.MethodPost, "/api/cluster/invite", map[string]string{"uuid": args[0], "code": args[1], "address": address}, &out); err != nil {
				return err
			}
			fmt.Printf("Invited %s (%s).\n", out.Name, out.UUID)
			return nil
		},
	}
	cmd.Flags().StringVar(&address, "address", "", "cluster endpoint (host or host:port) when the node was not discovered")
	return cmd
}

func newClusterRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <node-uuid|name>",
		Short: "Remove a member from the cluster",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			id, err := c.resolveNode(args[0])
			if err != nil {
				return err
			}
			if err := c.do(http.MethodDelete, "/api/cluster/nodes/"+id, nil, nil); err != nil {
				return err
			}
			fmt.Printf("Removed %s.\n", args[0])
			return nil
		},
	}
}

// resolveNode accepts a uuid, a uuid prefix or a unique member name.
func (c *clusterClient) resolveNode(ref string) (string, error) {
	v, err := c.view()
	if err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	var matches []string
	for _, m := range v.Members {
		if m.UUID == ref {
			return m.UUID, nil
		}
		if strings.HasPrefix(m.UUID, ref) || strings.EqualFold(m.Name, ref) {
			matches = append(matches, m.UUID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no member matches %q", ref)
	default:
		return "", fmt.Errorf("%q matches %d members; use the full uuid", ref, len(matches))
	}
}

func newClusterModelsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models",
		Short: "Show which nodes hold which models",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var out struct {
				Models []cluster.ClusterModel `json:"models"`
			}
			if err := c.do(http.MethodGet, "/api/cluster/models", nil, &out); err != nil {
				return err
			}
			if asJSON {
				return printJSON(out.Models)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "MODEL\tSIZE\tNODES (● loaded, ○ present, ✕ offline)")
			for _, m := range out.Models {
				var cells []string
				for _, n := range m.Nodes {
					mark := "○"
					switch {
					case !n.Online:
						mark = "✕"
					case n.Loaded:
						mark = "●"
					}
					cells = append(cells, mark+" "+n.Name)
				}
				fmt.Fprintf(w, "%s\t%.1f GB\t%s\n", m.ID, float64(m.Size)/(1<<30), strings.Join(cells, "  "))
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON list")
	return cmd
}

func newClusterSyncCmd() *cobra.Command {
	var all bool
	var nodes []string
	cmd := &cobra.Command{
		Use:   "sync <model>",
		Short: "Have members download a model from their own model source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			body := map[string]any{"model": args[0]}
			switch {
			case all || len(nodes) == 0:
				body["nodes"] = "all"
			default:
				var ids []string
				for _, n := range nodes {
					id, err := c.resolveNode(n)
					if err != nil {
						return err
					}
					ids = append(ids, id)
				}
				body["nodes"] = ids
			}
			var out struct {
				Model   string               `json:"model"`
				Results []cluster.SyncResult `json:"results"`
			}
			if err := c.do(http.MethodPost, "/api/cluster/models/sync", body, &out); err != nil {
				return err
			}
			for _, r := range out.Results {
				switch {
				case r.Skipped != "":
					fmt.Printf("%s: skipped (%s)\n", r.NodeName, r.Skipped)
				case r.Error != "":
					fmt.Printf("%s: error: %s\n", r.NodeName, r.Error)
				default:
					fmt.Printf("%s: download started\n", r.NodeName)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "sync to every member (default when no --node is given)")
	cmd.Flags().StringArrayVar(&nodes, "node", nil, "member uuid or name to sync to (repeatable)")
	return cmd
}

func newClusterExplainCmd() *cobra.Command {
	var promptTokens, maxTokens int
	cmd := &cobra.Command{
		Use:   "explain <model>",
		Short: "Show how the scheduler would place a request for a model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var ex cluster.Explain
			path := fmt.Sprintf("/api/cluster/explain?model=%s&prompt_tokens=%d&max_tokens=%d", urlQueryEscape(args[0]), promptTokens, maxTokens)
			if err := c.do(http.MethodGet, path, nil, &ex); err != nil {
				return err
			}
			for _, cand := range ex.Candidates {
				name := cand.Name
				if cand.Local {
					name += " (this node)"
				}
				if !cand.Eligible {
					fmt.Printf("  -  %-24s excluded: %s\n", name, cand.Excluded)
					continue
				}
				extra := ""
				if cand.Affinity {
					extra = " [affinity]"
				}
				fmt.Printf("%3d. %-24s ~%.1fs%s\n", cand.Rank, name, cand.Seconds, extra)
				for _, f := range cand.Factors {
					fmt.Printf("       - %s\n", f)
				}
			}
			if len(ex.Order) == 0 {
				fmt.Println("No node can serve this model right now.")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&promptTokens, "prompt-tokens", 512, "assumed prompt length")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 256, "assumed completion length")
	return cmd
}

func newClusterToggleCmd(use, path, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			var v cluster.ClusterView
			if err := c.do(http.MethodPost, path, nil, &v); err != nil {
				return err
			}
			if v.Active {
				fmt.Printf("Cluster networking is on (listening on port %d).\n", v.Node.ClusterPort)
			} else {
				fmt.Println("Cluster networking is off; this node is dormant.")
			}
			return nil
		},
	}
}

func newClusterStateCmd(use string, state cluster.NodeState, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClusterClient()
			if err != nil {
				return err
			}
			v, err := c.view()
			if err != nil {
				return err
			}
			var out cluster.Settings
			if err := c.do(http.MethodPost, "/api/cluster/nodes/"+v.Node.UUID+"/state", map[string]string{"state": string(state)}, &out); err != nil {
				return err
			}
			fmt.Printf("Node state is now %s.\n", out.State)
			return nil
		},
	}
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer("%", "%25", "&", "%26", "+", "%2B", " ", "%20", "#", "%23", "?", "%3F")
	return r.Replace(s)
}

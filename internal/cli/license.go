package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

// newLicenseCmd manages the enterprise license. Every subcommand talks to a
// running server when one is reachable so the change takes effect at once;
// otherwise it works on the license file directly through the same manager
// the server uses, and the server picks the file up when it next starts.
func newLicenseCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "Show, verify, install or remove the enterprise license",
		Long: strings.Join([]string{
			"Manage the CSGLite enterprise license.",
			"Licenses are issued by the CSGHub license issuer and delivered as a",
			"LICENSE KEY PEM file. CSGLite only verifies them; without a license it",
			"runs as the Community edition.",
			"",
			"Examples:",
			"  csghub-lite license show",
			"  csghub-lite license verify ./csglite.lic",
			"  csghub-lite license install ./csglite.lic",
			"  cat csglite.lic | csghub-lite license install -",
			"  csghub-lite license features",
			"  csghub-lite license remove",
		}, "\n"),
	}
	cmd.AddCommand(
		newLicenseShowCmd(version),
		newLicenseVerifyCmd(version),
		newLicenseInstallCmd(version),
		newLicenseRemoveCmd(version),
		newLicenseFeaturesCmd(version),
	)
	return cmd
}

type licenseClient struct {
	cfg     *config.Config
	baseURL string // empty when no server is reachable
	manager *license.Manager
	version string
}

func newLicenseClient(version string) (*licenseClient, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	c := &licenseClient{cfg: cfg, version: version}
	if base := serverBaseURL(cfg); serverHealthy(base) {
		c.baseURL = base
	}
	return c, nil
}

func (c *licenseClient) local() (*license.Manager, error) {
	if c.manager != nil {
		return c.manager, nil
	}
	root := c.cfg.StorageDir()
	if root == "" {
		defaultRoot, err := config.DefaultStorageDir()
		if err != nil {
			return nil, fmt.Errorf("resolving storage directory: %w", err)
		}
		root = defaultRoot
	}
	opts, err := license.DefaultOptions(root, c.version)
	if err != nil {
		return nil, err
	}
	c.manager = license.NewManager(opts)
	c.manager.Refresh()
	return c.manager, nil
}

func (c *licenseClient) do(method, path string, body any, out any) error {
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
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("contacting server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Error != "" {
			return fmt.Errorf("%s", apiErr.Error)
		}
		return fmt.Errorf("server returned %s", resp.Status)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *licenseClient) state() (api.LicenseState, error) {
	if c.baseURL != "" {
		var st api.LicenseState
		err := c.do(http.MethodGet, "/api/license", nil, &st)
		return st, err
	}
	m, err := c.local()
	if err != nil {
		return api.LicenseState{}, err
	}
	return stateToAPI(m, m.State()), nil
}

func stateToAPI(m *license.Manager, st license.State) api.LicenseState {
	limits := make(map[string]int, len(st.Limits))
	for k, v := range st.Limits {
		limits[k] = v
	}
	out := api.LicenseState{
		Status:    string(st.Status),
		Edition:   st.Edition(),
		Features:  st.EnabledKeys(),
		Limits:    limits,
		Reason:    st.Reason,
		Warnings:  st.Warnings,
		Source:    st.Source,
		FilePath:  m.FilePath(),
		CheckedAt: st.CheckedAt,
	}
	if p := st.Payload; p != nil {
		out.License = &api.LicenseSummary{
			Key: p.Key, Company: p.Company, Email: p.Email, Product: p.Product, Edition: p.Edition,
			MaxUser: p.MaxUser, StartTime: p.StartTime, ExpireTime: p.ExpireTime, Version: p.Version,
		}
	}
	if !st.GraceUntil.IsZero() {
		g := st.GraceUntil
		out.GraceUntil = &g
	}
	return out
}

func readLicenseArg(arg string) (string, error) {
	var data []byte
	var err error
	if arg == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	} else {
		data, err = os.ReadFile(arg)
	}
	if err != nil {
		return "", fmt.Errorf("reading license: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "", fmt.Errorf("license is empty")
	}
	return text, nil
}

func newLicenseShowCmd(version string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the current license status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newLicenseClient(version)
			if err != nil {
				return err
			}
			st, err := c.state()
			if err != nil {
				return err
			}
			if asJSON {
				return printJSON(st)
			}
			printLicenseState(st, c.baseURL == "")
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON state")
	return cmd
}

func newLicenseVerifyCmd(version string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "verify <file|->",
		Short: "Verify a license file without installing it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readLicenseArg(args[0])
			if err != nil {
				return err
			}
			c, err := newLicenseClient(version)
			if err != nil {
				return err
			}
			var resp api.LicenseVerifyResponse
			if c.baseURL != "" {
				if err := c.do(http.MethodPost, "/api/license/verify", api.LicenseImportRequest{Data: data}, &resp); err != nil {
					return err
				}
			} else {
				m, err := c.local()
				if err != nil {
					return err
				}
				st := stateToAPI(m, m.Verify(data))
				resp = api.LicenseVerifyResponse{
					Valid:  st.Status == string(license.StatusValid) || st.Status == string(license.StatusGrace),
					Status: st.Status, License: st.License, Features: st.Features, Limits: st.Limits,
					Reason: st.Reason, Warnings: st.Warnings,
				}
			}
			if asJSON {
				return printJSON(resp)
			}
			printLicenseState(api.LicenseState{
				Status: resp.Status, License: resp.License, Features: resp.Features, Limits: resp.Limits,
				Reason: resp.Reason, Warnings: resp.Warnings,
				Edition: editionFor(resp),
			}, false)
			if !resp.Valid {
				return fmt.Errorf("license is not usable (status %s)", resp.Status)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON result")
	return cmd
}

func editionFor(resp api.LicenseVerifyResponse) string {
	if resp.Valid && resp.License != nil && resp.License.Edition != "" {
		return resp.License.Edition
	}
	return license.EditionCommunity
}

func newLicenseInstallCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <file|->",
		Short: "Install a license file (replaces any existing license)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readLicenseArg(args[0])
			if err != nil {
				return err
			}
			c, err := newLicenseClient(version)
			if err != nil {
				return err
			}
			var st api.LicenseState
			if c.baseURL != "" {
				if err := c.do(http.MethodPut, "/api/license", api.LicenseImportRequest{Data: data}, &st); err != nil {
					return err
				}
			} else {
				m, err := c.local()
				if err != nil {
					return err
				}
				installed, err := m.Install(data)
				if err != nil {
					return err
				}
				st = stateToAPI(m, installed)
			}
			fmt.Println("License installed.")
			printLicenseState(st, c.baseURL == "")
			return nil
		},
	}
	return cmd
}

func newLicenseRemoveCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Remove the installed license and return to the Community edition",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newLicenseClient(version)
			if err != nil {
				return err
			}
			if c.baseURL != "" {
				if err := c.do(http.MethodDelete, "/api/license", nil, nil); err != nil {
					return err
				}
			} else {
				m, err := c.local()
				if err != nil {
					return err
				}
				if _, err := m.Remove(); err != nil {
					return err
				}
			}
			fmt.Println("License removed. Running as the Community edition.")
			return nil
		},
	}
}

func newLicenseFeaturesCmd(version string) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "features",
		Short: "List license-gated features and whether each is enabled",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newLicenseClient(version)
			if err != nil {
				return err
			}
			var entries []api.LicenseFeatureDefinition
			if c.baseURL != "" {
				if err := c.do(http.MethodGet, "/api/license/features", nil, &entries); err != nil {
					return err
				}
			} else {
				m, err := c.local()
				if err != nil {
					return err
				}
				st := m.State()
				for _, def := range license.Catalog() {
					enabled := st.Licensed()
					if def.Type == license.FeatureTypeBoolean {
						enabled = st.Enabled(def)
					}
					entries = append(entries, api.LicenseFeatureDefinition{
						Key: def.Key, Type: string(def.Type), DefaultValue: def.DefaultValue,
						NavItem: def.NavItem, Since: def.Since, Enabled: enabled,
					})
				}
			}
			if asJSON {
				return printJSON(entries)
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "FEATURE\tTYPE\tENABLED\tDEFAULT\tSINCE")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%v\t%v\t%s\n", e.Key, e.Type, e.Enabled, e.DefaultValue, e.Since)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the raw JSON catalog")
	return cmd
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printLicenseState(st api.LicenseState, offline bool) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Edition:\t%s\n", st.Edition)
	fmt.Fprintf(w, "Status:\t%s\n", st.Status)
	if st.Reason != "" {
		fmt.Fprintf(w, "Reason:\t%s\n", st.Reason)
	}
	if l := st.License; l != nil {
		fmt.Fprintf(w, "Company:\t%s\n", l.Company)
		if l.Email != "" {
			fmt.Fprintf(w, "Email:\t%s\n", l.Email)
		}
		fmt.Fprintf(w, "Product:\t%s\n", l.Product)
		fmt.Fprintf(w, "License ID:\t%s\n", l.Key)
		fmt.Fprintf(w, "Seats:\t%d\n", l.MaxUser)
		fmt.Fprintf(w, "Valid from:\t%s\n", l.StartTime.Local().Format("2006-01-02"))
		fmt.Fprintf(w, "Expires:\t%s\n", l.ExpireTime.Local().Format("2006-01-02"))
		if st.GraceUntil != nil {
			fmt.Fprintf(w, "Grace until:\t%s\n", st.GraceUntil.Local().Format("2006-01-02"))
		}
		if l.Version != "" {
			fmt.Fprintf(w, "Min version:\t%s\n", l.Version)
		}
	}
	if st.FilePath != "" {
		fmt.Fprintf(w, "File:\t%s\n", st.FilePath)
	}
	if st.Source == "env" {
		fmt.Fprintf(w, "Source:\t%s\n", license.EnvLicense)
	}
	if len(st.Features) > 0 {
		features := append([]string(nil), st.Features...)
		sort.Strings(features)
		fmt.Fprintf(w, "Features:\t%s\n", strings.Join(features, ", "))
	}
	for _, warning := range st.Warnings {
		fmt.Fprintf(w, "Warning:\t%s\n", warning)
	}
	if offline {
		fmt.Fprintln(w, "Note:\tno running server detected; the file was read directly")
	}
	_ = w.Flush()
}

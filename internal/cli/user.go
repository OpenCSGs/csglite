package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/license"
	"github.com/opencsgs/csglite/internal/team"
	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

// newUserCmd manages user accounts from the host. Listing and resetting a
// password work on team.json directly, so an administrator who forgot their
// password, or whose license lapsed, can always get back in from the machine
// itself. Creating a user goes through the running server when there is one,
// so the license and seat cap are checked the same way as in the web UI.
func newUserCmd(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage user accounts (Enterprise)",
		Long: strings.Join([]string{
			"Manage the user accounts of a shared CSGLite instance.",
			"",
			"Accounts are an Enterprise feature. Once the first user exists, everyone",
			"who reaches this instance over the network must sign in or present an",
			"API key. Commands run on this machine keep administrator access.",
			"",
			"Examples:",
			"  csghub-lite user create admin --role admin",
			"  csghub-lite user create alice --role member --invite",
			"  csghub-lite user list",
			"  csghub-lite user reset-password admin",
			"  printf '%s\\n' \"$PASSWORD\" | csghub-lite user reset-password admin --password-stdin",
		}, "\n"),
	}
	cmd.AddCommand(newUserListCmd(), newUserCreateCmd(version), newUserResetPasswordCmd())
	return cmd
}

func teamStore() (*team.Store, error) {
	home, err := config.AppHome()
	if err != nil {
		return nil, fmt.Errorf("resolving the app home: %w", err)
	}
	return team.NewStore(home), nil
}

func newUserListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List user accounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := teamStore()
			if err != nil {
				return err
			}
			users, err := store.Users()
			if err != nil {
				return err
			}
			teams, err := store.Teams()
			if err != nil {
				return err
			}
			teamNames := map[string]string{}
			for _, group := range teams {
				teamNames[group.ID] = group.Name
			}
			out := cmd.OutOrStdout()
			if asJSON {
				type row struct {
					Username string    `json:"username"`
					Display  string    `json:"display_name,omitempty"`
					Role     team.Role `json:"role"`
					Team     string    `json:"team,omitempty"`
					Status   string    `json:"status"`
				}
				rows := make([]row, 0, len(users))
				for _, user := range users {
					rows = append(rows, row{user.Username, user.DisplayName, user.Role, teamNames[user.TeamID], userStatus(user)})
				}
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			if len(users) == 0 {
				fmt.Fprintln(out, "No user accounts. Create the first administrator with: csghub-lite user create <username> --role admin")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "USERNAME\tROLE\tTEAM\tSTATUS\tLAST SIGN-IN")
			for _, user := range users {
				last := "never"
				if !user.LastLoginAt.IsZero() {
					last = user.LastLoginAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", user.Username, user.Role, dashIfEmpty(teamNames[user.TeamID]), userStatus(user), last)
			}
			return tw.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return cmd
}

func userStatus(user team.User) string {
	switch {
	case user.Disabled:
		return "disabled"
	case user.Pending():
		return "invited"
	case user.MustChangePassword:
		return "must change password"
	}
	return "active"
}

func dashIfEmpty(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func newUserCreateCmd(version string) *cobra.Command {
	var role, displayName, email string
	var invite, passwordStdin bool
	cmd := &cobra.Command{
		Use:   "create <username>",
		Short: "Create a user account",
		Long: strings.Join([]string{
			"Create a user account. The first account is always an administrator and",
			"switches accounts on. A password you set for someone else must be changed",
			"at their first sign-in; with --invite the user sets their own through an",
			"invitation link instead.",
		}, "\n"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.TeamUserCreateRequest{
				Username: args[0], Role: role, DisplayName: displayName, Email: email, Invite: invite,
			}
			if !invite {
				password, err := readNewPassword(cmd, passwordStdin)
				if err != nil {
					return err
				}
				req.Password = password
			}
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			out := cmd.OutOrStdout()
			if base := serverBaseURL(cfg); serverHealthy(base) {
				var created api.TeamUser
				if err := postJSON(base+"/api/team/users", req, &created); err != nil {
					return err
				}
				fmt.Fprintf(out, "Created %s (%s).\n", created.Username, created.Role)
				if created.InviteToken != "" {
					fmt.Fprintf(out, "Send this invitation link (valid 7 days), with this instance's address in front:\n  /invite/%s\n", created.InviteToken)
				}
				return nil
			}
			created, inviteToken, err := createUserOffline(cfg, version, req)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Created %s (%s). The server picks the account up when it starts.\n", created.Username, created.Role)
			if inviteToken != "" {
				fmt.Fprintf(out, "Send this invitation link (valid 7 days), with this instance's address in front:\n  /invite/%s\n", inviteToken)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "member", "role: admin, auditor or member")
	cmd.Flags().StringVar(&displayName, "display-name", "", "name shown in the web UI")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	cmd.Flags().BoolVar(&invite, "invite", false, "create the user without a password and print an invitation link")
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from the first line of standard input")
	return cmd
}

// createUserOffline creates a user while no server is running. As in the
// web UI, the auditor role needs the license.
func createUserOffline(cfg *config.Config, version string, req api.TeamUserCreateRequest) (team.User, string, error) {
	store, err := teamStore()
	if err != nil {
		return team.User{}, "", err
	}
	if team.Role(req.Role) == team.RoleAuditor && store.Enabled() {
		root := cfg.StorageDir()
		if root == "" {
			defaultRoot, err := config.DefaultStorageDir()
			if err != nil {
				return team.User{}, "", fmt.Errorf("resolving storage directory: %w", err)
			}
			root = defaultRoot
		}
		opts, err := license.DefaultOptions(root, version)
		if err != nil {
			return team.User{}, "", err
		}
		if !license.NewManager(opts).Refresh().Enabled(license.FeatureTeamManagement) {
			return team.User{}, "", errors.New("the auditor role needs a CSGLite Enterprise license; install one with: csghub-lite license install <file>")
		}
	}
	return store.CreateUser(team.NewUser{
		Username:    req.Username,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Email:       req.Email,
		Role:        team.Role(req.Role),
		Invite:      req.Invite,
		SelfChosen:  !store.Enabled(),
	})
}

func newUserResetPasswordCmd() *cobra.Command {
	var passwordStdin, requireChange bool
	cmd := &cobra.Command{
		Use:   "reset-password <username>",
		Short: "Set a user's password from this machine",
		Long: strings.Join([]string{
			"Set a user's password directly in the account file on this machine. It works",
			"whether or not the server is running and whatever the license state, so it",
			"is the way back in when the last administrator forgot their password. The",
			"user is re-enabled and signed out everywhere.",
		}, "\n"),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			password, err := readNewPassword(cmd, passwordStdin)
			if err != nil {
				return err
			}
			store, err := teamStore()
			if err != nil {
				return err
			}
			user, err := store.ResetPassword(args[0], password, requireChange)
			if errors.Is(err, team.ErrNotFound) {
				return fmt.Errorf("no user named %q; list them with: csghub-lite user list", args[0])
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Password for %s (%s) is set.\n", user.Username, user.Role)
			return nil
		},
	}
	cmd.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from the first line of standard input")
	cmd.Flags().BoolVar(&requireChange, "require-change", false, "make the user choose a new password at next sign-in")
	return cmd
}

// readNewPassword reads a password from the first line of stdin, or prompts
// for it twice without echo.
func readNewPassword(cmd *cobra.Command, fromStdin bool) (string, error) {
	if fromStdin || !stdinIsTerminal() {
		line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		password := strings.TrimRight(line, "\r\n")
		if password == "" {
			return "", errors.New("no password on standard input")
		}
		return password, nil
	}
	errOut := cmd.ErrOrStderr()
	fmt.Fprint(errOut, "Password: ")
	first, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Fprintln(errOut)
	if err != nil {
		return "", err
	}
	fmt.Fprint(errOut, "Confirm password: ")
	second, err := term.ReadPassword(os.Stdin.Fd())
	fmt.Fprintln(errOut)
	if err != nil {
		return "", err
	}
	if !bytes.Equal(first, second) {
		return "", errors.New("the passwords do not match")
	}
	return string(first), nil
}

func postJSON(url string, body, out any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return json.Unmarshal(data, out)
}

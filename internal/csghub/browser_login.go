package csghub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// BrowserLoginToken turns a platform-verified SSO JWT into the same CSGHub
// access token the legacy lite callback uses. The JWT is never persisted.
func (c *Client) BrowserLoginToken(ctx context.Context) (string, error) {
	var me struct {
		Name string `json:"name"`
	}
	if err := c.getJSON(ctx, "/hf/api/whoami-v2", &me); err != nil || strings.TrimSpace(me.Name) == "" {
		return "", fmt.Errorf("platform did not verify the browser login")
	}
	var tokens APIResponse[[]TokenDetail]
	path := "/api/v1/user/" + url.PathEscape(me.Name) + "/tokens?app=csghub"
	if err := c.getJSON(ctx, path, &tokens); err != nil {
		return "", fmt.Errorf("read browser login credentials: %w", err)
	}
	for _, token := range tokens.Data {
		if token.Token != "" && (token.Application == "" || token.Application == "csghub") && (token.ExpireAt.IsZero() || token.ExpireAt.After(time.Now())) {
			return c.verifyBrowserLoginToken(ctx, token.Token, me.Name)
		}
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	var created APIResponse[struct {
		Token string `json:"token"`
	}]
	if err := c.postJSON(ctx, "/api/v1/token/csghub/csglite-"+hex.EncodeToString(random[:]), map[string]string{}, &created); err != nil {
		return "", fmt.Errorf("create browser login credential: %w", err)
	}
	if strings.TrimSpace(created.Data.Token) == "" {
		return "", fmt.Errorf("platform returned an empty login credential")
	}
	return c.verifyBrowserLoginToken(ctx, created.Data.Token, me.Name)
}

// Confirm that the reusable credential belongs to the verified SSO identity.
func (c *Client) verifyBrowserLoginToken(ctx context.Context, token, username string) (string, error) {
	client := NewClient(c.baseURL, token)
	client.httpClient = c.httpClient
	user, err := client.GetCurrentUser(ctx)
	if err != nil || user == nil || user.Username != username {
		return "", fmt.Errorf("browser login credential does not match the verified identity")
	}
	return token, nil
}

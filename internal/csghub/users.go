package csghub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// GetTokenDetail returns the owner details for a token value.
func (c *Client) GetTokenDetail(ctx context.Context, tokenValue string) (*TokenDetail, error) {
	tokenValue = strings.TrimSpace(tokenValue)
	if tokenValue == "" {
		return nil, fmt.Errorf("token value is empty")
	}

	path := "/api/v1/token/" + url.PathEscape(tokenValue)
	var resp APIResponse[TokenDetail]

	anon := NewClient(c.baseURL, "")
	anon.httpClient = c.httpClient
	if err := anon.getJSON(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("getting token detail: %w", err)
	}

	return &resp.Data, nil
}

// GetUser returns details for a specific user.
func (c *Client) GetUser(ctx context.Context, username string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("username is empty")
	}

	path := "/api/v1/user/" + url.PathEscape(username)
	var resp APIResponse[User]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return nil, fmt.Errorf("getting user %s: %w", username, err)
	}

	return &resp.Data, nil
}

// GetCurrentUser resolves the current user from the configured access token.
func (c *Client) GetCurrentUser(ctx context.Context) (*User, error) {
	tokenValue := strings.TrimSpace(c.token)
	if tokenValue == "" {
		return nil, fmt.Errorf("missing access token")
	}

	detail, err := c.GetTokenDetail(ctx, tokenValue)
	if err != nil {
		return nil, err
	}

	username := strings.TrimSpace(detail.UserName)
	if username == "" {
		return nil, fmt.Errorf("token owner username is empty")
	}

	user, err := c.GetUser(ctx, username)
	if err != nil {
		return &User{
			Username: username,
			UUID:     strings.TrimSpace(detail.UserUUID),
		}, nil
	}

	if strings.TrimSpace(user.Username) == "" {
		user.Username = username
	}
	if strings.TrimSpace(user.UUID) == "" {
		user.UUID = strings.TrimSpace(detail.UserUUID)
	}

	return user, nil
}

// GetBuiltinAPIKey returns the built-in AI Gateway API key for a user or organization namespace.
func (c *Client) GetBuiltinAPIKey(ctx context.Context, namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", fmt.Errorf("namespace is empty")
	}

	path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/apikeys/builtin"
	var resp APIResponse[json.RawMessage]
	if err := c.getJSON(ctx, path, &resp); err != nil {
		return "", fmt.Errorf("getting built-in API key: %w", err)
	}

	apiKey := extractBuiltinAPIKey(resp.Data)
	if apiKey == "" {
		return "", fmt.Errorf("built-in API key not found in response")
	}
	return apiKey, nil
}

func extractBuiltinAPIKey(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return ""
	}
	return extractBuiltinAPIKeyValue(value)
}

func extractBuiltinAPIKeyValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		for _, key := range []string{"api_key", "apikey", "apiKey", "key", "token", "value", "secret"} {
			if raw, ok := v[key]; ok {
				if apiKey := extractBuiltinAPIKeyValue(raw); apiKey != "" {
					return apiKey
				}
			}
		}
		for _, raw := range v {
			if _, ok := raw.(string); ok {
				continue
			}
			if apiKey := extractBuiltinAPIKeyValue(raw); apiKey != "" {
				return apiKey
			}
		}
	case []interface{}:
		for _, raw := range v {
			if apiKey := extractBuiltinAPIKeyValue(raw); apiKey != "" {
				return apiKey
			}
		}
	}
	return ""
}

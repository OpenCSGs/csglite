// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package edge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ErrGatewayUnreachable means the CSGHub AI gateway did not answer the edge
// API. The usual case is the gateway's own 404 page, which is not an
// application error the UI should show.
var ErrGatewayUnreachable = errors.New("cannot connect to the CSGHub AI gateway")

// APIError is a gateway API failure, carrying the HTTP status so the local
// API can pass it on.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("gateway returned HTTP %d: %s", e.Status, e.Message)
}

// envelope is the gateway's response wrapper.
type envelope struct {
	Msg   string          `json:"msg"`
	Data  json.RawMessage `json:"data"`
	Total int             `json:"total"`
}

// call sends one request to the gateway's edge API as the signed-in user
// and returns the response data.
func (m *Manager) call(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, int, error) {
	gatewayURL, token := m.host.Cloud()
	if strings.TrimSpace(token) == "" {
		return nil, 0, errSignedOut
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(data)
	}
	u := strings.TrimRight(gatewayURL, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("reach gateway: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, 0, err
	}
	var env envelope
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode >= http.StatusBadRequest {
		// A 404 with no edge API message is the gateway's own missing-route
		// page ("404 page not found"), not a missing application.
		if resp.StatusCode == http.StatusNotFound && !hasEdgeAPIMessage(env) {
			return nil, 0, ErrGatewayUnreachable
		}
		msg := strings.TrimSpace(env.Msg)
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return nil, 0, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return env.Data, env.Total, nil
}

// hasEdgeAPIMessage reports whether a gateway error body is an edge API
// message rather than the process's generic not-found page.
func hasEdgeAPIMessage(env envelope) bool {
	msg := strings.TrimSpace(env.Msg)
	if msg == "" || plainNotFound(msg) {
		return false
	}
	return true
}

func plainNotFound(msg string) bool {
	switch strings.ToLower(strings.TrimSpace(msg)) {
	case "404 page not found", "404 not found", "not found":
		return true
	default:
		return false
	}
}

// accountFingerprint identifies a sign-in without keeping the token.
func accountFingerprint(userToken string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(userToken)))
	return hex.EncodeToString(sum[:8])
}

// ensureRegistered returns this node's token on a gateway, registering when
// it has none there yet or another account signed in since.
func (m *Manager) ensureRegistered(ctx context.Context, gatewayURL, userToken string) (string, error) {
	account := accountFingerprint(userToken)
	m.mu.Lock()
	cred := *m.credential(gatewayURL)
	m.mu.Unlock()
	if cred.NodeToken != "" && cred.Account == account {
		return cred.NodeToken, nil
	}
	token, err := m.register(ctx, cred.NodeUUID)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusForbidden && strings.Contains(apiErr.Message, "another user") {
		// This identity is registered to another account on this gateway
		// (someone else signed in here before): take a fresh one for it.
		cred.NodeUUID = newUUID()
		token, err = m.register(ctx, cred.NodeUUID)
	}
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	stored := m.credential(gatewayURL)
	stored.NodeUUID, stored.NodeToken, stored.Account = cred.NodeUUID, token, account
	err = m.save()
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	return token, nil
}

// register registers nodeUUID with the configured gateway as the signed-in
// user and returns the node token it issues.
func (m *Manager) register(ctx context.Context, nodeUUID string) (string, error) {
	hostname, _ := os.Hostname()
	req := map[string]any{
		"node_uuid":    nodeUUID,
		"name":         m.nodeName,
		"hostname":     hostname,
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
		"lite_version": m.host.Version(),
		"hardware":     map[string]any{"cpus": runtime.NumCPU()},
	}
	data, _, err := m.call(ctx, http.MethodPost, "/api/v1/edge/nodes", nil, req)
	if err != nil {
		return "", fmt.Errorf("register node: %w", err)
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &resp); err != nil || resp.Token == "" {
		// An EE gateway without an active license answers its licensed
		// routes with an empty success.
		return "", errors.New("register node: gateway returned no token (is edge serving enabled and licensed on this gateway?)")
	}
	return resp.Token, nil
}

// SubmitRequest is an application filed from this node.
type SubmitRequest struct {
	ModelName       string `json:"model_name"`
	LocalModelID    string `json:"local_model_id"`
	Description     string `json:"description"`
	PromptPrice     int64  `json:"prompt_price"`
	CompletionPrice int64  `json:"completion_price"`
	MaxConcurrency  int    `json:"max_concurrency"`
}

// Submit files an application to serve a local model of this node. The
// node is registered first when needed, since an application names it.
func (m *Manager) Submit(ctx context.Context, req SubmitRequest) (json.RawMessage, error) {
	gatewayURL, userToken := m.host.Cloud()
	if _, err := m.ensureRegistered(ctx, gatewayURL, userToken); err != nil {
		return nil, err
	}
	body := map[string]any{
		"node_uuid":        m.nodeUUIDFor(gatewayURL),
		"model_name":       req.ModelName,
		"local_model_id":   req.LocalModelID,
		"description":      req.Description,
		"prompt_price":     req.PromptPrice,
		"completion_price": req.CompletionPrice,
		"max_concurrency":  req.MaxConcurrency,
	}
	for _, mod := range m.host.Models(ctx) {
		if mod.ID == req.LocalModelID {
			body["quantization"] = mod.Format
			body["context_length"] = mod.ContextLength
		}
	}
	data, _, err := m.call(ctx, http.MethodPost, "/api/v1/edge/applications", nil, body)
	return data, err
}

// Applications lists the signed-in user's applications.
func (m *Manager) Applications(ctx context.Context, page, per int) (json.RawMessage, int, error) {
	q := url.Values{}
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	if per > 0 {
		q.Set("per", strconv.Itoa(per))
	}
	return m.call(ctx, http.MethodGet, "/api/v1/edge/applications", q, nil)
}

// Withdraw withdraws one application and stops serving it.
func (m *Manager) Withdraw(ctx context.Context, id int64) (json.RawMessage, error) {
	data, _, err := m.call(ctx, http.MethodPost, "/api/v1/edge/applications/"+strconv.FormatInt(id, 10)+"/withdraw", nil, nil)
	return data, err
}

// Earnings returns the signed-in user's usage and earnings for a month
// (YYYY-MM, empty for the current month).
func (m *Manager) Earnings(ctx context.Context, month string) (json.RawMessage, error) {
	q := url.Values{}
	if month != "" {
		q.Set("month", month)
	}
	data, _, err := m.call(ctx, http.MethodGet, "/api/v1/edge/earnings", q, nil)
	return data, err
}

// Balance returns the signed-in user's earnings balance as CSGHub records it:
// total, settled, awaiting settlement and the open month, in whole cents.
func (m *Manager) Balance(ctx context.Context) (json.RawMessage, error) {
	data, _, err := m.call(ctx, http.MethodGet, "/api/v1/edge/balance", nil, nil)
	return data, err
}

// Statements lists the signed-in user's monthly statements, newest first,
// with each month's settlement status.
func (m *Manager) Statements(ctx context.Context) (json.RawMessage, error) {
	data, _, err := m.call(ctx, http.MethodGet, "/api/v1/edge/statements", nil, nil)
	return data, err
}

// Nodes lists the signed-in user's nodes as the gateway sees them.
func (m *Manager) Nodes(ctx context.Context) (json.RawMessage, error) {
	data, _, err := m.call(ctx, http.MethodGet, "/api/v1/edge/nodes", nil, nil)
	return data, err
}

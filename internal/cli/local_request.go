package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// requestLocalJSON uses the configured console listener. Loading a model may
// take several minutes, so inference uses the caller's context for cancellation.
func requestLocalJSON(cmd *cobra.Command, method, path string, payload, result any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	contentType := ""
	if body != nil {
		contentType = "application/json"
	}
	resp, err := localRequest(cmd, method, path, contentType, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decoding %s response: %w", path, err)
	}
	return nil
}

// The caller owns the body of a successful response.
func localRequest(cmd *cobra.Command, method, path, contentType string, body io.Reader) (*http.Response, error) {
	base, err := currentServerURL()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(cmd.Context(), method, base+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting %s (start the service with 'csghub-lite start'): %w", base, err)
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 8192))
		if err != nil {
			return nil, fmt.Errorf("%s %s: %s: %w", method, path, resp.Status, err)
		}
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(data))
	}
	return resp, nil
}

func printLocalResponse(cmd *cobra.Command, method, path string, body any) error {
	var result json.RawMessage
	if err := requestLocalJSON(cmd, method, path, body, &result); err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func localAction(use, short, method, path string, body any) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printLocalResponse(cmd, method, path, body)
		},
	}
}

func pathID(id string) (string, error) {
	if strings.TrimSpace(id) == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return "", fmt.Errorf("invalid ID %q", id)
	}
	return url.PathEscape(id), nil
}

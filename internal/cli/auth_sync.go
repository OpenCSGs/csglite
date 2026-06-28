package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/opencsgs/csglite/internal/config"
)

func syncRunningServerCloudToken(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}

	baseURL := serverBaseURL(cfg)
	if !serverHealthy(baseURL) {
		return nil
	}

	client := &http.Client{Timeout: 5 * time.Second}
	token := strings.TrimSpace(cfg.Token)
	var (
		req *http.Request
		err error
	)
	if token == "" {
		req, err = http.NewRequest(http.MethodDelete, baseURL+"/api/cloud/auth/token", nil)
	} else {
		body, marshalErr := json.Marshal(map[string]string{"token": token})
		if marshalErr != nil {
			return marshalErr
		}
		req, err = http.NewRequest(http.MethodPost, baseURL+"/api/cloud/auth/token", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("local service returned %s: %s", resp.Status, msg)
}

func warnIfTokenSyncFailed(cfg *config.Config) {
	if err := syncRunningServerCloudToken(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: running service token was not updated: %v\n", err)
	}
}

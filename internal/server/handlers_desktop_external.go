package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

type desktopOpenExternalRequest struct {
	URL string `json:"url"`
}

func validateDesktopExternalURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", fmt.Errorf("only HTTPS URLs are allowed")
	}
	return parsed.String(), nil
}

func desktopOpenExternalCommand(rawURL string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL), nil
	case "windows":
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", rawURL), nil
	default:
		return nil, fmt.Errorf("desktop external links are unsupported on %s", runtime.GOOS)
	}
}

func (s *Server) handleDesktopOpenExternal(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.DesktopMode {
		writeError(w, http.StatusConflict, "external links are managed by the browser")
		return
	}
	var req desktopOpenExternalRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	externalURL, err := validateDesktopExternalURL(req.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cmd, err := desktopOpenExternalCommand(externalURL)
	if err != nil {
		writeError(w, http.StatusNotImplemented, err.Error())
		return
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		writeError(w, http.StatusInternalServerError, "opening external link: "+message)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

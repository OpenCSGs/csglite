// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type peerError struct {
	Status int
	Body   errorResponse
}

func (e *peerError) Error() string {
	if e.Body.Error != "" {
		return fmt.Sprintf("peer answered %d: %s", e.Status, e.Body.Error)
	}
	return fmt.Sprintf("peer answered %d", e.Status)
}

// peerErrorFrom turns a peer's non-2xx answer into an error carrying its
// status and, when the body is the shared JSON envelope, its message and
// machine-readable code. A body that is not that envelope becomes the message
// as-is, so a bare text error is still readable.
func peerErrorFrom(status int, body []byte) *peerError {
	pe := &peerError{Status: status}
	_ = json.Unmarshal(body, &pe.Body)
	if pe.Body.Error == "" {
		pe.Body.Error = strings.TrimSpace(string(body))
	}
	return pe
}

// peerJSON posts (or gets) JSON to a pinned member at addr.
func (m *Manager) peerJSON(ctx context.Context, mem Member, addr, method, path string, in, out any) error {
	return doJSON(ctx, m.peers.get(mem.UUID, mem.CertFingerprint), addr, method, path, in, out)
}

func doJSON(ctx context.Context, client *http.Client, addr, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return peerErrorFrom(resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// fetchStatusUnpinned reads a node's status without a pin (seed probing,
// unpaired nodes). Only non-sensitive fields are trusted from it.
func (m *Manager) fetchStatusUnpinned(ctx context.Context, addr string) (*Status, error) {
	client := peerClient(m.identity, "", "")
	defer client.CloseIdleConnections()
	var st Status
	if err := doJSON(ctx, client, addr, http.MethodGet, peerPathStatus+"?public=1", nil, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// fetchStatus polls a member, trying each known endpoint in turn.
func (m *Manager) fetchStatus(ctx context.Context, mem Member) (*Status, string, error) {
	// Bound the whole attempt, not just each address: a member with several
	// stale addresses must still report back before the poll is considered
	// stuck.
	ctx, cancel := context.WithTimeout(ctx, statusFetchBudget)
	defer cancel()
	addrs := m.dir.Candidates(mem.UUID)
	for _, a := range m.seedAddressesFor(mem) {
		if !slices.Contains(addrs, a) {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return nil, "", errors.New("no known address")
	}
	// Every address is reported, not just the last one: a member with one
	// stale address and one good one otherwise hides the error that matters
	// behind the stale address's "no route to host".
	var failures []string
	for _, addr := range addrs {
		attempt, cancel := context.WithTimeout(ctx, 6*time.Second)
		var st Status
		err := m.peerJSON(attempt, mem, addr, http.MethodGet, peerPathStatus, nil, &st)
		cancel()
		if err == nil {
			if st.UUID != mem.UUID {
				failures = append(failures, fmt.Sprintf("%s answered as %s", addr, shortUUID(st.UUID)))
				continue
			}
			return &st, addr, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", addr, unwrapURLError(err)))
	}
	return nil, "", errors.New(strings.Join(failures, "; "))
}

// unwrapURLError strips the URL wrapper so several attempts read as one line.
func unwrapURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// statusFetchBudget caps one poll across every candidate address; it stays
// below pollStuckAfter so a poll reports back before it is abandoned.
const statusFetchBudget = 25 * time.Second

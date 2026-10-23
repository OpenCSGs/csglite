// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"testing"
)

func TestHandshakeErrorFilter(t *testing.T) {
	var buf bytes.Buffer
	l := log.New(handshakeErrorFilter{w: &buf}, "", 0)
	l.Printf("http: TLS handshake error from 192.168.1.9:50684: remote error: tls: bad certificate")
	if buf.Len() != 0 {
		t.Fatalf("handshake error should be dropped: %q", buf.String())
	}
	l.Printf("http: panic serving 10.0.0.1:1234: boom")
	if !bytes.Contains(buf.Bytes(), []byte("panic serving")) {
		t.Fatalf("other server errors must pass through: %q", buf.String())
	}
}

func TestPeerClientReportsSelfAddress(t *testing.T) {
	self, err := LoadOrCreateIdentity(t.TempDir(), "self")
	if err != nil {
		t.Fatal(err)
	}
	other, err := LoadOrCreateIdentity(t.TempDir(), "other")
	if err != nil {
		t.Fatal(err)
	}
	ln, srv, err := listenPeer("127.0.0.1:0", self, http.NotFoundHandler())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = serveTLS(ctx, srv, ln) }()

	// The member's pinned identity is "other", but its old address now
	// answers with this node's certificate.
	client := peerClient(self, other.UUID, other.Fingerprint())
	err = doJSON(ctx, client, ln.Addr().String(), http.MethodGet, peerPathStatus, nil, &Status{})
	if !errors.Is(err, errSelfAddress) {
		t.Fatalf("expected errSelfAddress, got %v", err)
	}
}

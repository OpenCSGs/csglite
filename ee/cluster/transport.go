// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// pinCheck answers whether a certificate fingerprint is pinned for a UUID.
type pinCheck func(nodeUUID, fingerprint string) bool

// serverTLSConfig presents the node certificate and asks clients for theirs.
// Verification is done per request: the join and invite handshakes arrive
// from nodes that are not pinned yet, every other path requires a pinned
// client certificate (see peerAuth).
func serverTLSConfig(id *Identity) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{id.Certificate()},
		ClientAuth:   tls.RequestClientCert,
		MinVersion:   tls.VersionTLS12,
	}
}

// peerFromRequest returns the UUID of a pinned client, or "" when the client
// presented no pinned certificate.
func peerFromRequest(r *http.Request, pinned pinCheck) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	leaf := r.TLS.PeerCertificates[0]
	nodeUUID := uuidFromCert(leaf)
	if nodeUUID == "" || !pinned(nodeUUID, CertFingerprint(leaf)) {
		return ""
	}
	return nodeUUID
}

// clientTLSConfig verifies the server by fingerprint instead of a CA. When
// wantFingerprint is empty (first contact in a join), any certificate is
// accepted and the handshake payload is what authenticates the peer.
func clientTLSConfig(id *Identity, wantUUID, wantFingerprint string) *tls.Config {
	return &tls.Config{
		Certificates:       []tls.Certificate{id.Certificate()},
		InsecureSkipVerify: true, //nolint:gosec // verified by fingerprint below
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("peer presented no certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			if wantFingerprint == "" {
				return nil
			}
			if got := CertFingerprint(leaf); got != wantFingerprint {
				return fmt.Errorf("peer certificate %s is not the pinned certificate for %s", got, wantUUID)
			}
			if wantUUID != "" && uuidFromCert(leaf) != wantUUID {
				return fmt.Errorf("peer certificate names %s, expected %s", uuidFromCert(leaf), wantUUID)
			}
			return nil
		},
	}
}

// peerClient builds an HTTP client for one peer. Connect timeout is short:
// a member that does not answer within three seconds is treated as away and
// the caller moves on. The overall timeout is left to the caller's context so
// long inference streams are not cut.
func peerClient(id *Identity, wantUUID, wantFingerprint string) *http.Client {
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSClientConfig:       clientTLSConfig(id, wantUUID, wantFingerprint),
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: 0,
			MaxIdleConns:          8,
			IdleConnTimeout:       90 * time.Second,
			ForceAttemptHTTP2:     false,
		},
	}
}

// listen opens the node-to-node listener.
func listenPeer(addr string, id *Identity, handler http.Handler) (net.Listener, *http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	srv := &http.Server{
		Handler:           handler,
		TLSConfig:         serverTLSConfig(id),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return ln, srv, nil
}

// serveTLS runs the peer listener until ctx ends.
func serveTLS(ctx context.Context, srv *http.Server, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	err := srv.ServeTLS(ln, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

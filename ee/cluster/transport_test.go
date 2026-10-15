// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"bytes"
	"log"
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

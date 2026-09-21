// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"io"
	"net"
	"testing"
	"time"
)

// The worker speaks an unauthenticated protocol that upstream says must never
// be exposed, so everything it says and hears has to survive being piped
// through the cluster's own connection unchanged. A byte lost or reordered
// here is a wrong tensor, not a failed request.
func TestPipeCopiesBothWaysAndClosesTogether(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()
	go pipe(a2, b1)

	send := func(w net.Conn, s string) {
		if err := w.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Error(err)
		}
		if _, err := w.Write([]byte(s)); err != nil {
			t.Errorf("write %q: %v", s, err)
		}
	}
	recv := func(r net.Conn, n int) string {
		if err := r.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Error(err)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatalf("read: %v", err)
		}
		return string(buf)
	}

	go send(a1, "towards the worker")
	if got := recv(b2, len("towards the worker")); got != "towards the worker" {
		t.Fatalf("forward direction carried %q", got)
	}
	go send(b2, "back from the worker")
	if got := recv(a1, len("back from the worker")); got != "back from the worker" {
		t.Fatalf("reverse direction carried %q", got)
	}

	// One side going away must close the other, or a dead worker would leave
	// the server waiting for tensors that will never arrive.
	_ = b2.Close()
	if err := a1.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := a1.Read(make([]byte, 1)); err == nil {
		t.Fatal("the near side stayed open after the far side closed")
	}
}

// Bytes the far side sends immediately after the handshake arrive in the same
// read as the headers. Dropping them would corrupt the first tensor exchange.
func TestPrefixedConnReplaysWhatWasAlreadyBuffered(t *testing.T) {
	server, client := net.Pipe()
	c := &prefixedConn{Conn: client, prefix: []byte("early")}

	buf := make([]byte, 5)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("reading the buffered prefix: %v", err)
	}
	if string(buf) != "early" {
		t.Fatalf("prefix read as %q", buf)
	}

	go func() {
		_, _ = server.Write([]byte("later"))
		_ = server.Close()
	}()
	rest, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("reading past the prefix: %v", err)
	}
	if string(rest) != "later" {
		t.Fatalf("stream continued with %q", rest)
	}
}

// The worker binary is version-locked to llama-server and looked for beside
// it, never on PATH: a mismatched copy fails at load time or computes the
// wrong answer, and both are worse than refusing to start.
func TestRPCWorkerBinaryPathRefusesWhatItCannotVerify(t *testing.T) {
	if _, err := RPCWorkerBinaryPath(""); err == nil {
		t.Fatal("an empty llama-server path was accepted")
	}
	dir := t.TempDir()
	if _, err := RPCWorkerBinaryPath(dir + "/llama-server"); err == nil {
		t.Fatal("a missing worker was accepted")
	}
}

// A worker that is not there must be caught before llama-server is told about
// it: llama-server does not fail on an endpoint it cannot reach, it logs a
// line and loads the whole model locally, which for a model that does not fit
// on one machine means thrashing rather than a clear refusal.
func TestProbeTunnelTellsALiveEndpointFromADeadOne(t *testing.T) {
	live, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	go func() {
		for {
			c, err := live.Accept()
			if err != nil {
				return
			}
			// A real worker says nothing until it is spoken to.
			_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		}
	}()
	if err := probeTunnel(live.Addr().String()); err != nil {
		t.Fatalf("a listening endpoint was reported dead: %v", err)
	}

	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := dead.Addr().String()
	_ = dead.Close()
	if err := probeTunnel(addr); err == nil {
		t.Fatal("a closed endpoint was reported live")
	}

	// A far side that accepts and hangs up at once is the shape of a worker
	// whose process died between being started and being used.
	hangup, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer hangup.Close()
	go func() {
		c, err := hangup.Accept()
		if err == nil {
			_ = c.Close()
		}
	}()
	if err := probeTunnel(hangup.Addr().String()); err == nil {
		t.Fatal("an endpoint that hung up immediately was reported live")
	}
}

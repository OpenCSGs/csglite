// Copyright (c) OpenCSG. Licensed under the CSGLite Enterprise Edition License.
// See ee/LICENSE for details.

package cluster

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

func TestLostSpansNoticesAMachineThatHasGone(t *testing.T) {
	bus := NewMemoryBus()
	n := startNode(t, bus, "host", &fakeHost{licensed: true})
	n.m.spans.put(&Span{Model: "big", Members: []SpanMember{
		{UUID: n.m.identity.UUID, Name: "host", Local: true},
		{UUID: "gone-uuid", Name: "gone"},
	}})
	lost := n.m.lostSpans()
	if len(lost) != 1 || lost[0].model != "big" || lost[0].member != "gone" {
		t.Fatalf("a span whose machine is not in the directory should count as lost: %+v", lost)
	}
}

func TestASpanEndsWhenTheMachineStaysButItsWorkerDoesNot(t *testing.T) {
	// llama.cpp's worker aborts when a load asks for more memory than the
	// machine has, which leaves a member online and reporting, quite correctly,
	// that it is holding nothing. The span is over either way.
	bus := NewMemoryBus()
	n := startNode(t, bus, "host", &fakeHost{licensed: true})
	n.m.dir.Track("peer-uuid", nil)
	// A status collected before the split began says nothing about it, and the
	// span must survive it.
	n.m.dir.MarkSuccess("peer-uuid", "127.0.0.1:1", &Status{UUID: "peer-uuid", SpanWorker: false})
	n.m.spans.put(&Span{Model: "big", Started: time.Now(), Members: []SpanMember{{UUID: "peer-uuid", Name: "peer"}}})
	if lost := n.m.lostSpans(); len(lost) != 0 {
		t.Fatalf("a status older than the span ended it: %+v", lost)
	}
	// Once the member has been heard from since, what it says counts.
	n.m.dir.MarkSuccess("peer-uuid", "127.0.0.1:1", &Status{UUID: "peer-uuid", SpanWorker: true})
	if lost := n.m.lostSpans(); len(lost) != 0 {
		t.Fatalf("a member still holding its share ended the span: %+v", lost)
	}
	n.m.dir.MarkSuccess("peer-uuid", "127.0.0.1:1", &Status{UUID: "peer-uuid", SpanWorker: false})
	lost := n.m.lostSpans()
	if len(lost) != 1 || lost[0].member != "peer" {
		t.Fatalf("a member that has stopped holding the weights should end the span: %+v", lost)
	}
}

func TestReleasingTheLastHoldStopsTheWorker(t *testing.T) {
	// The memory a worker holds is the machine's own, and the cache that makes
	// a reload quick lives on disk, so there is nothing to keep the process
	// for once no model is split onto this node.
	w := newRPCWorker("", "", func(string, ...any) {})
	w.cmd = &exec.Cmd{}
	w.port = 1234
	w.hold("member-a")
	w.hold("member-b")
	w.release("member-a")
	if !w.inUse() || w.running() == 0 {
		t.Fatal("the worker was stopped while another member still had a model split onto this node")
	}
	w.release("member-b")
	if w.inUse() || w.running() != 0 {
		t.Fatalf("the worker outlived its last hold: inUse=%v port=%d", w.inUse(), w.running())
	}
}

func TestAHoldIsDroppedWhenTheMemberThatTookItGoesAway(t *testing.T) {
	// A node that restarts while its split model is loaded never sends the
	// release. Without this the machine would refuse cold loads for as long as
	// it stayed up.
	w := newRPCWorker("", "", func(string, ...any) {})
	w.cmd = &exec.Cmd{}
	w.port = 1234
	w.hold("gone")
	w.hold("still-here")
	dropped := w.releaseGone(func(uuid string) bool { return uuid == "still-here" })
	if len(dropped) != 1 || dropped[0] != "gone" {
		t.Fatalf("dropped %v, want just the member that went away", dropped)
	}
	if !w.inUse() {
		t.Fatal("the remaining member's hold was dropped too")
	}
	w.release("still-here")
	if w.inUse() {
		t.Fatal("the worker still counts as lending with no holders left")
	}
}

func TestAHoldIsDroppedWhenTheSplitModelStopsSendingAnything(t *testing.T) {
	// The member that split a model onto this node may be switched off rather
	// than shut down, in which case it never releases anything. Tensor traffic
	// runs the whole time a split model is loaded, so a long silence with no
	// open connection is this node's own evidence that the span is over.
	w := newRPCWorker("", "", func(string, ...any) {})
	w.cmd = &exec.Cmd{}
	w.port = 1234
	w.hold("member-a")
	now := time.Now()
	if got := w.releaseStale(now); got != nil {
		t.Fatalf("a fresh hold was dropped: %v", got)
	}
	w.connOpened()
	if got := w.releaseStale(now.Add(2 * rpcWorkerHoldGrace)); got != nil {
		t.Fatalf("a hold with a live connection was dropped: %v", got)
	}
	w.connClosed()
	if got := w.releaseStale(now.Add(rpcWorkerHoldGrace / 2)); got != nil {
		t.Fatalf("a hold was dropped inside the grace period: %v", got)
	}
	dropped := w.releaseStale(time.Now().Add(2 * rpcWorkerHoldGrace))
	if len(dropped) != 1 || dropped[0] != "member-a" {
		t.Fatalf("dropped %v, want the silent member's hold", dropped)
	}
	if w.inUse() {
		t.Fatal("the worker still counts as lending after its last hold was dropped")
	}
}

func TestAMemberBeingDrainedRefusesToHoldPartOfAModel(t *testing.T) {
	// The node asking has its own view of a member's state, but that view is
	// only as fresh as the last poll, and a split takes minutes to build. The
	// member itself is the one that knows, so it answers for itself.
	bus := NewMemoryBus()
	a := startNode(t, bus, "alpha", &fakeHost{licensed: true})
	b := startNode(t, bus, "beta", &fakeHost{licensed: true})
	_, token, err := a.m.CreateCluster("Lab")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b to discover a", func() bool { return len(b.m.dir.Discovered(time.Minute)) == 1 })
	if _, err := b.m.Join(context.Background(), token, ""); err != nil {
		t.Fatalf("join: %v", err)
	}
	waitFor(t, "a to see b", func() bool {
		rt, ok := a.m.dir.Get(b.m.identity.UUID)
		return ok && rt.Online() && rt.Status != nil
	})
	if _, err := b.m.UpdateSettings(func(s *Settings) { s.State = NodeStateDrain }); err != nil {
		t.Fatal(err)
	}

	// Ask b directly, the way a span does, before a has polled b again.
	mem, _ := a.m.store.Member(b.m.identity.UUID)
	_, err = a.m.startRemoteWorker(context.Background(), mem)
	if err == nil || !strings.Contains(err.Error(), "not taking work") {
		t.Fatalf("a draining member agreed to hold part of a model: %v", err)
	}
}

func TestAWorkerLeftBehindByAKilledNodeIsStoppedOnTheWayBackUp(t *testing.T) {
	// A node that is killed outright cannot stop its own worker, so the note
	// it leaves behind is what lets the next run find it. The note is only
	// acted on when the process is still the same program, since process ids
	// are reused.
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "rpc-worker.pid")
	w := newRPCWorker("", pidFile, func(string, ...any) {})

	// A process that is not the worker is left alone, and the stale note goes.
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Skipf("cannot start a helper process here: %v", err)
	}
	defer func() { _ = sleep.Process.Kill(); _ = sleep.Wait() }()
	w.recordPID(sleep.Process.Pid, "/nowhere/ggml-rpc-server")
	w.stopOrphan()
	if _, err := os.Stat(pidFile); !os.IsNotExist(err) {
		t.Fatal("the note about a process that is not the worker was kept")
	}
	if err := sleep.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("a process that is not the worker was killed: %v", err)
	}

	// No note at all is not an error.
	w.stopOrphan()
}

func TestOneMachineHoldsOneSplitModelAtATime(t *testing.T) {
	// Two split models on one worker would ask it for memory the machine does
	// not have, and llama.cpp's worker answers that by aborting, which would
	// take the first model down with the second.
	w := newRPCWorker("", "", func(string, ...any) {})
	w.cmd = &exec.Cmd{}
	w.port = 1234
	w.hold("member-a")
	if _, busy := w.heldByOther("member-a"); busy {
		t.Fatal("the member that already holds it was told the worker was busy")
	}
	other, busy := w.heldByOther("member-b")
	if !busy || other != "member-a" {
		t.Fatalf("a second member was allowed to split onto a worker already in use: other=%q busy=%v", other, busy)
	}
	// Once the first releases it, the second is free to use it at once, with
	// no wait for anyone's status to catch up.
	w.release("member-a")
	if _, busy := w.heldByOther("member-b"); busy {
		t.Fatal("the worker stayed busy after its only holder released it")
	}
}

func TestOneModelIsOnlySplitOnce(t *testing.T) {
	// Building a split takes minutes, which is long enough for a second
	// request for the same model to arrive and start a second set of workers
	// on the same machines.
	st := newSpanState()
	if !st.begin("big") {
		t.Fatal("the first caller was refused")
	}
	if st.begin("big") {
		t.Fatal("a second caller was allowed to split the same model at the same time")
	}
	if !st.begin("other") {
		t.Fatal("a different model was refused")
	}
	st.done("big")
	if !st.begin("big") {
		t.Fatal("the model stayed claimed after the caller that failed gave it up")
	}
	st.done("big")
	st.put(&Span{Model: "big"})
	if st.begin("big") {
		t.Fatal("a model that is already split was claimed again")
	}
}

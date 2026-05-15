package server

import (
	"bytes"
	"testing"
	"time"
)

func waitForShellSession(t *testing.T, timeout time.Duration, check func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}

func TestAIAppShellSessionDetachedActivityDoesNotExtendTimeout(t *testing.T) {
	oldTimeout := aiAppShellDetachedTimeout
	aiAppShellDetachedTimeout = 120 * time.Millisecond
	t.Cleanup(func() {
		aiAppShellDetachedTimeout = oldTimeout
	})

	manager := newAIAppShellManager()
	session := &aiAppShellSession{
		manager: manager,
		id:      "detached-activity",
		subs:    make(map[chan aiAppShellEvent]struct{}),
	}
	manager.sessions[session.id] = session
	session.scheduleDetachedTimeout()

	time.Sleep(70 * time.Millisecond)
	session.touchActivity()

	if _, ok := manager.Get(session.id); !ok {
		t.Fatal("session expired before detached timeout elapsed")
	}

	waitForShellSession(t, 150*time.Millisecond, func() bool {
		_, ok := manager.Get(session.id)
		return !ok
	}, "detached session output refreshed the timeout")
}

func TestAIAppShellSessionAttachKeepsLiveSession(t *testing.T) {
	oldTimeout := aiAppShellIdleTimeout
	oldDetachedTimeout := aiAppShellDetachedTimeout
	aiAppShellIdleTimeout = 100 * time.Millisecond
	aiAppShellDetachedTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		aiAppShellIdleTimeout = oldTimeout
		aiAppShellDetachedTimeout = oldDetachedTimeout
	})

	manager := newAIAppShellManager()
	session := &aiAppShellSession{
		manager: manager,
		id:      "attach-timeout",
		subs:    make(map[chan aiAppShellEvent]struct{}),
	}
	manager.sessions[session.id] = session
	session.scheduleDetachedTimeout()

	attach := session.Attach()
	if attach.events == nil {
		t.Fatal("expected live attach events channel")
	}

	time.Sleep(250 * time.Millisecond)
	if _, ok := manager.Get(session.id); !ok {
		t.Fatal("attached live session expired")
	}

	session.Detach(attach.events)
	waitForShellSession(t, 250*time.Millisecond, func() bool {
		_, ok := manager.Get(session.id)
		return !ok
	}, "detached live session did not expire after detached timeout")
}

func TestDrainAIAppShellOutputBatchesQueuedChunksAndPreservesExit(t *testing.T) {
	events := make(chan aiAppShellEvent, 4)
	events <- aiAppShellEvent{output: []byte(" world")}
	events <- aiAppShellEvent{output: []byte("!\n")}
	events <- aiAppShellEvent{
		exit: &aiAppShellControlMessage{Type: "exit", ExitCode: 0},
	}

	payload, exitMsg, closed := drainAIAppShellOutput(aiAppShellEvent{output: []byte("hello")}, events)
	if closed {
		t.Fatal("closed = true, want false")
	}
	if exitMsg == nil || exitMsg.Type != "exit" {
		t.Fatalf("exitMsg = %#v, want exit control message", exitMsg)
	}
	if got, want := string(payload), "hello world!\n"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestDrainAIAppShellOutputStopsAtBatchLimit(t *testing.T) {
	events := make(chan aiAppShellEvent, 2)
	fill := bytes.Repeat([]byte("a"), aiAppShellWriteBatch)

	payload, exitMsg, closed := drainAIAppShellOutput(aiAppShellEvent{output: fill}, events)
	if closed {
		t.Fatal("closed = true, want false")
	}
	if exitMsg != nil {
		t.Fatalf("exitMsg = %#v, want nil", exitMsg)
	}
	if len(payload) != aiAppShellWriteBatch {
		t.Fatalf("payload len = %d, want %d", len(payload), aiAppShellWriteBatch)
	}
}

func TestBroadcastDoesNotDropWhenSubscriberBufferIsFull(t *testing.T) {
	session := &aiAppShellSession{
		subs: make(map[chan aiAppShellEvent]struct{}),
	}
	ch := make(chan aiAppShellEvent, 1)
	session.subs[ch] = struct{}{}
	ch <- aiAppShellEvent{output: []byte("first")}

	done := make(chan struct{})
	go func() {
		session.broadcast(aiAppShellEvent{output: []byte("second")})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("broadcast returned before subscriber buffer drained")
	case <-time.After(20 * time.Millisecond):
	}

	select {
	case event := <-ch:
		if got, want := string(event.output), "first"; got != want {
			t.Fatalf("first event = %q, want %q", got, want)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out reading first event")
	}

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("broadcast did not resume after buffer drained")
	}

	select {
	case event := <-ch:
		if got, want := string(event.output), "second"; got != want {
			t.Fatalf("second event = %q, want %q", got, want)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out reading second event")
	}
}

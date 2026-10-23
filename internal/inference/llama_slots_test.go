package inference

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
)

func TestSlotSchedulerOffWithOneSlot(t *testing.T) {
	if s := newLlamaSlotScheduler(1); s != nil {
		t.Fatal("scheduler created for a single slot")
	}
	var s *llamaSlotScheduler
	if slot, release := s.acquire("a"); slot != -1 {
		t.Fatalf("nil scheduler picked slot %d", slot)
	} else {
		release()
	}
}

func TestSlotSchedulerKeepsConversationsInTheirSlots(t *testing.T) {
	s := newLlamaSlotScheduler(2)
	use := func(key string) int {
		slot, release := s.acquire(key)
		release()
		return slot
	}
	a, b := use("A"), use("B")
	if a == b || a < 0 || b < 0 {
		t.Fatalf("A=%d B=%d, want two different slots", a, b)
	}
	for i := 0; i < 3; i++ {
		if got := use("A"); got != a {
			t.Fatalf("A moved to slot %d", got)
		}
		if got := use("B"); got != b {
			t.Fatalf("B moved to slot %d", got)
		}
	}
}

func TestSlotSchedulerQueuesOnOwnBusySlot(t *testing.T) {
	s := newLlamaSlotScheduler(2)
	own, releaseFirst := s.acquire("A")
	// A second A request waits for A's slot rather than overwriting another.
	if again, release := s.acquire("A"); again != own {
		t.Fatalf("second A request got slot %d, want its own busy slot %d", again, own)
	} else {
		defer release()
	}
	b, releaseB := s.acquire("B")
	if b == own || b < 0 {
		t.Fatalf("B got slot %d while slot %d is A's", b, own)
	}
	releaseFirst()
	releaseFirst() // releasing twice must not free a slot twice
	if s.slots[own].busy != 1 {
		t.Fatalf("busy count = %d after double release, want 1", s.slots[own].busy)
	}
	releaseB()
}

func TestSlotSchedulerPinsEvenWhenEverySlotIsBusy(t *testing.T) {
	s := newLlamaSlotScheduler(2)
	a, _ := s.acquire("A")
	b, _ := s.acquire("B")
	c, releaseC := s.acquire("C")
	defer releaseC()
	if c != a {
		t.Fatalf("C got slot %d, want the least recently used busy slot %d", c, a)
	}
	if s.slots[c].owner != "C" {
		t.Fatalf("slot %d owner = %q, want C, whose prompt will replace A's", c, s.slots[c].owner)
	}
	// A lost its slot, so it no longer finds one of its own there.
	if s.ownedSlot("A") != -1 || s.ownedSlot("B") != b {
		t.Fatalf("owners after C: A=%d B=%d", s.ownedSlot("A"), s.ownedSlot("B"))
	}
}

func TestSlotSchedulerEvictsLeastRecentlyUsed(t *testing.T) {
	s := newLlamaSlotScheduler(2)
	use := func(key string) int {
		slot, release := s.acquire(key)
		release()
		return slot
	}
	a, b := use("A"), use("B")
	use("B")
	if c := use("C"); c != a {
		t.Fatalf("C took slot %d, want A's slot %d (least recently used)", c, a)
	}
	if got := use("B"); got != b {
		t.Fatalf("B lost its slot to C: got %d", got)
	}
	// A was evicted, so it now takes the least recently used slot, C's.
	if got := use("A"); got != a {
		t.Fatalf("A got %d, want %d", got, a)
	}
}

func TestSlotSchedulerKeylessRequestsTakeOverWhatTheyOverwrite(t *testing.T) {
	s := newLlamaSlotScheduler(2)
	use := func(key string) int {
		slot, release := s.acquire(key)
		release()
		return slot
	}
	a := use("A")
	if anon := use(""); anon == a {
		t.Fatalf("keyless request used A's slot %d while unowned slots were idle", anon)
	}
	b := use("B") // takes the slot the keyless request left unowned
	use("B")
	// Both slots are owned now, and A's is the least recently used one.
	if anon := use(""); anon != a {
		t.Fatalf("keyless request took slot %d, want A's least recently used slot %d", anon, a)
	}
	if s.ownedSlot("A") != -1 {
		t.Fatal("A still owns the slot a keyless request overwrote")
	}
	if got := use("B"); got != b {
		t.Fatalf("B moved to slot %d", got)
	}
}

// fakeLlamaServer records the id_slot of every chat completion it receives.
func fakeLlamaServer(t *testing.T) (*llamaEngine, func() []any) {
	t.Helper()
	var mu sync.Mutex
	var slots []any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		slots = append(slots, body["id_slot"])
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)
	_, portText, _ := net.SplitHostPort(parsed.Host)
	port, _ := strconv.Atoi(portText)
	return &llamaEngine{port: port, client: server.Client()}, func() []any {
		mu.Lock()
		defer mu.Unlock()
		return append([]any(nil), slots...)
	}
}

func TestLlamaChatCompletionPinsConversationSlot(t *testing.T) {
	engine, sent := fakeLlamaServer(t)
	engine.slots = newLlamaSlotScheduler(2)
	call := func(key string, body map[string]interface{}) {
		resp, err := engine.ChatCompletion(WithSlotAffinity(context.Background(), key), body)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	shared := map[string]interface{}{"messages": []any{}}
	for i := 0; i < 2; i++ {
		call("A", shared)
		call("B", shared)
	}
	call("A", map[string]interface{}{"id_slot": 1})

	got := sent()
	if got[0] != got[2] || got[1] != got[3] || got[0] == got[1] || got[0] == nil {
		t.Fatalf("id_slot sent = %v, want A and B each in their own slot", got)
	}
	if got[4] != float64(1) {
		t.Fatalf("explicit id_slot overridden: %v", got[4])
	}
	if _, ok := shared["id_slot"]; ok {
		t.Fatal("caller's request body was modified")
	}
}

func TestLlamaChatCompletionSingleSlotSendsNoSlot(t *testing.T) {
	engine, sent := fakeLlamaServer(t)
	resp, err := engine.ChatCompletion(WithSlotAffinity(context.Background(), "A"), map[string]interface{}{"messages": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := sent(); got[0] != nil {
		t.Fatalf("single-slot engine sent id_slot %v", got[0])
	}
}

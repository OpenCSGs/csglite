package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/config"
	"github.com/opencsgs/csglite/internal/inference"
)

type poolRuntimeTestEngine struct {
	chat  func(inference.TokenCallback) (string, error)
	proxy func() (*http.Response, error)
}

func (e *poolRuntimeTestEngine) ModelName() string { return "test" }
func (e *poolRuntimeTestEngine) Close() error      { return nil }
func (e *poolRuntimeTestEngine) Generate(_ context.Context, _ string, _ inference.Options, callback inference.TokenCallback) (string, error) {
	return e.Chat(context.Background(), nil, inference.Options{}, callback)
}
func (e *poolRuntimeTestEngine) Chat(_ context.Context, _ []inference.Message, _ inference.Options, callback inference.TokenCallback) (string, error) {
	if e.chat != nil {
		return e.chat(callback)
	}
	return "ok", nil
}
func (e *poolRuntimeTestEngine) ChatCompletion(context.Context, map[string]interface{}) (*http.Response, error) {
	return e.proxy()
}

func newPoolRuntimeTestEngine(now *time.Time, members ...providerPoolEngineMember) *providerPoolEngine {
	return &providerPoolEngine{
		poolID:  "pool",
		modelID: "pool-model",
		members: members,
		mu:      &sync.Mutex{},
		current: make(map[string]int),
		runtime: make(map[string]*providerPoolMemberRuntime),
		now:     func() time.Time { return *now },
	}
}

func poolRuntimeResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestProviderPoolRPMSlidingWindowSkipsExhaustedMember(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	firstCalls, fallbackCalls := 0, 0
	engine := newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "first", Priority: 0, RequestsPM: 1},
			new: func() (inference.Engine, error) {
				firstCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{"usage":{"total_tokens":1}}`), nil
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "fallback", Priority: 1},
			new: func() (inference.Engine, error) {
				fallbackCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
	)
	request := map[string]interface{}{"prompt": "x"}
	for i := 0; i < 2; i++ {
		response, err := engine.ChatCompletion(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if firstCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("calls before expiry = first:%d fallback:%d", firstCalls, fallbackCalls)
	}
	now = now.Add(time.Minute + time.Nanosecond)
	response, err := engine.ChatCompletion(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if firstCalls != 2 {
		t.Fatalf("first calls after sliding-window expiry = %d, want 2", firstCalls)
	}
}

func TestProviderPoolWeightedRoundRobin(t *testing.T) {
	now := time.Now()
	calls := map[string]int{}
	member := func(id string, weight int) providerPoolEngineMember {
		return providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: id, Priority: 0, Weight: weight},
			new: func() (inference.Engine, error) {
				calls[id]++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		}
	}
	engine := newPoolRuntimeTestEngine(&now, member("heavy", 3), member("light", 1))
	for range 8 {
		response, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if calls["heavy"] != 6 || calls["light"] != 2 {
		t.Fatalf("weighted calls = %#v, want heavy:6 light:2", calls)
	}
}

func TestProviderPoolAllMembersLimitedReturns429(t *testing.T) {
	now := time.Now()
	engine := newPoolRuntimeTestEngine(&now, providerPoolEngineMember{
		member: config.ProviderPoolMember{ID: "only", RequestsPM: 1},
		new: func() (inference.Engine, error) {
			return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
				return poolRuntimeResponse(`{}`), nil
			}}, nil
		},
	})
	response, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if _, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"}); inference.HTTPStatusCode(err) != http.StatusTooManyRequests {
		t.Fatalf("exhausted pool error = %v, want HTTP 429", err)
	}
}

func TestProviderPoolMaxConcurrentFallsBackUntilBodyCloses(t *testing.T) {
	now := time.Now()
	firstCalls, fallbackCalls := 0, 0
	engine := newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "first", Priority: 0, MaxConcurrent: 1},
			new: func() (inference.Engine, error) {
				firstCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "fallback", Priority: 1},
			new: func() (inference.Engine, error) {
				fallbackCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
	)
	first, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	if firstCalls != 1 || fallbackCalls != 1 {
		t.Fatalf("calls while first body open = first:%d fallback:%d", firstCalls, fallbackCalls)
	}
	_ = first.Body.Close()
	third, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = third.Body.Close()
	if firstCalls != 2 {
		t.Fatalf("first calls after close = %d, want 2", firstCalls)
	}
}

func TestProviderPoolTPMReconcilesNonStreamingActualUsage(t *testing.T) {
	now := time.Now()
	firstCalls, fallbackCalls := 0, 0
	engine := newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "first", Priority: 0, TokensPM: 11},
			new: func() (inference.Engine, error) {
				firstCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{"usage":{"total_tokens":2}}`), nil
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "fallback", Priority: 1},
			new: func() (inference.Engine, error) {
				fallbackCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
	)
	request := map[string]interface{}{"prompt": "1234", "max_tokens": 8, "stream": false}
	for i := 0; i < 2; i++ {
		response, err := engine.ChatCompletion(t.Context(), request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(response.Body); err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if firstCalls != 2 || fallbackCalls != 0 {
		t.Fatalf("actual-token reconciliation calls = first:%d fallback:%d", firstCalls, fallbackCalls)
	}
}

func TestProviderPoolRetryAfterCooldownAndOrdinary4xx(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	firstCalls, fallbackCalls := 0, 0
	engine := newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "first", Priority: 0},
			new: func() (inference.Engine, error) {
				firstCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					if firstCalls == 1 {
						response := poolRuntimeResponse(`{"error":"limited"}`)
						response.StatusCode = http.StatusTooManyRequests
						response.Header.Set("Retry-After", "30")
						return response, nil
					}
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "fallback", Priority: 1},
			new: func() (inference.Engine, error) {
				fallbackCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
	)
	for _, advance := range []time.Duration{0, 10 * time.Second, 21 * time.Second} {
		now = now.Add(advance)
		response, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if firstCalls != 2 || fallbackCalls != 2 {
		t.Fatalf("cooldown calls = first:%d fallback:%d", firstCalls, fallbackCalls)
	}

	ordinaryCalls := 0
	engine = newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "bad", Priority: 0},
			new: func() (inference.Engine, error) {
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					response := poolRuntimeResponse(`{"error":"bad request"}`)
					response.StatusCode = http.StatusBadRequest
					return response, nil
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "unused", Priority: 1},
			new: func() (inference.Engine, error) {
				ordinaryCalls++
				return nil, errors.New("must not be called")
			},
		},
	)
	if _, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"}); inference.HTTPStatusCode(err) != http.StatusBadRequest {
		t.Fatalf("ordinary 4xx error = %v", err)
	}
	if ordinaryCalls != 0 {
		t.Fatalf("ordinary 4xx fallback calls = %d, want 0", ordinaryCalls)
	}
}

func TestProviderPoolFallsBackOnInsufficientBalance(t *testing.T) {
	now := time.Now()
	fallbackCalls := 0
	engine := newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "depleted", Source: "cloud", Priority: 0},
			new: func() (inference.Engine, error) {
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					response := poolRuntimeResponse(`{"error":{"message":"Insufficient balance"}}`)
					response.StatusCode = http.StatusPaymentRequired
					return response, nil
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "fallback", Source: "provider:fallback", Priority: 1},
			new: func() (inference.Engine, error) {
				fallbackCalls++
				return &poolRuntimeTestEngine{proxy: func() (*http.Response, error) {
					return poolRuntimeResponse(`{}`), nil
				}}, nil
			},
		},
	)

	response, err := engine.ChatCompletion(t.Context(), map[string]interface{}{"prompt": "x"})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if fallbackCalls != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallbackCalls)
	}
	if got := response.Header.Get(providerPoolMemberSourceHeader); got != "provider:fallback" {
		t.Fatalf("member source = %q, want provider:fallback", got)
	}
	if got := response.Header.Get(providerPoolFallbackCountHeader); got != "1" {
		t.Fatalf("fallback count = %q, want 1", got)
	}
}

func TestProviderPoolRetryableIncludesPaymentRequired(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "payment required without recognizable message",
			err:  inference.NewHTTPStatusError(http.StatusPaymentRequired, "billing unavailable"),
			want: true,
		},
		{
			name: "insufficient balance on another client error",
			err:  inference.NewHTTPStatusError(http.StatusForbidden, "Insufficient balance"),
			want: true,
		},
		{
			name: "ordinary client error",
			err:  inference.NewHTTPStatusError(http.StatusBadRequest, "bad request"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerPoolRetryable(tt.err); got != tt.want {
				t.Fatalf("providerPoolRetryable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProviderPoolDoesNotRetryAfterStreamingToken(t *testing.T) {
	now := time.Now()
	fallbackCalls := 0
	streamErr := errors.New("stream failed")
	engine := newPoolRuntimeTestEngine(&now,
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "first", Priority: 0},
			new: func() (inference.Engine, error) {
				return &poolRuntimeTestEngine{chat: func(callback inference.TokenCallback) (string, error) {
					callback("partial")
					return "", streamErr
				}}, nil
			},
		},
		providerPoolEngineMember{
			member: config.ProviderPoolMember{ID: "fallback", Priority: 1},
			new: func() (inference.Engine, error) {
				fallbackCalls++
				return &poolRuntimeTestEngine{}, nil
			},
		},
	)
	var tokens []string
	_, err := engine.Chat(t.Context(), []inference.Message{{Role: "user", Content: "x"}}, inference.DefaultOptions(), func(token string) {
		tokens = append(tokens, token)
	})
	if !errors.Is(err, streamErr) || fallbackCalls != 0 || len(tokens) != 1 {
		t.Fatalf("stream result err=%v fallback=%d tokens=%v", err, fallbackCalls, tokens)
	}
}

func TestProviderPoolDirectChatRecordsPoolAndMemberUsage(t *testing.T) {
	s := newTestServer(t)
	now := time.Now()
	capture := &providerPoolUsageCapture{}
	engine := newPoolRuntimeTestEngine(&now, providerPoolEngineMember{
		member: config.ProviderPoolMember{ID: "local", Source: "local", Model: "actual-model"},
		new: func() (inference.Engine, error) {
			return &poolRuntimeTestEngine{chat: func(inference.TokenCallback) (string, error) {
				return "answer", nil
			}}, nil
		},
	})
	engine.poolID = "chat-pool"
	engine.poolName = "Chat Pool"
	engine.modelID = "public-model"
	engine.usage = capture

	if _, err := engine.Chat(t.Context(), []inference.Message{{Role: "user", Content: "question"}}, inference.DefaultOptions(), nil); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request = request.WithContext(context.WithValue(request.Context(), providerPoolUsageContextKey{}, capture))
	s.recordAPIUsage(request, "public-model", "pool:chat-pool", 2, 1)

	state, err := s.apiUsage.List(config.APIUsageListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Records) != 1 {
		t.Fatalf("usage records = %#v", state.Records)
	}
	record := state.Records[0]
	if record.PoolID != "chat-pool" || record.PoolName != "Chat Pool" ||
		record.PoolModel != "public-model" || record.Source != "local" ||
		record.MemberModel != "actual-model" {
		t.Fatalf("pool usage record = %#v", record)
	}
}

package server

import (
	"context"

	"github.com/opencsgs/csglite/internal/inference"
	"github.com/opencsgs/csglite/internal/realtime"
)

// realtimeResponder adapts the chat engines to the session's Responder, so a
// session that names a conversation model has its replies written here rather
// than by the client. The gain is not the round trip it saves but the overlap:
// the session hands each sentence to synthesis as the model finishes it, where
// a client can only ask for the reply to be spoken once it holds all of it.
type realtimeResponder struct {
	server *Server
}

func (s *Server) newRealtimeResponder() realtime.Responder {
	return &realtimeResponder{server: s}
}

func (r *realtimeResponder) Reply(ctx context.Context, model string, history []realtime.Message, onText func(string) error) error {
	// The empty source lets the model resolve the way a chat completion for it
	// would: local first, then the cloud and configured providers.
	eng, err := r.server.getChatEngine(ctx, model, "", 0, 0, -1, "", "", "")
	if err != nil {
		return err
	}
	defer r.server.touchEngine(model)

	messages := make([]inference.Message, 0, len(history))
	for _, m := range history {
		messages = append(messages, inference.Message{Role: m.Role, Content: m.Content})
	}
	opts := inference.DefaultOptions()
	// Reasoning is for reading, not listening: a spoken reply that opens with
	// the model thinking aloud is seconds of noise before the answer.
	opts.DisableThinking = true

	// The token callback cannot return an error, so a refusal from onText is
	// carried out of the engine by cancelling its context.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var textErr error
	_, err = eng.Chat(ctx, messages, opts, func(token string) {
		if textErr != nil || token == "" {
			return
		}
		if e := onText(token); e != nil {
			textErr = e
			cancel()
		}
	})
	if textErr != nil {
		return textErr
	}
	return err
}

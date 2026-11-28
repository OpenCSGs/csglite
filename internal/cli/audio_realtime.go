package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/opencsgs/csglite/internal/realtime"
	"github.com/spf13/cobra"
)

// Read and write protocol events concurrently so clients can process transcript
// or audio deltas while still supplying microphone data on stdin.
func newRealtimeCmd() *cobra.Command {
	var sessionFile, eventsFile, model, until string
	var transcription bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use: "realtime", Short: "Exchange JSON events over a realtime audio WebSocket",
		Example: "  csghub-lite audio realtime --transcription --model ASR_MODEL --events turn.jsonl",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 {
				return fmt.Errorf("--timeout must be positive")
			}
			base, err := currentServerURL()
			if err != nil {
				return err
			}
			endpoint, terminal, err := realtimeEndpoint(base, sessionFile, model, until, transcription)
			if err != nil {
				return err
			}
			input := cmd.InOrStdin()
			if eventsFile != "" {
				file, err := os.Open(eventsFile)
				if err != nil {
					return err
				}
				defer file.Close()
				input = file
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			return runRealtimeEvents(ctx, endpoint, input, cmd.OutOrStdout(), terminal)
		},
	}
	cmd.Flags().StringVar(&sessionFile, "session", "", "initial realtime session configuration JSON file")
	cmd.Flags().StringVar(&eventsFile, "events", "", "client event file (default: JSON events on stdin)")
	cmd.Flags().StringVar(&model, "model", "", "recognition model for a transcription session")
	cmd.Flags().BoolVar(&transcription, "transcription", false, "open a transcription-only session")
	cmd.Flags().StringVar(&until, "until", "", "exit after this server event (defaults to response.done or the final transcript)")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "maximum session duration")
	return cmd
}

func realtimeEndpoint(base, sessionFile, model, until string, transcription bool) (string, string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", "", err
	}
	u.Scheme = "ws"
	u.Path = "/ai-gateway/v1/realtime"
	query := url.Values{}
	if model != "" {
		query.Set("model", model)
	}
	var session realtime.SessionConfig
	if sessionFile != "" {
		raw, err := os.ReadFile(sessionFile)
		if err != nil {
			return "", "", err
		}
		if err := json.Unmarshal(raw, &session); err != nil {
			return "", "", fmt.Errorf("reading session configuration: %w", err)
		}
	}
	if transcription || strings.EqualFold(session.Type, "transcription") {
		session.Type = "transcription"
		u.Path += "/transcription"
		if until == "" {
			until = realtime.ServerTranscriptCompleted
		}
	} else if until == "" {
		until = realtime.ServerResponseDone
	}
	if sessionFile != "" || transcription {
		data, err := json.Marshal(session)
		if err != nil {
			return "", "", err
		}
		query.Set("session", string(data))
	}
	u.RawQuery = query.Encode()
	return u.String(), until, nil
}

func runRealtimeEvents(ctx context.Context, endpoint string, input io.Reader, output io.Writer, terminal string) error {
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
			return fmt.Errorf("opening realtime session: %s: %w", resp.Status, err)
		}
		return err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	sent := make(chan error, 1)
	go func() {
		err := sendRealtimeEvents(conn, input)
		sent <- err
		if err != nil {
			_ = conn.Close()
		}
	}()
	defer func() {
		// On an early server error stdin may still be blocked awaiting more
		// events. The command owns its input stream for the session.
		if closer, ok := input.(io.Closer); ok {
			_ = closer.Close()
		}
	}()
	encoder := json.NewEncoder(output)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case sendErr := <-sent:
				if sendErr != nil {
					return sendErr
				}
			default:
			}
			return fmt.Errorf("reading realtime events: %w", err)
		}
		var ev realtime.ServerEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return err
		}
		if err := encoder.Encode(json.RawMessage(data)); err != nil {
			return err
		}
		if ev.Type == realtime.ServerError || ev.Type == realtime.ServerTranscriptFailed {
			if ev.Error != nil {
				return fmt.Errorf("%s: %s", ev.Type, ev.Error.Message)
			}
			return fmt.Errorf("realtime session returned %s", ev.Type)
		}
		if ev.Type == realtime.ServerResponseDone {
			status := ev.Status
			if ev.Response != nil {
				status = ev.Response.Status
			}
			if status != "completed" {
				return fmt.Errorf("realtime response ended with status %q", status)
			}
		}
		if ev.Type == terminal {
			return nil
		}
	}
}

func sendRealtimeEvents(conn *websocket.Conn, input io.Reader) error {
	decoder := json.NewDecoder(input)
	for count := 0; ; count++ {
		var data json.RawMessage
		if err := decoder.Decode(&data); errors.Is(err, io.EOF) {
			if count == 0 {
				return fmt.Errorf("provide client events on stdin or with --events")
			}
			return nil
		} else if err != nil {
			return fmt.Errorf("reading client event: %w", err)
		}
		var event struct{ Type string }
		if err := json.Unmarshal(data, &event); err != nil || strings.TrimSpace(event.Type) == "" {
			return fmt.Errorf("client event requires a type")
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			return err
		}
	}
}

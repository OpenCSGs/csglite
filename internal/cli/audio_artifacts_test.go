package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/opencsgs/csglite/pkg/api"
)

func serveCLI(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	setupCLIConfigHome(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	saveListenAddr(t, strings.TrimPrefix(srv.URL, "http://"))
}

func TestCLIUploads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		files  []string
		fields []string
		want   string
	}{
		{"directory", []string{".git/config", "sub/a.gguf"}, []string{"model=org/model", "mode=directory", "overwrite=true", "paths=sub/a.gguf", "files=a.gguf:data"}, "{\"ok\":true}\n"},
		{"archive", []string{"m.tar.gz"}, []string{"model=org/model", "mode=archive", "overwrite=false", "files=m.tar.gz:data"}, "{\"ok\":true}\n"},
		{"weights", []string{"m.gguf"}, []string{"model=org/model", "mode=files", "overwrite=false", "files=m.gguf:data"}, "{\"ok\":true}\n"},
		{"transcribe", []string{"sample.wav"}, []string{"model=asr", "language=zh", "file=sample.wav:data"}, "你好\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := dir
			for _, name := range tc.files {
				path = filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("data"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"import", "org/model", path}
			route, reply := "/api/models/upload", `{"ok":true}`
			if tc.name == "directory" {
				args = []string{"import", "org/model", dir, "--overwrite"}
			}
			if tc.name == "transcribe" {
				args = []string{"audio", "transcribe", "asr", path, "--language", "zh", "--source", "cloud"}
				route, reply = "/ai-gateway/providers/cloud/v1/audio/transcriptions", `{"text":"你好"}`
			}
			serveCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != route || r.Method != "POST" {
					t.Errorf("%s %s", r.Method, r.URL.Path)
				}
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				var fields []string
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					data, err := io.ReadAll(part)
					if err != nil {
						t.Error(err)
						return
					}
					value := string(data)
					if part.FileName() != "" {
						value = part.FileName() + ":" + value
					}
					fields = append(fields, part.FormName()+"="+value)
				}
				if !reflect.DeepEqual(fields, tc.fields) {
					t.Errorf("form %v, want %v", fields, tc.fields)
				}
				_, _ = io.WriteString(w, reply)
			})
			out, err := runModelCLI(args, "")
			if err != nil || out != tc.want {
				t.Fatalf("output %q, error %v", out, err)
			}
		})
	}
}

func TestCLISpeechOutput(t *testing.T) {
	for _, mode := range []string{"stdout", "file", "truncated", "existing", "refused"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "speech.wav")
			if mode == "stdout" {
				path = "-"
			}
			if mode == "existing" {
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			serveCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if mode == "existing" {
					t.Error("submitted inference for an existing output file")
				}
				if r.Method != "POST" || r.URL.Path != "/ai-gateway/providers/local/v1/audio/speech" {
					t.Errorf("%s %s", r.Method, r.URL.Path)
				}
				var req api.OpenAIAudioSpeechRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Model != "tts" || req.Input != "read me" || req.Voice != "a" || req.ResponseFormat != "wav" {
					t.Errorf("request %+v: %v", req, err)
				}
				if mode == "refused" {
					http.Error(w, "model unavailable", 409)
					return
				}
				w.Header().Set("Content-Type", "audio/wav")
				if mode == "truncated" {
					w.Header().Set("Content-Length", "500")
				}
				_, _ = w.Write([]byte{'W', 0, 'V'})
			})
			out, err := runModelCLI([]string{"audio", "speak", "tts", "--voice", "a", "--output", path}, "read me")
			switch mode {
			case "existing":
				b, e := os.ReadFile(path)
				if err == nil || e != nil || string(b) != "original" {
					t.Fatalf("file %q, errors %v %v", b, err, e)
				}
			case "truncated", "refused":
				if _, e := os.Stat(path); err == nil || !os.IsNotExist(e) {
					t.Fatalf("error %v, file status %v", err, e)
				}
			case "file":
				b, e := os.ReadFile(path)
				if err != nil || e != nil || string(b) != "W\x00V" || out != "" {
					t.Fatalf("file %q, output %q, errors %v %v", b, out, err, e)
				}
			case "stdout":
				if err != nil || out != "W\x00V" {
					t.Fatalf("output %q, error %v", out, err)
				}
			}
		})
	}
}

func TestCLIRealtime(t *testing.T) {
	const completed = `{"type":"conversation.item.input_audio_transcription.completed","transcript":"done"}`
	for _, tc := range []struct{ name, event, wantErr string }{
		{"final", completed, ""},
		{"server error", `{"type":"error","error":{"message":"audio unavailable"}}`, "audio unavailable"},
		{"cancelled", `{"type":"response.done","response":{"status":"cancelled"}}`, "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serveCLI(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/ai-gateway/v1/realtime/transcription" || r.URL.Query().Get("model") != "asr" {
					t.Errorf("request %s", r.URL)
				}
				up := websocket.Upgrader{}
				conn, err := up.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				for _, want := range []string{"input_audio_buffer.append", "input_audio_buffer.commit"} {
					var event struct{ Type string }
					if err := conn.ReadJSON(&event); err != nil || event.Type != want {
						t.Errorf("event %q, error %v", event.Type, err)
						return
					}
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(tc.event)); err != nil {
					t.Error(err)
				}
			})
			out, err := runModelCLI([]string{"audio", "realtime", "--transcription", "--model", "asr", "--timeout", "4s"}, "{\"type\":\"input_audio_buffer.append\",\"audio\":\"AA==\"}\n{\"type\":\"input_audio_buffer.commit\"}")
			if out != tc.event+"\n" || (tc.wantErr == "" && err != nil) || (tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr))) {
				t.Fatalf("output %q, error %v", out, err)
			}
		})
	}
}

func TestCLIPublishService(t *testing.T) {
	serveCLI(t, func(w http.ResponseWriter, r *http.Request) {
		var req api.ProviderPoolCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/provider-pools" || req.Name != "lab" || req.Model != "lab" || req.Policy != "failover" || req.Category != "chat" || len(req.Members) != 2 {
			t.Errorf("request %s %+v", r.URL, req)
			return
		}
		for i, source := range []string{"local", "cloud"} {
			member := req.Members[i]
			if _, err := uuid.Parse(member.ID); err != nil || member.Priority != i || member.Model != "org/m" || member.Source != source || member.Weight != 100 {
				t.Errorf("member %+v", member)
			}
		}
		if req.Members[0].ID == req.Members[1].ID {
			t.Error("duplicate member ID")
		}
		fmt.Fprint(w, `{"id":"key"}`)
	})
	out, err := runModelCLI([]string{"gateway", "publish", "lab", "--member", "local=org/m", "--member", "cloud=org/m"}, "")
	if err != nil || !strings.Contains(out, "key") {
		t.Fatalf("output %q, error %v", out, err)
	}
}

func TestCLIUploadReportsEarlyRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	// Exceed the socket buffer so the producer is still writing on rejection.
	if err := f.Truncate(8 << 20); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	serveCLI(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upload disabled", http.StatusForbidden)
	})
	out, err := runModelCLI([]string{"import", "org/model", path}, "")
	if out != "" || err == nil || !strings.Contains(err.Error(), "upload disabled") {
		t.Fatalf("output %q, error %v", out, err)
	}
}

func TestCLIRealtimeInvalidInput(t *testing.T) {
	serveCLI(t, func(w http.ResponseWriter, r *http.Request) {
		up := websocket.Upgrader{}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _, _ = conn.ReadMessage()
	})
	out, err := runModelCLI([]string{"audio", "realtime", "--timeout", "4s"}, `{"audio":"AA=="}`)
	if err == nil || !strings.Contains(err.Error(), "client event requires a type") || out != "" {
		t.Fatalf("output %q, error %v", out, err)
	}
}

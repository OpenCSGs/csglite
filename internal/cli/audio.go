package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/opencsgs/csglite/pkg/api"
	"github.com/spf13/cobra"
)

func newAudioCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "audio", Short: "Transcribe audio, synthesise speech, and run realtime sessions"}
	cmd.AddCommand(newTranscribeCmd(), newSpeechCmd(), newRealtimeCmd())
	return cmd
}

func newTranscribeCmd() *cobra.Command {
	var source, language string
	var asJSON bool
	cmd := &cobra.Command{Use: "transcribe MODEL FILE", Short: "Transcribe a recording", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := inferenceRoute(source, "audio/transcriptions")
			if err != nil {
				return err
			}
			resp, err := postMultipart(cmd, path, func(form *multipart.Writer) error {
				if err := form.WriteField("model", args[0]); err != nil {
					return err
				}
				if err := form.WriteField("language", language); err != nil {
					return err
				}
				return writeFormFile(form, "file", args[1])
			})
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if asJSON {
				var result json.RawMessage
				if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			}
			var result struct{ Text *string }
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				return err
			}
			if result.Text == nil {
				return fmt.Errorf("server returned no transcript")
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), *result.Text)
			return err
		},
	}
	cmd.Flags().StringVar(&source, "source", "local", "recognition source (local or cloud)")
	cmd.Flags().StringVar(&language, "language", "", "language hint")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the complete transcription response")
	return cmd
}

func newSpeechCmd() *cobra.Command {
	var source, output string
	var req api.OpenAIAudioSpeechRequest
	cmd := &cobra.Command{Use: "speak MODEL [TEXT]", Short: "Synthesize speech to a new file or standard output", Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := inferenceRoute(source, "audio/speech")
			if err != nil {
				return err
			}
			input, err := inferenceInputs(cmd, args[1:])
			if err != nil {
				return err
			}
			if output == "" {
				return fmt.Errorf("provide an output path or - for stdout")
			}
			if output != "-" {
				if _, err := os.Lstat(output); err == nil {
					return fmt.Errorf("output already exists: %s", output)
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			req.Model, req.Input = args[0], input[0]
			data, err := json.Marshal(req)
			if err != nil {
				return err
			}
			resp, err := localRequest(cmd, http.MethodPost, path, "application/json", bytes.NewReader(data))
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "audio/") {
				return fmt.Errorf("expected audio from speech endpoint, received %q", resp.Header.Get("Content-Type"))
			}
			if output == "-" {
				_, err = io.Copy(cmd.OutOrStdout(), resp.Body)
				return err
			}
			return saveNewAudioFile(output, resp.Body)
		},
	}
	cmd.Flags().StringVar(&source, "source", "local", "speech model source")
	cmd.Flags().StringVarP(&output, "output", "o", "", "new audio file, or - for stdout")
	_ = cmd.MarkFlagRequired("output")
	cmd.Flags().StringVar(&req.Voice, "voice", "", "speaker name")
	cmd.Flags().StringVar(&req.ResponseFormat, "format", "wav", "wav, mp3, opus, flac, aac or pcm")
	return cmd
}

func saveNewAudioFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return nil
}

package embedding

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/logutil"
)

func TestWaitReadyIncludesWorkerOutputTail(t *testing.T) {
	logBuf := logutil.NewTailWriter(64 * 1024)
	if _, err := logBuf.Write([]byte("Traceback: libtorchaudio.pyd failed to load")); err != nil {
		t.Fatal(err)
	}
	exitCh := make(chan error, 1)
	exitCh <- errors.New("exit status 1")
	close(exitCh)

	engine := &PythonEngine{
		exitCh: exitCh,
		port:   1,
		client: &http.Client{Timeout: time.Millisecond},
		logBuf: logBuf,
	}
	err := engine.waitReady(context.Background())
	if err == nil {
		t.Fatal("waitReady returned nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "embedding worker exited before becoming ready") {
		t.Fatalf("waitReady error missing exit context: %q", msg)
	}
	if !strings.Contains(msg, "Traceback: libtorchaudio.pyd failed to load") {
		t.Fatalf("waitReady error missing worker output tail: %q", msg)
	}
}

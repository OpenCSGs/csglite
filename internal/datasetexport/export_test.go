package datasetexport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opencsgs/csglite/internal/observability"
)

type fakeTraceStore map[string][]observability.RequestRecord

func (s fakeTraceStore) GetTrace(_ context.Context, id string) (observability.TraceRecord, []observability.RequestRecord, error) {
	return observability.TraceRecord{TraceID: id}, s[id], nil
}

func (s fakeTraceStore) VisitTraces(_ context.Context, ids []string, visit func(string, []observability.RequestRecord) error) error {
	for _, id := range ids {
		if records, ok := s[id]; ok {
			if err := visit(id, records); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestBuildPreviewRedactsAndAdaptsFormats(t *testing.T) {
	store := fakeTraceStore{
		"trace-1": {{
			ID: "request-1", TraceID: "trace-1", Status: "completed", Protocol: "openai",
			Model: "test/model", Source: "local", InputTokens: 4, OutputTokens: 2,
			RequestBody:  `{"messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Email me at user@example.com"}]}`,
			ResponseBody: `{"choices":[{"message":{"role":"assistant","content":"Call +1 202 555 0123"}}]}`,
		}},
	}
	for _, format := range []string{FormatOpenAI, FormatShareGPT, FormatAlpaca, FormatCompletion} {
		t.Run(format, func(t *testing.T) {
			preview, err := BuildPreview(context.Background(), store, Options{
				TraceIDs: []string{"trace-1"}, Format: format, RedactionPolicy: PolicyRedact,
			})
			if err != nil {
				t.Fatal(err)
			}
			if preview.Exported != 1 || preview.Excluded != 0 || len(preview.Sample) == 0 {
				t.Fatalf("unexpected preview: %+v", preview)
			}
			if strings.Contains(string(preview.Sample), "user@example.com") || strings.Contains(string(preview.Sample), "202 555") {
				t.Fatalf("preview contains private data: %s", preview.Sample)
			}
			if len(preview.Risks) != 2 {
				t.Fatalf("risks = %+v, want email and phone", preview.Risks)
			}
			var value any
			if err := json.Unmarshal(preview.Sample, &value); err != nil {
				t.Fatalf("preview sample is invalid JSON: %v", err)
			}
		})
	}
}

func TestExportWritesValidatedArtifacts(t *testing.T) {
	now := time.Now().UTC()
	store := fakeTraceStore{
		"trace-1": {{
			ID: "request-1", TraceID: "trace-1", StartedAt: now, CompletedAt: now.Add(time.Second),
			Status: "completed", Protocol: "lite", Model: "test/model",
			RequestBody:  `{"messages":[{"role":"user","content":"hello"}]}`,
			ResponseBody: "data: {\"message\":{\"content\":\"world\"}}\n\ndata: {\"done\":true}\n",
		}},
	}
	root := t.TempDir()
	datasetDir := filepath.Join(root, "datasets")
	artifact, err := Export(context.Background(), store, root, datasetDir, Options{
		TraceIDs: []string{"trace-1"}, Format: FormatOpenAI, RedactionPolicy: PolicyRedact,
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Exported != 1 || len(artifact.Files) != 3 {
		t.Fatalf("unexpected artifact: %+v", artifact)
	}
	data, err := os.ReadFile(filepath.Join(artifact.Directory, "data", "train-00000.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"content":"world"`) {
		t.Fatalf("dataset content = %s", data)
	}
	if _, err := os.Stat(filepath.Join(artifact.Directory, "manifest.json")); err != nil {
		t.Fatalf("local dataset manifest missing: %v", err)
	}
	if artifact.DatasetID == "" {
		t.Fatal("local dataset ID is missing")
	}
	manifestData, err := os.ReadFile(filepath.Join(artifact.Directory, "export-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exportManifest manifest
	if err := json.Unmarshal(manifestData, &exportManifest); err != nil {
		t.Fatal(err)
	}
	// The export manifest cannot checksum itself without becoming recursively
	// self-referential; Artifact.Files remains the authoritative complete list.
	if len(exportManifest.Files) != 2 || len(artifact.Files) != 3 {
		t.Fatalf("manifest files = %d artifact files = %d, want 2 and 3", len(exportManifest.Files), len(artifact.Files))
	}
	loaded, err := LoadArtifact(root, datasetDir, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Format != FormatOpenAI || loaded.Exported != 1 {
		t.Fatalf("loaded artifact = %+v", loaded)
	}
}

func TestCompletionFormatOnlyDegradesComplexConversations(t *testing.T) {
	simple := Sample{Messages: []Message{
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "Answer"},
	}}
	_, degraded, err := adaptSample(simple, FormatCompletion)
	if err != nil {
		t.Fatal(err)
	}
	if degraded {
		t.Fatal("simple prompt/completion sample was incorrectly marked degraded")
	}

	complex := Sample{Messages: []Message{
		{Role: "system", Content: "Be concise"},
		{Role: "user", Content: "Question"},
		{Role: "assistant", Content: "Answer"},
	}}
	_, degraded, err = adaptSample(complex, FormatCompletion)
	if err != nil {
		t.Fatal(err)
	}
	if !degraded {
		t.Fatal("flattened multi-message sample was not marked degraded")
	}
}

func TestExportRejectsUnconfirmedDetectPolicy(t *testing.T) {
	root := t.TempDir()
	_, err := Export(context.Background(), fakeTraceStore{}, root, filepath.Join(root, "datasets"), Options{
		TraceIDs: []string{"trace-1"}, Format: FormatOpenAI, RedactionPolicy: PolicyDetect,
	})
	if err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("error = %v, want confirmation error", err)
	}
}

func BenchmarkExport1000Traces(b *testing.B) {
	store := make(fakeTraceStore, 1000)
	traceIDs := make([]string, 0, 1000)
	for index := range 1000 {
		traceID := fmt.Sprintf("trace-%04d", index)
		traceIDs = append(traceIDs, traceID)
		store[traceID] = []observability.RequestRecord{{
			ID: "request-" + traceID, TraceID: traceID, Status: "completed", Protocol: "openai",
			RequestBody:  `{"messages":[{"role":"user","content":"Write a concise test response."}]}`,
			ResponseBody: `{"choices":[{"message":{"role":"assistant","content":"This is a concise test response."}}]}`,
		}}
	}
	b.ReportAllocs()
	for b.Loop() {
		root := b.TempDir()
		if _, err := Export(context.Background(), store, root, filepath.Join(root, "datasets"), Options{
			TraceIDs: traceIDs, Format: FormatOpenAI, RedactionPolicy: PolicyRedact,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

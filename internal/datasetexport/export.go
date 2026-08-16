package datasetexport

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/opencsgs/csglite/internal/dataset"
	"github.com/opencsgs/csglite/internal/observability"
)

const (
	DirName           = "dataset-exports"
	SchemaVersion     = "1.0"
	FormatOpenAI      = "openai_messages"
	FormatShareGPT    = "sharegpt"
	FormatAlpaca      = "alpaca"
	FormatCompletion  = "prompt_completion"
	PolicyRedact      = "redact"
	PolicyExclude     = "exclude"
	PolicyDetect      = "detect"
	maxTraceSelection = 100000
)

var supportedFormats = map[string]struct{}{
	FormatOpenAI:     {},
	FormatShareGPT:   {},
	FormatAlpaca:     {},
	FormatCompletion: {},
}

var supportedPolicies = map[string]struct{}{
	PolicyRedact:  {},
	PolicyExclude: {},
	PolicyDetect:  {},
}

type TraceStore interface {
	GetTrace(context.Context, string) (observability.TraceRecord, []observability.RequestRecord, error)
}

type BatchTraceStore interface {
	VisitTraces(context.Context, []string, func(string, []observability.RequestRecord) error) error
}

type Options struct {
	TraceIDs        []string
	Format          string
	RedactionPolicy string
	Confirmed       bool
	DatasetName     string
}

type Message struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	ToolCalls  any    `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type Sample struct {
	SchemaVersion string         `json:"schema_version"`
	Messages      []Message      `json:"messages"`
	Model         string         `json:"model,omitempty"`
	Source        string         `json:"source,omitempty"`
	Metadata      SampleMetadata `json:"metadata"`
}

type SampleMetadata struct {
	TraceHash    string `json:"trace_hash"`
	Protocol     string `json:"protocol,omitempty"`
	InputTokens  int64  `json:"input_tokens,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
}

type RiskSummary struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type Preview struct {
	Selected int             `json:"selected"`
	Exported int             `json:"exported"`
	Excluded int             `json:"excluded"`
	Degraded int             `json:"degraded"`
	Risks    []RiskSummary   `json:"risks"`
	Sample   json.RawMessage `json:"sample,omitempty"`
}

type Artifact struct {
	ID          string    `json:"id"`
	DatasetID   string    `json:"dataset_id"`
	Format      string    `json:"format"`
	CreatedAt   time.Time `json:"created_at"`
	Directory   string    `json:"-"`
	ArchivePath string    `json:"-"`
	Files       []File    `json:"files"`
	Preview
}

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type candidate struct {
	sample   Sample
	traceID  string
	risks    map[string]int
	degraded bool
	excluded bool
}

type manifest struct {
	SchemaVersion   string        `json:"schema_version"`
	Format          string        `json:"format"`
	RedactionPolicy string        `json:"redaction_policy"`
	CreatedAt       time.Time     `json:"created_at"`
	Selected        int           `json:"selected"`
	Exported        int           `json:"exported"`
	Excluded        int           `json:"excluded"`
	Degraded        int           `json:"degraded"`
	Risks           []RiskSummary `json:"risks"`
	// Files deliberately excludes export-manifest.json. Including its own
	// checksum would make the manifest recursively self-referential.
	Files []File `json:"files"`
}

func NormalizeOptions(options Options) (Options, error) {
	options.Format = strings.TrimSpace(options.Format)
	if options.Format == "" {
		options.Format = FormatOpenAI
	}
	if _, ok := supportedFormats[options.Format]; !ok {
		return options, fmt.Errorf("unsupported dataset format %q", options.Format)
	}
	options.RedactionPolicy = strings.TrimSpace(options.RedactionPolicy)
	if options.RedactionPolicy == "" {
		options.RedactionPolicy = PolicyRedact
	}
	if _, ok := supportedPolicies[options.RedactionPolicy]; !ok {
		return options, fmt.Errorf("unsupported redaction policy %q", options.RedactionPolicy)
	}
	if len(options.TraceIDs) == 0 {
		return options, errors.New("at least one trace is required")
	}
	if len(options.TraceIDs) > maxTraceSelection {
		return options, fmt.Errorf("at most %d traces may be exported at once", maxTraceSelection)
	}
	seen := make(map[string]struct{}, len(options.TraceIDs))
	ids := make([]string, 0, len(options.TraceIDs))
	for _, raw := range options.TraceIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return options, errors.New("at least one valid trace is required")
	}
	options.TraceIDs = ids
	return options, nil
}

func BuildPreview(ctx context.Context, store TraceStore, options Options) (Preview, error) {
	normalized, err := NormalizeOptions(options)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{Selected: len(normalized.TraceIDs)}
	riskCounts := make(map[string]int)
	err = visitCandidates(ctx, store, normalized, func(item candidate) error {
		accumulatePreview(&preview, riskCounts, item)
		if item.excluded || len(preview.Sample) > 0 {
			return nil
		}
		sample := item.sample
		if normalized.RedactionPolicy == PolicyDetect {
			redactSample(&sample)
		}
		row, _, adaptErr := adaptSample(sample, normalized.Format)
		if adaptErr == nil {
			preview.Sample = row
		}
		return nil
	})
	if err != nil {
		return Preview{}, err
	}
	preview.Risks = sortedRisks(riskCounts)
	return preview, nil
}

func Export(ctx context.Context, store TraceStore, storageRoot, datasetDir string, options Options) (Artifact, error) {
	normalized, err := NormalizeOptions(options)
	if err != nil {
		return Artifact{}, err
	}
	if normalized.RedactionPolicy == PolicyDetect && !normalized.Confirmed {
		return Artifact{}, errors.New("exporting detected private data requires explicit confirmation")
	}
	if strings.TrimSpace(storageRoot) == "" {
		return Artifact{}, errors.New("dataset export storage root is required")
	}
	if strings.TrimSpace(datasetDir) == "" {
		return Artifact{}, errors.New("local dataset directory is required")
	}
	id := newID()
	namespace := "trace-exports"
	name := normalizedDatasetName(options.DatasetName, id)
	datasetID := namespace + "/" + name
	root := dataset.DatasetDir(filepath.Clean(datasetDir), namespace, name)
	dataDir := filepath.Join(root, "data")
	if _, err := os.Stat(root); err == nil {
		return Artifact{}, fmt.Errorf("local dataset %s already exists", datasetID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Artifact{}, fmt.Errorf("checking local dataset path: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("creating dataset export directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(root)
		}
	}()

	dataPath := filepath.Join(dataDir, "train-00000.jsonl")
	dataFile, err := os.OpenFile(dataPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Artifact{}, fmt.Errorf("creating dataset file: %w", err)
	}
	writer := bufio.NewWriter(dataFile)
	preview := Preview{Selected: len(normalized.TraceIDs)}
	riskCounts := make(map[string]int)
	exported := 0
	err = visitCandidates(ctx, store, normalized, func(item candidate) error {
		accumulatePreview(&preview, riskCounts, item)
		if item.excluded {
			return nil
		}
		row, _, adaptErr := adaptSample(item.sample, normalized.Format)
		if adaptErr != nil {
			return nil
		}
		if !utf8.Valid(row) {
			return errors.New("dataset sample is not valid UTF-8")
		}
		if _, err := writer.Write(row); err != nil {
			return fmt.Errorf("writing dataset sample: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			return fmt.Errorf("writing dataset newline: %w", err)
		}
		exported++
		return nil
	})
	if err != nil {
		_ = dataFile.Close()
		return Artifact{}, err
	}
	if err := writer.Flush(); err != nil {
		_ = dataFile.Close()
		return Artifact{}, fmt.Errorf("flushing dataset file: %w", err)
	}
	if err := dataFile.Close(); err != nil {
		return Artifact{}, fmt.Errorf("closing dataset file: %w", err)
	}
	if exported == 0 {
		return Artifact{}, errors.New("no selected traces can be exported")
	}
	preview.Exported = exported
	preview.Risks = sortedRisks(riskCounts)

	readme := datasetCard(normalized, preview)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(readme), 0o600); err != nil {
		return Artifact{}, fmt.Errorf("writing dataset card: %w", err)
	}
	files, err := collectFiles(root, []string{filepath.Join("data", "train-00000.jsonl"), "README.md"})
	if err != nil {
		return Artifact{}, err
	}
	createdAt := time.Now().UTC()
	info := manifest{
		SchemaVersion:   SchemaVersion,
		Format:          normalized.Format,
		RedactionPolicy: normalized.RedactionPolicy,
		CreatedAt:       createdAt,
		Selected:        preview.Selected,
		Exported:        preview.Exported,
		Excluded:        preview.Excluded,
		Degraded:        preview.Degraded,
		Risks:           preview.Risks,
		Files:           files,
	}
	manifestData, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return Artifact{}, fmt.Errorf("encoding dataset manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(root, "export-manifest.json"), append(manifestData, '\n'), 0o600); err != nil {
		return Artifact{}, fmt.Errorf("writing dataset manifest: %w", err)
	}
	files, err = collectFiles(root, []string{filepath.Join("data", "train-00000.jsonl"), "README.md", "export-manifest.json"})
	if err != nil {
		return Artifact{}, err
	}
	localFiles := make([]dataset.LocalDatasetFile, 0, len(files))
	var totalSize int64
	for _, file := range files {
		localFiles = append(localFiles, dataset.LocalDatasetFile{Path: file.Path, Size: file.Size, SHA256: file.SHA256})
		totalSize += file.Size
	}
	local := &dataset.LocalDataset{
		Namespace:    namespace,
		Name:         name,
		Size:         totalSize,
		Files:        filePaths(files),
		FileEntries:  localFiles,
		DownloadedAt: createdAt,
		Origin:       dataset.LocalDatasetOriginExport,
		Description:  "Training dataset exported from CSGHub Lite observability traces.",
		License:      "other",
	}
	if err := dataset.SaveManifest(datasetDir, local); err != nil {
		return Artifact{}, fmt.Errorf("registering local dataset: %w", err)
	}
	artifact := Artifact{
		ID:        id,
		DatasetID: datasetID,
		Format:    normalized.Format,
		CreatedAt: createdAt,
		Directory: root,
		Files:     files,
		Preview:   preview,
	}
	indexDir := filepath.Join(filepath.Clean(storageRoot), DirName)
	if err := os.MkdirAll(indexDir, 0o700); err != nil {
		return Artifact{}, fmt.Errorf("creating dataset export index: %w", err)
	}
	indexData, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return Artifact{}, fmt.Errorf("encoding dataset export index: %w", err)
	}
	if err := os.WriteFile(filepath.Join(indexDir, id+".json"), append(indexData, '\n'), 0o600); err != nil {
		return Artifact{}, fmt.Errorf("writing dataset export index: %w", err)
	}
	cleanup = false
	return artifact, nil
}

func LoadArtifact(storageRoot, datasetDir, id string) (Artifact, error) {
	if !validID(id) {
		return Artifact{}, errors.New("invalid dataset export ID")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Clean(storageRoot), DirName, id+".json"))
	if err != nil {
		return Artifact{}, err
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return Artifact{}, fmt.Errorf("decoding dataset export index: %w", err)
	}
	namespace, name, ok := strings.Cut(artifact.DatasetID, "/")
	if !ok || !validDatasetPart(namespace) || !validDatasetPart(name) {
		return Artifact{}, errors.New("dataset export index contains an invalid local dataset")
	}
	artifact.Directory = dataset.DatasetDir(filepath.Clean(datasetDir), namespace, name)
	return artifact, nil
}

func visitCandidates(ctx context.Context, store TraceStore, options Options, visit func(candidate) error) error {
	if store == nil {
		return errors.New("observability store is unavailable")
	}
	if visit == nil {
		return errors.New("dataset candidate visitor is required")
	}
	seen := make(map[string]struct{}, len(options.TraceIDs))
	if batchStore, ok := store.(BatchTraceStore); ok {
		if err := batchStore.VisitTraces(ctx, options.TraceIDs, func(traceID string, records []observability.RequestRecord) error {
			seen[traceID] = struct{}{}
			return visit(buildCandidate(traceID, records, options))
		}); err != nil {
			return err
		}
	} else {
		for _, traceID := range options.TraceIDs {
			_, records, err := store.GetTrace(ctx, traceID)
			if err != nil {
				continue
			}
			seen[traceID] = struct{}{}
			if err := visit(buildCandidate(traceID, records, options)); err != nil {
				return err
			}
		}
	}
	for _, traceID := range options.TraceIDs {
		if _, exists := seen[traceID]; exists {
			continue
		}
		if err := visit(candidate{traceID: traceID, excluded: true}); err != nil {
			return err
		}
	}
	return nil
}

func accumulatePreview(preview *Preview, riskCounts map[string]int, item candidate) {
	for riskType, count := range item.risks {
		riskCounts[riskType] += count
	}
	if item.excluded {
		preview.Excluded++
		return
	}
	preview.Exported++
	if item.degraded {
		preview.Degraded++
	}
}

func sortedRisks(riskCounts map[string]int) []RiskSummary {
	risks := make([]RiskSummary, 0, len(riskCounts))
	for riskType, count := range riskCounts {
		risks = append(risks, RiskSummary{Type: riskType, Count: count})
	}
	sort.Slice(risks, func(i, j int) bool { return risks[i].Type < risks[j].Type })
	return risks
}

func buildCandidate(traceID string, records []observability.RequestRecord, options Options) candidate {
	item := candidate{traceID: traceID}
	var err error
	item.sample, item.degraded, err = normalizeTrace(records)
	if err != nil {
		item.excluded = true
		return item
	}
	item.sample.Metadata.TraceHash = hashID(traceID)
	item.risks = detectRisks(item.sample)
	if len(item.risks) > 0 {
		switch options.RedactionPolicy {
		case PolicyExclude:
			item.excluded = true
		case PolicyRedact:
			redactSample(&item.sample)
		}
	}
	if !item.excluded {
		if _, degraded, adaptErr := adaptSample(item.sample, options.Format); adaptErr != nil {
			item.excluded = true
		} else {
			item.degraded = item.degraded || degraded
		}
	}
	return item
}

func normalizeTrace(records []observability.RequestRecord) (Sample, bool, error) {
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		if record.Status != "completed" || record.RequestBodyTruncated || record.ResponseBodyTruncated {
			continue
		}
		messages, degraded, err := requestMessages(record)
		if err != nil || len(messages) == 0 {
			continue
		}
		assistant, responseDegraded, err := responseMessage(record)
		if err != nil || strings.TrimSpace(contentText(assistant.Content)) == "" {
			continue
		}
		messages = append(messages, assistant)
		if !validConversation(messages) {
			continue
		}
		return Sample{
			SchemaVersion: SchemaVersion,
			Messages:      messages,
			Model:         record.Model,
			Source:        record.Source,
			Metadata: SampleMetadata{
				Protocol:     record.Protocol,
				InputTokens:  record.InputTokens,
				OutputTokens: record.OutputTokens,
			},
		}, degraded || responseDegraded, nil
	}
	return Sample{}, false, errors.New("trace has no complete training conversation")
}

func requestMessages(record observability.RequestRecord) ([]Message, bool, error) {
	var body map[string]any
	if err := json.Unmarshal([]byte(record.RequestBody), &body); err != nil {
		return nil, false, err
	}
	var messages []Message
	degraded := false
	if system, ok := body["system"]; ok {
		text, changed := safeContent(system)
		if text != "" {
			messages = append(messages, Message{Role: "system", Content: text})
		}
		degraded = degraded || changed
	}
	rawMessages, _ := body["messages"].([]any)
	if len(rawMessages) == 0 {
		if input, ok := body["input"]; ok {
			switch typed := input.(type) {
			case string:
				messages = append(messages, Message{Role: "user", Content: typed})
			case []any:
				rawMessages = typed
			}
		}
	}
	for _, raw := range rawMessages {
		value, ok := raw.(map[string]any)
		if !ok {
			degraded = true
			continue
		}
		role, _ := value["role"].(string)
		role = strings.TrimSpace(role)
		if role == "developer" {
			role = "system"
		}
		if role == "" {
			degraded = true
			continue
		}
		content, changed := safeContent(value["content"])
		message := Message{Role: role, Content: content}
		if toolCalls, ok := value["tool_calls"]; ok {
			message.ToolCalls = toolCalls
		}
		if id, ok := value["tool_call_id"].(string); ok {
			message.ToolCallID = id
		}
		if content == "" && message.ToolCalls == nil {
			continue
		}
		messages = append(messages, message)
		degraded = degraded || changed
	}
	if len(messages) == 0 {
		if prompt, ok := body["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
			messages = append(messages, Message{Role: "user", Content: prompt})
		}
	}
	return messages, degraded, nil
}

func responseMessage(record observability.RequestRecord) (Message, bool, error) {
	body := strings.TrimSpace(record.ResponseBody)
	if body == "" {
		return Message{}, false, errors.New("empty response")
	}
	if strings.Contains(body, "\ndata:") || strings.HasPrefix(body, "data:") || strings.Contains(body, "\nevent:") {
		text := streamText(body)
		if text == "" {
			return Message{}, false, errors.New("stream response has no text")
		}
		return Message{Role: "assistant", Content: text}, true, nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return Message{}, false, err
	}
	if message, ok := value["message"].(map[string]any); ok {
		return messageFromMap(message)
	}
	if choices, ok := value["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				return messageFromMap(message)
			}
			if text, ok := choice["text"].(string); ok {
				return Message{Role: "assistant", Content: text}, false, nil
			}
		}
	}
	if text, ok := value["response"].(string); ok {
		return Message{Role: "assistant", Content: text}, false, nil
	}
	if content, ok := value["content"]; ok {
		text, changed := safeContent(content)
		return Message{Role: "assistant", Content: text}, changed, nil
	}
	if output, ok := value["output"]; ok {
		text, changed := safeContent(output)
		return Message{Role: "assistant", Content: text}, changed, nil
	}
	if text, ok := value["output_text"].(string); ok {
		return Message{Role: "assistant", Content: text}, false, nil
	}
	return Message{}, false, errors.New("unsupported response payload")
}

func messageFromMap(value map[string]any) (Message, bool, error) {
	content, changed := safeContent(value["content"])
	message := Message{Role: "assistant", Content: content}
	if role, ok := value["role"].(string); ok && role != "" {
		message.Role = role
	}
	if calls, ok := value["tool_calls"]; ok {
		message.ToolCalls = calls
	}
	if content == "" && message.ToolCalls == nil {
		return Message{}, changed, errors.New("response message is empty")
	}
	return message, changed, nil
}

func safeContent(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, false
	case []any:
		var parts []string
		changed := false
		for _, raw := range typed {
			switch part := raw.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				kind, _ := part["type"].(string)
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				} else if content, ok := part["content"].(string); ok {
					parts = append(parts, content)
				} else if kind == "image_url" || kind == "image" || kind == "input_image" {
					parts = append(parts, "[image omitted]")
					changed = true
				} else {
					changed = true
				}
			default:
				changed = true
			}
		}
		return strings.Join(parts, ""), changed
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text, true
		}
		if content, ok := typed["content"]; ok {
			text, _ := safeContent(content)
			return text, true
		}
	}
	return "", value != nil
}

func streamText(body string) string {
	var out strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 1024), 2*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var value map[string]any
		if json.Unmarshal([]byte(data), &value) != nil {
			continue
		}
		appendStreamValue(&out, value)
	}
	return out.String()
}

func appendStreamValue(out *strings.Builder, value map[string]any) {
	if message, ok := value["message"].(map[string]any); ok {
		if content, ok := message["content"].(string); ok {
			out.WriteString(content)
		}
	}
	if choices, ok := value["choices"].([]any); ok {
		for _, raw := range choices {
			choice, _ := raw.(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if content, ok := delta["content"].(string); ok {
				out.WriteString(content)
			}
			if text, ok := choice["text"].(string); ok {
				out.WriteString(text)
			}
		}
	}
	if delta, ok := value["delta"].(map[string]any); ok {
		if text, ok := delta["text"].(string); ok {
			out.WriteString(text)
		}
	}
	if eventType, _ := value["type"].(string); strings.HasSuffix(eventType, ".delta") {
		if delta, ok := value["delta"].(string); ok {
			out.WriteString(delta)
		}
	}
}

func validConversation(messages []Message) bool {
	hasUser, hasAssistant := false, false
	for _, message := range messages {
		switch message.Role {
		case "user":
			hasUser = true
		case "assistant":
			hasAssistant = true
		}
	}
	return hasUser && hasAssistant && messages[len(messages)-1].Role == "assistant"
}

func adaptSample(sample Sample, format string) ([]byte, bool, error) {
	switch format {
	case FormatOpenAI:
		row, err := json.Marshal(struct {
			Messages []Message `json:"messages"`
		}{Messages: sample.Messages})
		return row, false, err
	case FormatShareGPT:
		type turn struct {
			From  string `json:"from"`
			Value string `json:"value"`
		}
		conversations := make([]turn, 0, len(sample.Messages))
		degraded := false
		for _, message := range sample.Messages {
			from := map[string]string{"system": "system", "user": "human", "assistant": "gpt", "tool": "tool"}[message.Role]
			if from == "" {
				degraded = true
				continue
			}
			if message.ToolCalls != nil {
				degraded = true
			}
			conversations = append(conversations, turn{From: from, Value: contentText(message.Content)})
		}
		if len(conversations) < 2 {
			return nil, degraded, errors.New("sample cannot be represented as ShareGPT")
		}
		row, err := json.Marshal(map[string]any{"conversations": conversations})
		return row, degraded, err
	case FormatAlpaca:
		instruction, input, output, degraded, err := flattenInstruction(sample.Messages)
		if err != nil {
			return nil, degraded, err
		}
		row, err := json.Marshal(map[string]string{"instruction": instruction, "input": input, "output": output})
		return row, true, err
	case FormatCompletion:
		instruction, input, output, degraded, err := flattenInstruction(sample.Messages)
		if err != nil {
			return nil, degraded, err
		}
		prompt := instruction
		if input != "" {
			prompt += "\n\n" + input
		}
		row, err := json.Marshal(map[string]string{"prompt": prompt, "completion": output})
		return row, degraded, err
	default:
		return nil, false, fmt.Errorf("unsupported dataset format %q", format)
	}
}

func flattenInstruction(messages []Message) (string, string, string, bool, error) {
	lastAssistant := -1
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" {
			lastAssistant = index
			break
		}
	}
	if lastAssistant < 1 {
		return "", "", "", false, errors.New("sample has no assistant output")
	}
	userIndex := -1
	for index := lastAssistant - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			userIndex = index
			break
		}
	}
	if userIndex < 0 {
		return "", "", "", false, errors.New("sample has no user instruction")
	}
	instruction := contentText(messages[userIndex].Content)
	output := contentText(messages[lastAssistant].Content)
	var context []string
	for index, message := range messages[:userIndex] {
		text := strings.TrimSpace(contentText(message.Content))
		if text != "" {
			context = append(context, fmt.Sprintf("%s: %s", message.Role, text))
		}
		if message.ToolCalls != nil || index > 0 {
			// Complex histories are deliberately flattened and reported as degraded.
		}
	}
	return instruction, strings.Join(context, "\n"), output, len(messages) > 2, nil
}

func contentText(value any) string {
	text, _ := safeContent(value)
	return text
}

var riskPatterns = []struct {
	name        string
	expression  *regexp.Regexp
	replacement string
}{
	{"secret", regexp.MustCompile(`(?i)\b(?:sk|glpat|ghp|github_pat|xox[baprs])[-_][A-Za-z0-9_-]{8,}\b`), "[REDACTED_SECRET]"},
	{"bearer_token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer [REDACTED]"},
	{"email", regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), "[REDACTED_EMAIL]"},
	{"phone", regexp.MustCompile(`(?:\+?\d[\d ()-]{7,}\d)`), "[REDACTED_PHONE]"},
	{"ip_address", regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), "[REDACTED_IP]"},
	{"cn_id", regexp.MustCompile(`\b[1-9]\d{5}(?:19|20)\d{2}(?:0[1-9]|1[0-2])(?:0[1-9]|[12]\d|3[01])\d{3}[0-9Xx]\b`), "[REDACTED_ID]"},
}

func detectRisks(sample Sample) map[string]int {
	data, _ := json.Marshal(sample.Messages)
	text := string(data)
	result := make(map[string]int)
	for _, pattern := range riskPatterns {
		result[pattern.name] = len(pattern.expression.FindAllStringIndex(text, -1))
		if result[pattern.name] == 0 {
			delete(result, pattern.name)
		}
	}
	return result
}

func redactSample(sample *Sample) {
	for index := range sample.Messages {
		sample.Messages[index].Content = redactValue(sample.Messages[index].Content)
		if sample.Messages[index].ToolCalls != nil {
			sample.Messages[index].ToolCalls = redactValue(sample.Messages[index].ToolCalls)
		}
	}
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case string:
		for _, pattern := range riskPatterns {
			typed = pattern.expression.ReplaceAllString(typed, pattern.replacement)
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = redactValue(typed[index])
		}
		return typed
	case map[string]any:
		for key := range typed {
			typed[key] = redactValue(typed[key])
		}
		return typed
	default:
		return value
	}
}

func datasetCard(options Options, preview Preview) string {
	return fmt.Sprintf(`---
pretty_name: CSGHub Lite Trace Export
license: other
task_categories:
- text-generation
---

# CSGHub Lite Trace Export

This dataset was manually exported from local CSGHub Lite observability traces.

- Format: %s
- Schema version: %s
- Exported samples: %d
- Excluded samples: %d
- Redaction policy: %s

The data may include model-generated content. Review repository visibility and
dataset contents before using it for training. The uploader is responsible for
confirming that the source data may be shared and trained on.
`, options.Format, SchemaVersion, preview.Exported, preview.Excluded, options.RedactionPolicy)
}

func collectFiles(root string, relativePaths []string) ([]File, error) {
	files := make([]File, 0, len(relativePaths))
	for _, relative := range relativePaths {
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		handle, err := os.Open(fullPath)
		if err != nil {
			return nil, fmt.Errorf("opening dataset artifact %s: %w", relative, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, handle)
		closeErr := handle.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("hashing dataset artifact %s: %w", relative, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("closing dataset artifact %s: %w", relative, closeErr)
		}
		files = append(files, File{Path: filepath.ToSlash(relative), Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))})
	}
	return files, nil
}

func hashID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}

func filePaths(files []File) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func normalizedDatasetName(value, exportID string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "traces-" + strings.TrimPrefix(exportID, "export-")[:12]
	}
	var result strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z',
			char >= '0' && char <= '9',
			char == '.', char == '_', char == '-':
			result.WriteRune(char)
		default:
			result.WriteByte('-')
		}
		if result.Len() >= 100 {
			break
		}
	}
	normalized := strings.Trim(result.String(), ".-_")
	if normalized == "" {
		return "traces-" + strings.TrimPrefix(exportID, "export-")[:12]
	}
	return normalized
}

func validDatasetPart(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func newID() string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err == nil {
		return "export-" + hex.EncodeToString(value)
	}
	return fmt.Sprintf("export-%d", time.Now().UnixNano())
}

func validID(id string) bool {
	if !strings.HasPrefix(id, "export-") || len(id) > 64 {
		return false
	}
	for _, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

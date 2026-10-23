package ctxcompress

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Protocol names the request body shape a gateway endpoint accepts.
type Protocol string

const (
	ProtocolAnthropic  Protocol = "anthropic"
	ProtocolOpenAIChat Protocol = "openai"
	ProtocolResponses  Protocol = "responses"
)

// Result describes what CompressRequest did to one request body.
type Result struct {
	// Body is the rewritten request, or the original bytes when nothing was
	// compressed.
	Body []byte
	// Changed reports whether Body differs from the input.
	Changed bool
	Stats   Stats
}

// CompressRequest rewrites the tool results inside a chat request body.
//
// Only tool output is touched: system prompts, tool definitions, user and
// assistant turns pass through unchanged. Every tool result in the history is
// compressed, not just the newest one, and the rewrite is a pure function of
// the result's own text. A client resends the original history on each turn,
// so the same result compresses to the same bytes every time and the prefix a
// provider or llama-server caches stays identical from one request to the
// next.
func CompressRequest(protocol Protocol, body []byte, opts Options) (Result, error) {
	result := Result{Body: body}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return result, fmt.Errorf("decoding request body: %w", err)
	}

	walker := requestWalker{opts: opts.withDefaults()}
	switch protocol {
	case ProtocolAnthropic:
		walker.anthropic(root)
	case ProtocolOpenAIChat:
		walker.openAIChat(root)
	case ProtocolResponses:
		walker.responses(root)
	default:
		return result, fmt.Errorf("unsupported protocol %q", protocol)
	}
	result.Stats = walker.stats
	if !walker.changed {
		return result, nil
	}

	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(root); err != nil {
		return result, fmt.Errorf("encoding request body: %w", err)
	}
	result.Body = bytes.TrimRight(out.Bytes(), "\n")
	result.Changed = true
	return result, nil
}

type requestWalker struct {
	opts    Options
	stats   Stats
	changed bool
	// seen maps each tool result already walked to the call that produced
	// it, for pointing a repeat back at the first copy.
	seen map[string]string
}

// duplicateFormat replaces a tool result identical to an earlier one. Only
// later copies are replaced, so the decision never depends on what comes
// after a result and the prefix before it stays cacheable.
const duplicateFormat = "[Identical to the earlier output of tool call %s.]"

// dedupable reports whether a repeat of this kind may be replaced by a
// pointer to its first copy. File contents, source code and diffs never
// are: an agent editing a file works from the read it looks at last, and
// must find the text there rather than a reference to an older turn.
func dedupable(kind Kind) bool {
	switch kind {
	case KindJSON, KindSearch, KindLog, KindText:
		return true
	}
	return false
}

// compress runs one tool result's text through Text and records the outcome.
func (w *requestWalker) compress(text, toolName, callID string) (string, bool) {
	out, kind := Text(text, toolName, w.opts)
	if dedupable(kind) {
		if first, ok := w.seen[text]; ok && first != "" && first != callID {
			out = fmt.Sprintf(duplicateFormat, first)
			w.stats.add(KindDuplicate, text, out)
			w.changed = true
			return out, true
		}
		if w.seen == nil {
			w.seen = map[string]string{}
		}
		if _, ok := w.seen[text]; !ok {
			w.seen[text] = callID
		}
	}
	changed := out != text
	w.stats.add(kind, text, out)
	if changed {
		w.changed = true
	}
	return out, changed
}

// anthropic handles /v1/messages: tool_result blocks inside user messages,
// whose content is either a string or a list of text/image blocks.
func (w *requestWalker) anthropic(root map[string]any) {
	messages, _ := root["messages"].([]any)
	names := map[string]string{}
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		blocks, _ := message["content"].([]any)
		for _, rawBlock := range blocks {
			block, _ := rawBlock.(map[string]any)
			switch block["type"] {
			case "tool_use", "server_tool_use":
				if id, ok := block["id"].(string); ok {
					names[id], _ = block["name"].(string)
				}
			case "tool_result":
				if isError, _ := block["is_error"].(bool); isError {
					continue
				}
				id, _ := block["tool_use_id"].(string)
				w.rewriteContent(block, "content", names[id], id)
			}
		}
	}
}

// openAIChat handles /v1/chat/completions: messages with role "tool".
func (w *requestWalker) openAIChat(root map[string]any) {
	messages, _ := root["messages"].([]any)
	names := map[string]string{}
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		if calls, ok := message["tool_calls"].([]any); ok {
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, _ := call["function"].(map[string]any)
				if id, ok := call["id"].(string); ok {
					names[id], _ = function["name"].(string)
				}
			}
		}
		if message["role"] != "tool" {
			continue
		}
		id, _ := message["tool_call_id"].(string)
		name := names[id]
		if name == "" {
			name, _ = message["name"].(string)
		}
		w.rewriteContent(message, "content", name, id)
	}
}

// responses handles /v1/responses: function_call_output and
// custom_tool_call_output items in the input list.
func (w *requestWalker) responses(root map[string]any) {
	items, _ := root["input"].([]any)
	names := map[string]string{}
	for _, rawItem := range items {
		item, _ := rawItem.(map[string]any)
		switch item["type"] {
		case "function_call", "custom_tool_call", "local_shell_call":
			if id, ok := item["call_id"].(string); ok {
				names[id], _ = item["name"].(string)
			}
		case "function_call_output", "custom_tool_call_output":
			id, _ := item["call_id"].(string)
			w.rewriteContent(item, "output", names[id], id)
		}
	}
}

// rewriteContent compresses holder[key] when it is a string, or each text
// part when it is a list of content parts. Non-text parts are left alone.
func (w *requestWalker) rewriteContent(holder map[string]any, key, toolName, callID string) {
	switch value := holder[key].(type) {
	case string:
		if out, changed := w.compress(value, toolName, callID); changed {
			holder[key] = out
		}
	case []any:
		for _, rawPart := range value {
			part, _ := rawPart.(map[string]any)
			switch part["type"] {
			case "text", "input_text", "output_text":
				text, ok := part["text"].(string)
				if !ok {
					continue
				}
				if out, changed := w.compress(text, toolName, callID); changed {
					part["text"] = out
				}
			}
		}
	}
}

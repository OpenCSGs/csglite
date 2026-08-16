export type TraceSectionKind =
  | "system"
  | "user"
  | "assistant"
  | "thinking"
  | "toolUse"
  | "toolResult"
  | "raw";

export interface TracePayloadSection {
  kind: TraceSectionKind;
  content: string;
  name?: string;
  callID?: string;
  isError?: boolean;
  streamKey?: string;
}

export interface TraceFlowRequest {
  id: string;
  request_body?: string;
  response_body?: string;
}

export interface TraceFlowSection extends TracePayloadSection {
  requestID: string;
  requestIndex: number;
  phase: "input" | "output";
}

function stringifyContent(value: unknown): string {
  if (typeof value === "string") return value;
  if (value == null) return "";
  if (Array.isArray(value)) {
    return value
      .map((part) => {
        if (typeof part === "string") return part;
        if (!part || typeof part !== "object") return "";
        const item = part as Record<string, unknown>;
        return stringifyContent(item.text ?? item.content ?? item.input_text ?? item.output_text);
      })
      .filter(Boolean)
      .join("\n");
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function stringifyToolArguments(value: unknown): string {
  if (value && typeof value === "object" && !Array.isArray(value) && Object.keys(value as Record<string, unknown>).length === 0) {
    return "";
  }
  return stringifyContent(value);
}

function pushMessageSections(sections: TracePayloadSection[], message: Record<string, unknown>, streaming = false) {
  const role = String(message.role || "");
  const kind: TraceSectionKind = role === "system" || role === "developer"
    ? "system"
    : role === "assistant"
      ? "assistant"
      : role === "tool"
        ? "toolResult"
        : "user";
  const rawContent = message.content;
  const content = Array.isArray(rawContent)
    ? stringifyContent(rawContent.filter((part) => {
        if (!part || typeof part !== "object") return true;
        const type = String((part as Record<string, unknown>).type || "");
        return !["thinking", "reasoning", "redacted_thinking", "tool_use", "tool_result", "function_call", "function_call_output", "functionCall", "functionResponse"].includes(type);
      }))
    : stringifyContent(rawContent);
  if (content) {
    sections.push({
      kind,
      content,
      name: role === "tool" ? String(message.name || "") : undefined,
      callID: role === "tool" ? String(message.tool_call_id ?? message.tool_use_id ?? "") : undefined,
      isError: role === "tool" ? Boolean(message.is_error) : undefined,
    });
  }

  const contentParts = Array.isArray(message.content) ? message.content : [];
  for (const rawPart of contentParts) {
    if (!rawPart || typeof rawPart !== "object") continue;
    const part = rawPart as Record<string, unknown>;
    const type = String(part.type || "");
    const geminiCall = part.functionCall && typeof part.functionCall === "object"
      ? part.functionCall as Record<string, unknown>
      : null;
    const geminiResult = part.functionResponse && typeof part.functionResponse === "object"
      ? part.functionResponse as Record<string, unknown>
      : null;
    if (geminiCall) {
      sections.push({
        kind: "toolUse",
        name: String(geminiCall.name || ""),
        callID: String(geminiCall.id || ""),
        content: stringifyContent(geminiCall.args),
      });
    } else if (geminiResult) {
      sections.push({
        kind: "toolResult",
        name: String(geminiResult.name || ""),
        callID: String(geminiResult.id || ""),
        content: stringifyContent(geminiResult.response),
      });
    } else if (type === "thinking" || type === "reasoning" || type === "redacted_thinking") {
      const text = stringifyContent(part.thinking ?? part.text ?? part.data);
      if (text) sections.push({ kind: "thinking", content: text });
    } else if (type === "tool_use" || type === "function_call" || type === "functionCall") {
      sections.push({
        kind: "toolUse",
        name: String(part.name || ""),
        callID: String(part.id ?? part.call_id ?? ""),
        content: stringifyContent(part.input ?? part.arguments ?? part.args),
      });
    } else if (type === "tool_result" || type === "function_call_output" || type === "functionResponse") {
      const response = part.response && typeof part.response === "object"
        ? part.response as Record<string, unknown>
        : part;
      sections.push({
        kind: "toolResult",
        name: String(response.name || ""),
        callID: String(part.tool_use_id ?? part.call_id ?? response.id ?? ""),
        content: stringifyContent(part.content ?? part.output ?? response.response ?? response.result),
        isError: Boolean(part.is_error ?? response.is_error),
      });
    }
  }

  const reasoning = stringifyContent(message.reasoning_content ?? message.reasoning);
  if (reasoning) sections.push({ kind: "thinking", content: reasoning });

  const toolCalls = Array.isArray(message.tool_calls) ? message.tool_calls : [];
  for (const call of toolCalls) {
    if (!call || typeof call !== "object") continue;
    const value = call as Record<string, unknown>;
    const fn = value.function && typeof value.function === "object"
      ? value.function as Record<string, unknown>
      : value;
    sections.push({
      kind: "toolUse",
      name: String(fn.name || value.name || ""),
      callID: String(value.id ?? value.call_id ?? (value.index != null ? `index:${value.index}` : "")),
      content: stringifyToolArguments(fn.arguments ?? value.input ?? value.arguments),
      streamKey: streaming && value.index != null ? `openai:${value.index}` : undefined,
    });
  }
}

function sectionsFromObject(value: Record<string, unknown>, output: boolean): TracePayloadSection[] {
  const sections: TracePayloadSection[] = [];
  if (!output && typeof value.system === "string" && value.system) {
    sections.push({ kind: "system", content: value.system });
  }
  if (!output && typeof value.prompt === "string" && value.prompt) {
    sections.push({ kind: "user", content: value.prompt });
  }

  const messages = Array.isArray(value.messages) ? value.messages : [];
  for (const message of messages) {
    if (message && typeof message === "object") pushMessageSections(sections, message as Record<string, unknown>);
  }

  if (!output && Array.isArray(value.input)) {
    for (const inputItem of value.input) {
      if (inputItem && typeof inputItem === "object") {
        const item = inputItem as Record<string, unknown>;
        if (item.role || item.type === "message") {
          pushMessageSections(sections, item);
        } else if (item.type === "function_call_output" || item.type === "tool_result") {
          sections.push({
            kind: "toolResult",
            callID: String(item.call_id ?? item.tool_use_id ?? ""),
            content: stringifyContent(item.output ?? item.content),
            isError: Boolean(item.is_error),
          });
        }
      } else {
        const input = stringifyContent(inputItem);
        if (input) sections.push({ kind: "user", content: input });
      }
    }
  } else if (!output && value.input != null) {
    const input = stringifyContent(value.input);
    if (input) sections.push({ kind: "user", content: input });
  }

  const geminiContents = !output && Array.isArray(value.contents) ? value.contents : [];
  for (const content of geminiContents) {
    if (!content || typeof content !== "object") continue;
    const item = content as Record<string, unknown>;
    const role = String(item.role || "") === "model" ? "assistant" : String(item.role || "user");
    pushMessageSections(sections, { role, content: item.parts });
  }

  const directMessage = value.message;
  if (directMessage && typeof directMessage === "object") {
    const message = directMessage as Record<string, unknown>;
    pushMessageSections(sections, output && !message.role ? { role: "assistant", ...message } : message);
  }

  const choices = Array.isArray(value.choices) ? value.choices : [];
  for (const choice of choices) {
    if (!choice || typeof choice !== "object") continue;
    const item = choice as Record<string, unknown>;
    const message = item.message ?? item.delta;
    if (message && typeof message === "object") {
      pushMessageSections(sections, { role: "assistant", ...(message as Record<string, unknown>) }, item.delta != null);
    } else {
      const text = stringifyContent(item.text);
      if (text) sections.push({ kind: "assistant", content: text });
    }
  }

  const contentItems = Array.isArray(value.content) ? value.content : [];
  for (const contentItem of contentItems) {
    if (!contentItem || typeof contentItem !== "object") continue;
    const item = contentItem as Record<string, unknown>;
    const type = String(item.type || "");
    if (type === "thinking" || type === "reasoning") {
      sections.push({ kind: "thinking", content: stringifyContent(item.thinking ?? item.text) });
    } else if (type === "tool_use" || type === "function_call") {
      sections.push({
        kind: "toolUse",
        name: String(item.name || ""),
        callID: String(item.id ?? item.call_id ?? ""),
        content: stringifyContent(item.input ?? item.arguments),
      });
    } else if (type === "tool_result" || type === "function_call_output") {
      sections.push({
        kind: "toolResult",
        name: String(item.name || ""),
        callID: String(item.tool_use_id ?? item.call_id ?? ""),
        content: stringifyContent(item.content ?? item.output),
        isError: Boolean(item.is_error),
      });
    } else {
      const text = stringifyContent(item.text ?? item.content);
      if (text) sections.push({ kind: output ? "assistant" : "user", content: text });
    }
  }

  const outputItems = Array.isArray(value.output) ? value.output : [];
  for (const outputItem of outputItems) {
    if (!outputItem || typeof outputItem !== "object") continue;
    const item = outputItem as Record<string, unknown>;
    if (item.type === "function_call") {
      sections.push({
        kind: "toolUse",
        name: String(item.name || ""),
        callID: String(item.call_id ?? item.id ?? ""),
        content: stringifyContent(item.arguments),
      });
      continue;
    }
    if (item.type === "function_call_output" || item.type === "tool_result") {
      sections.push({
        kind: "toolResult",
        name: String(item.name || ""),
        callID: String(item.call_id ?? item.tool_use_id ?? ""),
        content: stringifyContent(item.output ?? item.content),
        isError: Boolean(item.is_error),
      });
      continue;
    }
    const content = stringifyContent(item.content ?? item.text);
    if (content) sections.push({ kind: "assistant", content });
  }

  const outputText = stringifyContent(value.output_text ?? value.response);
  if (outputText) sections.push({ kind: "assistant", content: outputText });

  const eventType = String(value.type || "");
  const delta = value.delta;
  if (output && typeof delta === "string" && eventType.includes("output_text")) {
    sections.push({ kind: "assistant", content: delta });
  } else if (output && delta && typeof delta === "object") {
    const deltaValue = delta as Record<string, unknown>;
    const deltaType = String(deltaValue.type || eventType);
    if (deltaType.includes("thinking")) {
      const text = stringifyContent(deltaValue.thinking ?? deltaValue.text);
      if (text) sections.push({ kind: "thinking", content: text });
    } else if (deltaType.includes("input_json")) {
      const text = stringifyContent(deltaValue.partial_json);
      if (text) sections.push({
        kind: "toolUse",
        content: text,
        streamKey: `anthropic:${String(value.index ?? "")}`,
      });
    } else {
      const text = stringifyContent(deltaValue.text ?? deltaValue.content);
      if (text) sections.push({ kind: "assistant", content: text });
    }
  }
  const contentBlock = value.content_block;
  if (output && contentBlock && typeof contentBlock === "object") {
    const block = contentBlock as Record<string, unknown>;
    if (block.type === "tool_use") {
      sections.push({
        kind: "toolUse",
        name: String(block.name || ""),
        callID: String(block.id ?? ""),
        content: stringifyToolArguments(block.input),
        streamKey: `anthropic:${String(value.index ?? "")}`,
      });
    } else if (block.type === "tool_result") {
      sections.push({
        kind: "toolResult",
        callID: String(block.tool_use_id ?? ""),
        content: stringifyContent(block.content),
        isError: Boolean(block.is_error),
      });
    }
  }
  return sections.filter((section) => section.content || section.name);
}

function parseStreamingObjects(body: string): Record<string, unknown>[] {
  const objects: Record<string, unknown>[] = [];
  for (const rawLine of body.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line || line === "data: [DONE]" || line.startsWith("event:")) continue;
    const payload = line.startsWith("data:") ? line.slice(5).trim() : line;
    if (!payload.startsWith("{")) continue;
    try {
      const value = JSON.parse(payload);
      if (value && typeof value === "object") objects.push(value as Record<string, unknown>);
    } catch {
      // Ignore partial stream frames.
    }
  }
  return objects;
}

function mergeStreamingSections(sections: TracePayloadSection[]): TracePayloadSection[] {
  const merged: TracePayloadSection[] = [];
  const toolCallIndexes = new Map<string, number>();
  for (const section of sections) {
    const previous = merged[merged.length - 1];
    if (section.kind === "toolUse") {
      const key = section.streamKey || (section.callID ? `call:${section.callID}` : "");
      const existingIndex = key ? toolCallIndexes.get(key) : undefined;
      if (existingIndex !== undefined) {
        const existing = merged[existingIndex];
        existing.content += section.content;
        existing.name ||= section.name;
        if (!existing.callID || existing.callID.startsWith("index:")) existing.callID = section.callID || existing.callID;
        continue;
      }
      if (key) toolCallIndexes.set(key, merged.length);
      const { streamKey: _, ...cleanSection } = section;
      merged.push(cleanSection);
      continue;
    }
    if (
      previous &&
      previous.kind === section.kind &&
      previous.name === section.name &&
      (section.kind === "assistant" || section.kind === "thinking")
    ) {
      previous.content += section.content;
    } else {
      const { streamKey: _, ...cleanSection } = section;
      merged.push(cleanSection);
    }
  }
  return merged;
}

export function parseTracePayload(body: string | undefined, output: boolean): TracePayloadSection[] {
  const trimmed = body?.trim();
  if (!trimmed) return [];
  try {
    const value = JSON.parse(trimmed);
    if (value && typeof value === "object") {
      const sections = sectionsFromObject(value as Record<string, unknown>, output);
      return sections.length ? sections : [{ kind: "raw", content: stringifyContent(value) }];
    }
  } catch {
    // Streaming responses are decoded frame by frame below.
  }
  if (output) {
    const streamSections = parseStreamingObjects(trimmed).flatMap((value) => sectionsFromObject(value, true));
    if (streamSections.length) return mergeStreamingSections(streamSections);
  }
  return [{ kind: "raw", content: trimmed }];
}

function sectionSignature(section: TracePayloadSection): string {
  return [
    section.kind,
    section.callID || "",
    section.name || "",
    section.content,
    section.isError ? "error" : "",
  ].join("\u0000");
}

function sectionCounts(sections: TracePayloadSection[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const section of sections) {
    const signature = sectionSignature(section);
    counts.set(signature, (counts.get(signature) || 0) + 1);
  }
  return counts;
}

// Builds a conversation-like span flow from cumulative request histories.
// Most chat APIs resend the full history on every request, so only occurrences
// added since the preceding request are emitted. Prior model outputs repeated
// in the next request are also suppressed.
export function buildTraceFlowSections(requests: TraceFlowRequest[]): TraceFlowSection[] {
  const flow: TraceFlowSection[] = [];
  let previousInputCounts = new Map<string, number>();
  const priorOutputCounts = new Map<string, number>();

  requests.forEach((request, requestIndex) => {
    const input = parseTracePayload(request.request_body, false);
    const currentOccurrences = new Map<string, number>();
    for (const section of input) {
      const signature = sectionSignature(section);
      const occurrence = (currentOccurrences.get(signature) || 0) + 1;
      currentOccurrences.set(signature, occurrence);
      if (occurrence <= (previousInputCounts.get(signature) || 0)) continue;

      const priorOutputCount = priorOutputCounts.get(signature) || 0;
      if (priorOutputCount > 0) {
        priorOutputCounts.set(signature, priorOutputCount - 1);
        continue;
      }
      flow.push({ ...section, requestID: request.id, requestIndex, phase: "input" });
    }
    previousInputCounts = sectionCounts(input);

    const output = parseTracePayload(request.response_body, true);
    for (const section of output) {
      flow.push({ ...section, requestID: request.id, requestIndex, phase: "output" });
      const signature = sectionSignature(section);
      priorOutputCounts.set(signature, (priorOutputCounts.get(signature) || 0) + 1);
    }
  });
  return flow;
}

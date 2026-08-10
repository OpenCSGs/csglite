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

function pushMessageSections(sections: TracePayloadSection[], message: Record<string, unknown>) {
  const role = String(message.role || "");
  const kind: TraceSectionKind = role === "system" || role === "developer"
    ? "system"
    : role === "assistant"
      ? "assistant"
      : role === "tool"
        ? "toolResult"
        : "user";
  const content = stringifyContent(message.content);
  if (content) sections.push({ kind, content });

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
      content: stringifyContent(fn.arguments ?? value.input ?? value.arguments),
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

  if (!output && value.input != null) {
    const input = stringifyContent(value.input);
    if (input) sections.push({ kind: "user", content: input });
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
      pushMessageSections(sections, { role: "assistant", ...(message as Record<string, unknown>) });
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
      sections.push({ kind: "toolUse", name: String(item.name || ""), content: stringifyContent(item.input ?? item.arguments) });
    } else if (type === "tool_result" || type === "function_call_output") {
      sections.push({ kind: "toolResult", content: stringifyContent(item.content ?? item.output) });
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
      sections.push({ kind: "toolUse", name: String(item.name || ""), content: stringifyContent(item.arguments) });
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
      if (text) sections.push({ kind: "toolUse", content: text });
    } else {
      const text = stringifyContent(deltaValue.text ?? deltaValue.content);
      if (text) sections.push({ kind: "assistant", content: text });
    }
  }
  const contentBlock = value.content_block;
  if (output && contentBlock && typeof contentBlock === "object") {
    const block = contentBlock as Record<string, unknown>;
    if (block.type === "tool_use") {
      sections.push({ kind: "toolUse", name: String(block.name || ""), content: stringifyContent(block.input) });
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
  for (const section of sections) {
    const previous = merged[merged.length - 1];
    if (previous && previous.kind === section.kind && previous.name === section.name && (section.kind === "assistant" || section.kind === "thinking")) {
      previous.content += section.content;
    } else {
      merged.push({ ...section });
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

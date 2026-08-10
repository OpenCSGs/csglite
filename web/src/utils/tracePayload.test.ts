import { describe, expect, it } from "vitest";
import { parseTracePayload } from "./tracePayload";

describe("trace payload parser", () => {
  it("extracts system, user, and tool input spans", () => {
    const sections = parseTracePayload(JSON.stringify({
      model: "test",
      messages: [
        { role: "system", content: "Be concise" },
        { role: "user", content: "Weather?" },
        { role: "assistant", tool_calls: [{ function: { name: "weather", arguments: "{\"city\":\"Shanghai\"}" } }] },
        { role: "tool", content: "Sunny" },
      ],
    }), false);
    expect(sections.map((section) => section.kind)).toEqual(["system", "user", "toolUse", "toolResult"]);
    expect(sections[2].name).toBe("weather");
  });

  it("combines streamed OpenAI output", () => {
    const sections = parseTracePayload([
      'data: {"choices":[{"delta":{"content":"Hello "}}]}',
      'data: {"choices":[{"delta":{"content":"world"}}]}',
      "data: [DONE]",
    ].join("\n"), true);
    expect(sections).toEqual([{ kind: "assistant", content: "Hello world" }]);
  });

  it("extracts Anthropic thinking and text output", () => {
    const sections = parseTracePayload(JSON.stringify({
      content: [
        { type: "thinking", thinking: "Check facts" },
        { type: "text", text: "Answer" },
      ],
    }), true);
    expect(sections.map((section) => section.kind)).toEqual(["thinking", "assistant"]);
  });

  it("extracts Lite and Anthropic streamed output", () => {
    const lite = parseTracePayload('data: {"message":{"content":"Hello"}}', true);
    expect(lite).toEqual([{ kind: "assistant", content: "Hello" }]);

    const anthropic = parseTracePayload([
      'data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"Check"}}',
      'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Done"}}',
    ].join("\n"), true);
    expect(anthropic.map((section) => section.kind)).toEqual(["thinking", "assistant"]);
  });
});

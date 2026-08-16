import { describe, expect, it } from "vitest";
import { buildTraceFlowSections, parseTracePayload } from "./tracePayload";

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

  it("combines streamed OpenAI tool argument fragments into one call", () => {
    const sections = parseTracePayload([
      'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_search","function":{"name":"search","arguments":"{"}}]}}]}',
      'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\\"query\\":\\"models\\""}}]}}]}',
      'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"}"}}]}}]}',
      "data: [DONE]",
    ].join("\n"), true);
    expect(sections).toEqual([{
      kind: "toolUse",
      name: "search",
      callID: "call_search",
      content: "{\"query\":\"models\"}",
    }]);
  });

  it("combines streamed Anthropic tool argument fragments into one call", () => {
    const sections = parseTracePayload([
      'data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_search","name":"search","input":{}}}',
      'data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\\"query\\":"}}',
      'data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\\"models\\"}"}}',
    ].join("\n"), true);
    expect(sections).toEqual([{
      kind: "toolUse",
      name: "search",
      callID: "toolu_search",
      content: "{\"query\":\"models\"}",
    }]);
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

  it("correlates OpenAI tool calls and results by call ID", () => {
    const sections = parseTracePayload(JSON.stringify({
      messages: [
        {
          role: "assistant",
          tool_calls: [{
            id: "call_weather",
            type: "function",
            function: { name: "weather", arguments: "{\"city\":\"Shanghai\"}" },
          }],
        },
        { role: "tool", tool_call_id: "call_weather", name: "weather", content: "{\"temperature\":28}" },
      ],
    }), false);
    expect(sections).toEqual([
      {
        kind: "toolUse",
        name: "weather",
        callID: "call_weather",
        content: "{\"city\":\"Shanghai\"}",
      },
      {
        kind: "toolResult",
        name: "weather",
        callID: "call_weather",
        content: "{\"temperature\":28}",
        isError: false,
      },
    ]);
  });

  it("builds a deduplicated multi-request tool flow", () => {
    const firstMessages = [
      { role: "user", content: "What is the weather?" },
    ];
    const toolCall = {
      role: "assistant",
      tool_calls: [{
        id: "call_weather",
        type: "function",
        function: { name: "weather", arguments: "{\"city\":\"Shanghai\"}" },
      }],
    };
    const flow = buildTraceFlowSections([
      {
        id: "request-1",
        request_body: JSON.stringify({ messages: firstMessages }),
        response_body: JSON.stringify({ choices: [{ message: toolCall }] }),
      },
      {
        id: "request-2",
        request_body: JSON.stringify({
          messages: [
            ...firstMessages,
            toolCall,
            { role: "tool", tool_call_id: "call_weather", content: "Sunny" },
          ],
        }),
        response_body: JSON.stringify({
          choices: [{ message: { role: "assistant", content: "It is sunny." } }],
        }),
      },
    ]);
    expect(flow.map(({ kind, callID, requestID }) => ({ kind, callID, requestID }))).toEqual([
      { kind: "user", callID: undefined, requestID: "request-1" },
      { kind: "toolUse", callID: "call_weather", requestID: "request-1" },
      { kind: "toolResult", callID: "call_weather", requestID: "request-2" },
      { kind: "assistant", callID: undefined, requestID: "request-2" },
    ]);
  });

  it("extracts Anthropic and Gemini tool results", () => {
    const anthropic = parseTracePayload(JSON.stringify({
      messages: [{
        role: "user",
        content: [{
          type: "tool_result",
          tool_use_id: "toolu_1",
          content: [{ type: "text", text: "Anthropic result" }],
        }],
      }],
    }), false);
    expect(anthropic).toEqual([{
      kind: "toolResult",
      name: "",
      callID: "toolu_1",
      content: "Anthropic result",
      isError: false,
    }]);

    const gemini = parseTracePayload(JSON.stringify({
      contents: [{
        role: "user",
        parts: [{ functionResponse: { id: "gemini_1", name: "lookup", response: { value: 42 } } }],
      }],
    }), false);
    expect(gemini[0]).toMatchObject({
      kind: "toolResult",
      callID: "gemini_1",
      name: "lookup",
    });
  });
});

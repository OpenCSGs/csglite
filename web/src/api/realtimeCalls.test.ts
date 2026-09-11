import { afterEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import { endRealtimeCall, startRealtimeCall } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function sdpResponse(body = "v=0 answer", location = "/v1/realtime/calls/sess_abc") {
  return new Response(body, {
    status: 200,
    headers: { "Content-Type": "application/sdp", Location: location },
  });
}

async function sentSession(fetchMock: ReturnType<typeof vi.fn>): Promise<Record<string, any>> {
  const form = fetchMock.mock.calls[0][1].body as FormData;
  return JSON.parse(form.get("session") as string);
}

describe("realtime call client", () => {
  it("sends the offer and the session, and returns the call id to hang up with", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(sdpResponse()));
    vi.stubGlobal("fetch", fetchMock);

    const call = await startRealtimeCall("v=0 offer", {
      asrModel: "acme/asr",
      ttsModel: "acme/tts",
      voice: "af_heart",
    });

    expect(call).toEqual({ answer: "v=0 answer", callId: "sess_abc" });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/v1/realtime/calls");
    expect(init.method).toBe("POST");
    const form = init.body as FormData;
    expect(form.get("sdp")).toBe("v=0 offer");
    expect(await sentSession(fetchMock)).toEqual({
      type: "realtime",
      audio: {
        input: { transcription: { model: "acme/asr" } },
        output: { model: "acme/tts", voice: "af_heart" },
      },
    });
  });

  // Either half of the pipeline may be left out, which is how a machine that
  // cannot hold two models at once tests one of them.
  it("omits the half of the pipeline that has no model", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(sdpResponse()));
    vi.stubGlobal("fetch", fetchMock);

    await startRealtimeCall("v=0 offer", { ttsModel: "acme/tts" });
    expect(await sentSession(fetchMock)).toEqual({
      type: "realtime",
      audio: { output: { model: "acme/tts" } },
    });

    fetchMock.mockClear();
    await startRealtimeCall("v=0 offer", { asrModel: "acme/asr" });
    expect(await sentSession(fetchMock)).toEqual({
      type: "realtime",
      audio: { input: { transcription: { model: "acme/asr" } } },
    });
  });

  it("reports the server's message when the call is refused", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: { message: "no such model", type: "invalid_request_error" } }),
      { status: 400, headers: { "Content-Type": "application/json" } },
    )));

    await expect(startRealtimeCall("v=0 offer", { ttsModel: "acme/tts" }))
      .rejects.toThrow("no such model");
  });

  it("hangs up by call id, and does nothing without one", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await endRealtimeCall("sess_abc");
    expect(fetchMock).toHaveBeenCalledWith("/v1/realtime/calls/sess_abc", expect.objectContaining({ method: "DELETE" }));

    fetchMock.mockClear();
    await endRealtimeCall("");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

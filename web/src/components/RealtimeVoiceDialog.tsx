import { useEffect, useRef, useState } from "preact/hooks";
import { endRealtimeCall, getTTSVoices, startRealtimeCall, type SpeechVoice } from "../api/client";
import { t } from "../i18n";

export interface RealtimeVoiceModel {
  key: string;
  label: string;
}

type CallState = "idle" | "connecting" | "live" | "ended";

interface TranscriptLine {
  id: number;
  text: string;
  final: boolean;
}

// The events this dialog reacts to. Everything else is displayed in the log
// rather than interpreted, so an unexpected event is visible instead of silent.
interface RealtimeEvent {
  type: string;
  delta?: string;
  transcript?: string;
  status?: string;
  response?: { status?: string };
  error?: { message?: string };
}

/**
 * RealtimeVoiceDialog drives a full-duplex voice call against
 * POST /v1/realtime/calls: the microphone goes up as an Opus track, synthesised
 * speech comes back on a media track, and events travel on the oai-events
 * DataChannel. It is the only place in the UI where recognition and synthesis
 * run at the same time, which is why each half is optional -- a machine that
 * cannot hold two models at once can still test one of them here.
 */
export function RealtimeVoiceDialog({
  asrModels,
  ttsModels,
  initialASRModel,
  initialTTSModel,
  onClose,
}: {
  asrModels: RealtimeVoiceModel[];
  ttsModels: RealtimeVoiceModel[];
  initialASRModel?: string;
  initialTTSModel?: string;
  onClose: () => void;
}) {
  const [asrModel, setASRModel] = useState(initialASRModel || asrModels[0]?.key || "");
  const [ttsModel, setTTSModel] = useState(initialTTSModel || ttsModels[0]?.key || "");
  const [voices, setVoices] = useState<SpeechVoice[]>([]);
  const [voice, setVoice] = useState("");
  const [state, setState] = useState<CallState>("idle");
  const [error, setError] = useState("");
  const [lines, setLines] = useState<TranscriptLine[]>([]);
  const [speaking, setSpeaking] = useState(false);
  const [text, setText] = useState("");

  const peerRef = useRef<RTCPeerConnection | null>(null);
  const channelRef = useRef<RTCDataChannel | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const callIdRef = useRef("");
  const lineIdRef = useRef(0);

  // Voices are listed per model, and listing them loads the model, so this runs
  // only when a synthesis model is actually selected.
  useEffect(() => {
    if (!ttsModel) {
      setVoices([]);
      setVoice("");
      return;
    }
    let cancelled = false;
    getTTSVoices(ttsModel)
      .then((resp) => {
        if (cancelled) return;
        const list = resp.voices || [];
        setVoices(list);
        setVoice(list.length > 0 ? list[0].id : "");
      })
      .catch(() => {
        if (cancelled) return;
        // A model whose voices cannot be listed still speaks with its default.
        setVoices([]);
        setVoice("");
      });
    return () => { cancelled = true; };
  }, [ttsModel]);

  const appendLine = (text: string, final: boolean) => {
    setLines((prev) => {
      const next = [...prev];
      // Partial transcripts replace the previous partial rather than piling up:
      // a delta is the current best guess at the whole utterance, not an
      // append-only prefix.
      if (next.length > 0 && !next[next.length - 1].final) {
        next[next.length - 1] = { ...next[next.length - 1], text, final };
        return next;
      }
      next.push({ id: ++lineIdRef.current, text, final });
      return next;
    });
  };

  const handleEvent = (ev: RealtimeEvent) => {
    switch (ev.type) {
      case "conversation.item.input_audio_transcription.delta":
        if (ev.delta) appendLine(ev.delta, false);
        break;
      case "conversation.item.input_audio_transcription.completed":
        if (ev.transcript) appendLine(ev.transcript, true);
        break;
      case "output_audio_buffer.started":
        setSpeaking(true);
        break;
      case "output_audio_buffer.stopped":
        setSpeaking(false);
        break;
      case "error":
        setError(ev.error?.message || t("realtime.unknownError"));
        break;
    }
  };

  const stop = () => {
    channelRef.current?.close();
    channelRef.current = null;
    peerRef.current?.close();
    peerRef.current = null;
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
    if (audioRef.current) audioRef.current.srcObject = null;
    const callId = callIdRef.current;
    callIdRef.current = "";
    if (callId) void endRealtimeCall(callId);
    setSpeaking(false);
  };

  // A call holds a microphone and one or two loaded models, so leaving the
  // dialog must end it rather than leave it running in the background.
  useEffect(() => stop, []);

  const start = async () => {
    if (!asrModel && !ttsModel) {
      setError(t("realtime.pickAModel"));
      return;
    }
    setError("");
    setLines([]);
    setState("connecting");
    try {
      const peer = new RTCPeerConnection();
      peerRef.current = peer;
      peer.ontrack = (event) => {
        if (audioRef.current) audioRef.current.srcObject = event.streams[0];
      };
      peer.onconnectionstatechange = () => {
        if (peer.connectionState === "connected") setState("live");
        if (peer.connectionState === "failed" || peer.connectionState === "closed") {
          setState("ended");
        }
      };

      const channel = peer.createDataChannel("oai-events");
      channelRef.current = channel;
      channel.onmessage = (event) => {
        try {
          handleEvent(JSON.parse(event.data));
        } catch {
          // An event that does not parse is not worth tearing the call down for.
        }
      };

      if (asrModel) {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        streamRef.current = stream;
        stream.getAudioTracks().forEach((track) => peer.addTrack(track, stream));
      } else {
        // Without a microphone the call still needs an audio section to receive
        // the synthesised speech on.
        peer.addTransceiver("audio", { direction: "recvonly" });
      }

      const offer = await peer.createOffer();
      await peer.setLocalDescription(offer);
      // The answer carries its candidates, so the offer must too: this exchange
      // has no channel for trickling them afterwards.
      await new Promise<void>((resolve) => {
        if (peer.iceGatheringState === "complete") {
          resolve();
          return;
        }
        const done = () => {
          if (peer.iceGatheringState === "complete") {
            peer.removeEventListener("icegatheringstatechange", done);
            resolve();
          }
        };
        peer.addEventListener("icegatheringstatechange", done);
        // Proceed with whatever was gathered rather than hanging on a network
        // that never finishes.
        setTimeout(resolve, 3000);
      });

      const call = await startRealtimeCall(peer.localDescription?.sdp || offer.sdp || "", {
        asrModel: asrModel || undefined,
        ttsModel: ttsModel || undefined,
        voice: voice || undefined,
      });
      callIdRef.current = call.callId;
      await peer.setRemoteDescription({ type: "answer", sdp: call.answer });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      stop();
      setState("idle");
    }
  };

  const send = (payload: Record<string, unknown>) => {
    const channel = channelRef.current;
    if (!channel || channel.readyState !== "open") {
      setError(t("realtime.notConnected"));
      return false;
    }
    channel.send(JSON.stringify(payload));
    return true;
  };

  const speak = () => {
    const instructions = text.trim();
    if (!instructions) return;
    if (send({ type: "response.create", response: { instructions } })) setText("");
  };

  const interrupt = () => {
    send({ type: "response.cancel" });
    send({ type: "output_audio_buffer.clear" });
  };

  const hangUp = () => {
    stop();
    setState("ended");
  };

  const live = state === "live";
  const statusLabel = live
    ? t("realtime.statusLive")
    : state === "connecting"
      ? t("realtime.statusConnecting")
      : state === "ended"
        ? t("realtime.statusEnded")
        : t("realtime.statusIdle");

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={(e) => { if (e.target === e.currentTarget) { stop(); onClose(); } }}
    >
      <div class="bg-white rounded-2xl shadow-2xl max-w-2xl w-full mx-4 max-h-[85vh] flex flex-col">
        <div class="flex items-center justify-between px-6 py-4 border-b border-gray-100">
          <div>
            <h2 class="text-lg font-semibold text-gray-900">{t("realtime.title")}</h2>
            <p class="text-xs text-gray-500 mt-0.5">{t("realtime.subtitle")}</p>
          </div>
          <button
            class="text-gray-400 hover:text-gray-600 text-xl leading-none"
            onClick={() => { stop(); onClose(); }}
            aria-label={t("realtime.close")}
          >
            ×
          </button>
        </div>

        <div class="px-6 py-4 space-y-4 overflow-y-auto">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label class="text-sm">
              <span class="block text-gray-600 mb-1">{t("realtime.asrModel")}</span>
              <select
                class="w-full border border-gray-200 rounded-lg px-3 py-2 text-sm disabled:bg-gray-50"
                value={asrModel}
                disabled={live || state === "connecting"}
                onChange={(e) => setASRModel((e.target as HTMLSelectElement).value)}
              >
                <option value="">{t("realtime.noModel")}</option>
                {asrModels.map((m) => <option value={m.key} key={m.key}>{m.label}</option>)}
              </select>
            </label>
            <label class="text-sm">
              <span class="block text-gray-600 mb-1">{t("realtime.ttsModel")}</span>
              <select
                class="w-full border border-gray-200 rounded-lg px-3 py-2 text-sm disabled:bg-gray-50"
                value={ttsModel}
                disabled={live || state === "connecting"}
                onChange={(e) => setTTSModel((e.target as HTMLSelectElement).value)}
              >
                <option value="">{t("realtime.noModel")}</option>
                {ttsModels.map((m) => <option value={m.key} key={m.key}>{m.label}</option>)}
              </select>
            </label>
          </div>

          {voices.length > 0 && (
            <label class="text-sm block">
              <span class="block text-gray-600 mb-1">{t("realtime.voice")}</span>
              <select
                class="w-full border border-gray-200 rounded-lg px-3 py-2 text-sm disabled:bg-gray-50"
                value={voice}
                disabled={live || state === "connecting"}
                onChange={(e) => setVoice((e.target as HTMLSelectElement).value)}
              >
                {voices.map((v) => (
                  <option value={v.id} key={v.id}>{v.label || v.id}</option>
                ))}
              </select>
            </label>
          )}

          {asrModel && ttsModel && !live && (
            <p class="text-xs text-amber-700 bg-amber-50 border border-amber-100 rounded-lg px-3 py-2">
              {t("realtime.twoModelsWarning")}
            </p>
          )}

          <div class="flex items-center gap-3">
            {!live ? (
              <button
                class="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium disabled:opacity-50"
                disabled={state === "connecting"}
                onClick={() => void start()}
              >
                {state === "connecting" ? t("realtime.connecting") : t("realtime.start")}
              </button>
            ) : (
              <button
                class="px-4 py-2 rounded-lg bg-red-600 text-white text-sm font-medium"
                onClick={hangUp}
              >
                {t("realtime.hangUp")}
              </button>
            )}
            <span class={`text-sm ${live ? "text-green-600" : "text-gray-500"}`}>{statusLabel}</span>
            {speaking && <span class="text-sm text-blue-600">{t("realtime.speaking")}</span>}
          </div>

          {error && (
            <p class="text-sm text-red-600 bg-red-50 border border-red-100 rounded-lg px-3 py-2">{error}</p>
          )}

          {ttsModel && (
            <div class="space-y-2">
              <span class="block text-sm text-gray-600">{t("realtime.speakText")}</span>
              <div class="flex gap-2">
                <input
                  class="flex-1 border border-gray-200 rounded-lg px-3 py-2 text-sm"
                  value={text}
                  placeholder={t("realtime.speakPlaceholder")}
                  disabled={!live}
                  onInput={(e) => setText((e.target as HTMLInputElement).value)}
                  onKeyDown={(e) => { if (e.key === "Enter") speak(); }}
                />
                <button
                  class="px-3 py-2 rounded-lg bg-gray-900 text-white text-sm disabled:opacity-40"
                  disabled={!live || !text.trim()}
                  onClick={speak}
                >
                  {t("realtime.speak")}
                </button>
                <button
                  class="px-3 py-2 rounded-lg border border-gray-200 text-sm disabled:opacity-40"
                  disabled={!live || !speaking}
                  onClick={interrupt}
                >
                  {t("realtime.interrupt")}
                </button>
              </div>
            </div>
          )}

          {asrModel && (
            <div class="space-y-1">
              <span class="block text-sm text-gray-600">{t("realtime.transcript")}</span>
              <div class="border border-gray-100 rounded-lg bg-gray-50 px-3 py-2 min-h-[80px] max-h-48 overflow-y-auto text-sm">
                {lines.length === 0 ? (
                  <span class="text-gray-400">{live ? t("realtime.listening") : t("realtime.transcriptEmpty")}</span>
                ) : (
                  lines.map((line) => (
                    <p key={line.id} class={line.final ? "text-gray-900" : "text-gray-500 italic"}>{line.text}</p>
                  ))
                )}
              </div>
            </div>
          )}

          {/* The remote track plays here; it carries the synthesised speech. */}
          <audio ref={audioRef} autoPlay class="hidden" />
        </div>
      </div>
    </div>
  );
}

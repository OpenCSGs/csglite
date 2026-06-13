import MarkdownIt from "markdown-it";
import { useEffect, useRef, useState } from "preact/hooks";
import { signal, computed } from "@preact/signals";
import {
  getTags, getPs, streamChat, getCloudAuthStatus, saveCloudToken,
  listConversations, searchConversations, getConversation, createConversation, updateConversation, deleteConversation,
  getSettings, createImageGenerationJob, getImageGenerationJob, cancelImageGenerationJob, getASRRuntimeStatus, transcribeAudioStream,
} from "../api/client";
import type {
  ModelInfo, ChatMessage, ContentPart, CloudAuthStatus,
  ConversationMeta, Conversation,
  ChatMessageMeta, WebSearchResult,
} from "../api/client";
import { t, locale } from "../i18n";
import { parseReasoningText } from "../reasoning";
import { buildChatContextMessages } from "../chatContext";
import { isImageGenerationModel, isImageToImageModel, stripDataURL } from "../utils/imageModels";

const availableModels = signal<ModelInfo[]>([]);
const selectedModelKey = signal("");
const conversationMetas = signal<ConversationMeta[]>([]);
const conversationSearchQuery = signal("");
const activeConversation = signal<Conversation | null>(null);
const activeSessionId = signal("");
const inputText = signal("");
const isGenerating = signal(false);
const showSettings = signal(false);
const showSidebar = signal(true);
const showCloudAuthDialog = signal(false);
const openConversationMenuId = signal("");
const cloudAuth = signal<CloudAuthStatus | null>(null);
const cloudTokenInput = signal("");
const cloudAuthError = signal("");
const isSavingCloudToken = signal(false);
const webSearchEnabled = signal(true);
const webSearchAvailable = signal(false);
const streamingSources = signal<WebSearchResult[]>([]);
const cloudProviderName = signal("");
const cloudGatewayURL = signal("");

function hasCloudAuth(status: CloudAuthStatus | null | undefined): boolean {
  return Boolean(status?.authenticated || status?.has_api_key);
}

function openExternalURL(url?: string) {
  if (!url) return;
  window.open(url, "_blank", "noopener,noreferrer");
}

const systemPrompt = signal("");
const defaultChatTemperature = 0.95;
const kimiChatTemperature = 0.6;
const temperature = signal(defaultChatTemperature);
const topP = signal(0.75);
const maxTokens = signal(4096);

const streamingContent = signal("");
const searchingQuery = signal("");
const searchPlanningQuery = signal("");
const searchSkippedReason = signal("");
const chatError = signal("");
const pendingImages = signal<PendingImage[]>([]);
const pendingAudio = signal<PendingAudio | null>(null);
const audioPreviews = signal<Record<string, LocalAudioPreview>>({});
const isRecordingAudio = signal(false);
const contextStorageKey = "csghub.chat.num_ctx";
const contextLengthSteps = [4096, 8192, 16384, 32768, 65536, 131072, 262144];
const contextLengthLabels = ["4k", "8k", "16k", "32k", "64k", "128k", "256k"];
const parallelStorageKey = "csghub.chat.num_parallel";
const parallelSteps = [1, 2, 4, 8];
const selectedModelStorageKey = "csghub.chat.selected_model";
const modelContextBoundaryStorageKey = "csghub.chat.model_context_boundary";
const webSearchStorageKey = "csghub.chat.web_search.enabled";
const legacyWebSearchModeStorageKey = "csghub.chat.web_search.mode";
const providersChangedEvent = "csghub:providers-changed";

function modelKey(model: Pick<ModelInfo, "model" | "name" | "source">): string {
  return `${model.source || "local"}:${model.model || model.name}`;
}

function isKimiFamilyModel(model?: Pick<ModelInfo, "model" | "name" | "source"> | null): boolean {
  if (!model) return false;
  const name = (model.model || model.name || "").toLowerCase();
  if (name.startsWith("kimi-") || name.startsWith("moonshot-")) return true;
  const source = (model.source || "").toLowerCase();
  return source.includes("kimi") || source.includes("moonshot");
}

function defaultTemperatureForModel(model?: Pick<ModelInfo, "model" | "name" | "source"> | null): number {
  return isKimiFamilyModel(model) ? kimiChatTemperature : defaultChatTemperature;
}

function applyModelSamplingDefaults(model?: Pick<ModelInfo, "model" | "name" | "source"> | null) {
  temperature.value = defaultTemperatureForModel(model);
}

function isASRModel(model?: Pick<ModelInfo, "pipeline_tag" | "input_modalities" | "output_modalities"> | null): boolean {
  const pipelineTag = (model?.pipeline_tag || "").toLowerCase();
  return pipelineTag === "automatic-speech-recognition" ||
    Boolean(model?.input_modalities?.includes("audio")) ||
    Boolean(model?.output_modalities?.includes("transcription"));
}

type ChatModelMode = "chat" | "vision" | "image" | "asr";

function getChatModelMode(model?: ModelInfo | null): ChatModelMode {
  if (!model) return "chat";
  if (isImageGenerationModel(model)) return "image";
  if (isASRModel(model)) return "asr";
  if (model.input_modalities?.includes("image")) return "vision";
  if (model.pipeline_tag === "image-text-to-text" && (model.source === "cloud" || model.has_mmproj === true)) {
    return "vision";
  }
  return "chat";
}

function modelLabel(model: ModelInfo): string {
  const label = model.label || model.display_name || model.name;
  const source = model.source || "local";
  if (source === "cloud") {
    const provider = model.provider || cloudProviderName.value || t("chat.cloud");
    return `${label} [${provider}]`;
  }
  if (source.startsWith("provider:")) {
    return label;
  }
  return `${label} [${t("chat.local")}]`;
}

function configuredCloudProviderName(): string {
  return cloudProviderName.value || t("chat.cloud");
}

function isCloudAuthErrorMessage(message: string): boolean {
  return /(AUTH-ERR-1|AUTH-ERR-5|login first|inference error 401|Error 401|Cloud login required|login expired|save an API Key|Failed to load OpenCSG built-in API Key)/i.test(message);
}

function localizeChatErrorMessage(message: string, providerName = configuredCloudProviderName()): string {
  if (/Cloud login required|save an API Key/i.test(message)) {
    return t("chat.cloudAuthRequired", providerName);
  }
  if (/Failed to load OpenCSG built-in API Key/i.test(message)) {
    return t("chat.cloudBuiltinAPIKeyFailed", providerName);
  }
  return message;
}

function readSelectedModelKey(): string {
  try {
    return localStorage.getItem(selectedModelStorageKey) || "";
  } catch {
    return "";
  }
}

function saveSelectedModelKey(key: string) {
  try {
    if (key) {
      localStorage.setItem(selectedModelStorageKey, key);
    } else {
      localStorage.removeItem(selectedModelStorageKey);
    }
  } catch {
    /* ignore storage failures */
  }
}

interface ModelContextBoundary {
  model: string;
  start: number;
}

function readModelContextBoundaries(): Record<string, ModelContextBoundary> {
  try {
    const raw = localStorage.getItem(modelContextBoundaryStorageKey);
    if (!raw) return {};
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function saveModelContextBoundaries(boundaries: Record<string, ModelContextBoundary>) {
  try {
    localStorage.setItem(modelContextBoundaryStorageKey, JSON.stringify(boundaries));
  } catch {
    /* ignore storage failures */
  }
}

function setModelContextBoundary(conversationId: string, model: string, start: number) {
  if (!conversationId || !model) return;
  const boundaries = readModelContextBoundaries();
  boundaries[conversationId] = { model, start: Math.max(0, start) };
  saveModelContextBoundaries(boundaries);
}

function clearModelContextBoundary(conversationId: string) {
  if (!conversationId) return;
  const boundaries = readModelContextBoundaries();
  if (!(conversationId in boundaries)) return;
  delete boundaries[conversationId];
  saveModelContextBoundaries(boundaries);
}

function contextStartIndexForModel(conv: Conversation, currentModelKey: string): number {
  if (!currentModelKey || conv.messages.length === 0) {
    return 0;
  }
  if (conv.model && conv.model !== currentModelKey) {
    return conv.messages.length;
  }
  const boundary = readModelContextBoundaries()[conv.id];
  if (boundary?.model !== currentModelKey) {
    return 0;
  }
  if (!Number.isFinite(boundary.start) || boundary.start <= 0 || boundary.start > conv.messages.length) {
    return 0;
  }
  return boundary.start;
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const timer = window.setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      window.clearTimeout(timer);
      reject(new DOMException("Aborted", "AbortError"));
    }, { once: true });
  });
}

function readWebSearchEnabled(defaultValue: boolean): boolean {
  try {
    const raw = localStorage.getItem(webSearchStorageKey);
    if (raw === "1") return true;
    if (raw === "0") return false;
    const legacyMode = localStorage.getItem(legacyWebSearchModeStorageKey);
    if (legacyMode === "off") return false;
    if (legacyMode === "auto" || legacyMode === "fast" || legacyMode === "always") return true;
  } catch {
    /* ignore storage failures */
  }
  return defaultValue;
}

function saveWebSearchEnabled(enabled: boolean) {
  try {
    localStorage.setItem(webSearchStorageKey, enabled ? "1" : "0");
  } catch {
    /* ignore storage failures */
  }
}

function estimateTokens(text: string): number {
  const cjkChars = (text.match(/[\u3400-\u9fff\uf900-\ufaff\u3040-\u30ff]/g) || []).length;
  const nonCjk = text.replace(/[\u3400-\u9fff\uf900-\ufaff\u3040-\u30ff]/g, "");
  const wordLike = (nonCjk.match(/[A-Za-z0-9_]+|[^\sA-Za-z0-9_]/g) || []).length;
  return Math.max(1, cjkChars + Math.ceil(wordLike * 1.25));
}

function buildResponseMeta(text: string, startedAt: number): ChatMessageMeta {
  const durationMs = Math.max(1, Date.now() - startedAt);
  const tokens = estimateTokens(stripReasoningForStats(text));
  const seconds = durationMs / 1000;
  return {
    tokens,
    speed: tokens / Math.max(seconds, 0.001),
    duration_ms: durationMs,
    estimated: true,
  };
}

function stripReasoningForStats(text: string): string {
  const parsed = parseReasoningText(text);
  return parsed.answer || text;
}

function formatResponseMeta(meta: ChatMessageMeta): string {
  const tokens = meta.tokens ?? 0;
  const speed = meta.speed ?? 0;
  const duration = formatDuration(meta.duration_ms || 0);
  return t("chat.responseMeta", tokens, speed.toFixed(speed >= 10 ? 0 : 1), duration);
}

function formatDuration(durationMs: number): string {
  const seconds = durationMs / 1000;
  if (seconds < 60) {
    return `${seconds.toFixed(seconds >= 10 ? 0 : 1)}s`;
  }
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  return `${mins}m ${String(secs).padStart(2, "0")}s`;
}

const maxStackedSourceIcons = 5;

function sourceFaviconCandidates(url: string): string[] {
  try {
    const origin = new URL(url).origin;
    return [
      `${origin}/favicon.ico`,
      `${origin}/favicon.png`,
      `${origin}/apple-touch-icon.png`,
    ];
  } catch {
    return [];
  }
}

function DefaultSourceIcon() {
  return (
    <svg class="h-3.5 w-3.5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1"
      />
    </svg>
  );
}

const sourceIconShellClass =
  "relative inline-flex h-6 w-6 flex-shrink-0 items-center justify-center overflow-hidden rounded-full border-2 border-white bg-gray-50 shadow-sm transition-transform hover:z-20 hover:scale-110";

function SourceFaviconLink({ source }: { source: WebSearchResult }) {
  const candidates = source.url ? sourceFaviconCandidates(source.url) : [];
  const [candidateIdx, setCandidateIdx] = useState(0);
  const useDefault = candidateIdx >= candidates.length;
  const title = source.title || source.url || t("chat.sources");
  const content = useDefault ? (
    <DefaultSourceIcon />
  ) : (
    <img
      src={candidates[candidateIdx]}
      alt=""
      class="h-full w-full object-cover"
      onError={() => setCandidateIdx((current) => current + 1)}
    />
  );

  if (!source.url) {
    return (
      <span class={sourceIconShellClass} title={title}>
        {content}
      </span>
    );
  }

  return (
    <a
      href={source.url}
      target="_blank"
      rel="noopener noreferrer"
      title={title}
      class={`${sourceIconShellClass} hover:ring-2 hover:ring-blue-100`}
    >
      {content}
    </a>
  );
}

function MessageSourceLinks({ sources }: { sources: WebSearchResult[] }) {
  if (sources.length === 0) return null;
  const visible = sources.slice(0, maxStackedSourceIcons);
  const overflow = sources.length - visible.length;

  return (
    <div
      class="flex items-center border-l border-gray-200 pl-2"
      role="group"
      aria-label={t("chat.sources")}
    >
      <div class="flex items-center">
        {visible.map((source, idx) => (
          <div
            key={`${source.url || source.title}-${idx}`}
            class={idx > 0 ? "-ml-2" : ""}
            style={{ zIndex: visible.length - idx }}
          >
            <SourceFaviconLink source={source} />
          </div>
        ))}
        {overflow > 0 && (
          <span
            class="-ml-2 relative inline-flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-full border-2 border-white bg-gray-100 text-[10px] font-medium text-gray-500 shadow-sm"
            style={{ zIndex: 0 }}
            title={t("chat.moreSources", overflow)}
          >
            +{overflow}
          </span>
        )}
      </div>
    </div>
  );
}

const markdown = new MarkdownIt({
  breaks: true,
  html: false,
  linkify: true,
  typographer: true,
});

function MarkdownContent({ text }: { text: string }) {
  return <div class="chat-markdown" dangerouslySetInnerHTML={{ __html: markdown.render(text) }} />;
}

interface EmbeddingChatObject {
  object?: string;
  embedding: number[] | string;
  index: number;
}

interface EmbeddingChatResponse {
  object?: string;
  model?: string;
  data: EmbeddingChatObject[];
  usage?: {
    prompt_tokens?: number;
    total_tokens?: number;
  };
}

function parseEmbeddingChatResponse(text: string): EmbeddingChatResponse | null {
  try {
    const parsed = JSON.parse(text) as Partial<EmbeddingChatResponse>;
    if (!parsed || !Array.isArray(parsed.data)) return null;
    const data = parsed.data.filter((item): item is EmbeddingChatObject => {
      if (!item || typeof item !== "object") return false;
      const candidate = item as Partial<EmbeddingChatObject>;
      return typeof candidate.index === "number" && (Array.isArray(candidate.embedding) || typeof candidate.embedding === "string");
    });
    if (data.length === 0) return null;
    return { ...parsed, data } as EmbeddingChatResponse;
  } catch {
    return null;
  }
}

function embeddingDimension(embedding: number[] | string): string {
  if (Array.isArray(embedding)) return String(embedding.length);
  return t("chat.embeddingEncoded");
}

const EMBEDDING_ROWS_PER_COLUMN = 19;
const EMBEDDING_MAX_COLUMNS = 4;

function formatEmbeddingGridNumber(value: number): string {
  return Number.isFinite(value) ? Number(value).toFixed(3) : String(value);
}

function embeddingColumnOffsets(length: number): number[] {
  if (length <= EMBEDDING_ROWS_PER_COLUMN) return [0];
  const columns =
    length < 256 ? 2 : length < 1024 ? 2 : length < 2048 ? 3 : EMBEDDING_MAX_COLUMNS;
  const segmentSize = Math.floor(length / columns);
  return Array.from({ length: columns }, (_, column) => column * segmentSize);
}

function embeddingMaxAbs(values: number[]): number {
  let max = 0;
  for (const value of values) {
    if (Number.isFinite(value)) max = Math.max(max, Math.abs(value));
  }
  return max;
}

function embeddingValueStyle(value: number, maxAbs: number): Record<string, string> {
  if (maxAbs <= 0 || !Number.isFinite(value)) return {};
  const ratio = Math.abs(value) / maxAbs;
  if (ratio < 0.35) return {};
  const alpha = Math.min(0.55, 0.12 + ratio * 0.43);
  if (value > 0) return { backgroundColor: `rgba(34, 197, 94, ${alpha})` };
  return { backgroundColor: `rgba(251, 113, 133, ${alpha})` };
}

function EmbeddingVectorGrid({ embedding }: { embedding: number[] | string }) {
  if (typeof embedding === "string") {
    return (
      <div class="rounded-lg border border-gray-100 bg-gray-50/50 px-3 py-2 text-xs">
        <div class="font-medium text-gray-500">{t("chat.embeddingEncoded")}</div>
        <div class="mt-1 font-mono text-[11px] break-all text-gray-700">{embedding}</div>
      </div>
    );
  }

  const maxAbs = embeddingMaxAbs(embedding);
  const offsets = embeddingColumnOffsets(embedding.length);

  return (
    <div
      class="grid gap-x-6 gap-y-0"
      style={{ gridTemplateColumns: `repeat(${offsets.length}, minmax(0, 1fr))` }}
    >
      {offsets.map((start) => (
        <div class="min-w-[7.5rem]" key={start}>
          {Array.from({ length: EMBEDDING_ROWS_PER_COLUMN }, (_, row) => {
            const index = start + row;
            if (index >= embedding.length) return null;
            const value = embedding[index];
            return (
              <div class="flex items-stretch" key={index}>
                <span class="w-11 shrink-0 py-0.5 pl-0.5 font-mono text-[11px] tabular-nums text-gray-400">
                  {index}
                </span>
                <span
                  class="min-w-0 flex-1 rounded-sm py-0.5 pr-1 font-mono text-xs tabular-nums text-gray-900"
                  style={embeddingValueStyle(value, maxAbs)}
                >
                  {formatEmbeddingGridNumber(value)}
                </span>
              </div>
            );
          })}
        </div>
      ))}
    </div>
  );
}

function EmbeddingDataTable({ response }: { response: EmbeddingChatResponse }) {
  const multi = response.data.length > 1;
  return (
    <div class="overflow-hidden rounded-xl border border-gray-200 bg-white">
      <div class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-3">
        <div>
          <div class="text-sm font-semibold text-gray-900">{t("chat.embeddingData")}</div>
          {response.model && <div class="mt-0.5 font-mono text-xs text-gray-500">{response.model}</div>}
        </div>
        {response.usage && (
          <div class="text-xs text-gray-500">
            {t("chat.embeddingUsage", String(response.usage.prompt_tokens ?? 0), String(response.usage.total_tokens ?? response.usage.prompt_tokens ?? 0))}
          </div>
        )}
      </div>
      <div class="divide-y divide-gray-100">
        {response.data.map((item) => (
          <div class="px-4 py-3" key={item.index}>
            {multi && (
              <div class="mb-2 flex flex-wrap gap-x-3 gap-y-0.5 font-mono text-[11px] text-gray-500">
                <span>
                  {t("chat.embeddingIndex")} {item.index}
                </span>
                <span>{item.object || "embedding"}</span>
                <span>
                  {t("chat.embeddingDimensions")} {embeddingDimension(item.embedding)}
                </span>
              </div>
            )}
            {!multi && Array.isArray(item.embedding) && (
              <div class="mb-2 font-mono text-[11px] text-gray-500">
                {t("chat.embeddingDimensions")} {item.embedding.length}
              </div>
            )}
            <EmbeddingVectorGrid embedding={item.embedding} />
          </div>
        ))}
      </div>
    </div>
  );
}

function thinkingPreview(text: string): string {
  const lines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean);
  const preview = lines[lines.length - 1] || text.trim();
  const chars = Array.from(preview);
  return chars.length > 80 ? `${chars.slice(0, 80).join("")}...` : preview;
}

const selectedModelInfo = computed(() =>
  availableModels.value.find((x) => modelKey(x) === selectedModelKey.value)
);

function setAvailableModels(models: ModelInfo[]) {
  availableModels.value = models;
  if (selectedModelKey.value && models.some((x) => modelKey(x) === selectedModelKey.value)) {
    const current = models.find((x) => modelKey(x) === selectedModelKey.value);
    if (isKimiFamilyModel(current)) {
      applyModelSamplingDefaults(current);
    }
    return;
  }
  const savedModelKey = readSelectedModelKey();
  if (savedModelKey && models.some((x) => modelKey(x) === savedModelKey)) {
    selectedModelKey.value = savedModelKey;
    const saved = models.find((x) => modelKey(x) === savedModelKey);
    if (isKimiFamilyModel(saved)) {
      applyModelSamplingDefaults(saved);
    }
    return;
  }
  if (models.length === 0) {
    selectedModelKey.value = "";
    return;
  }

  const localModels = models.filter((x) => (x.source || "local") === "local");
  const gguf = localModels.filter((x) => x.format === "gguf");
  const fallback = gguf[0] || localModels[0] || models[0];
  if (fallback) {
    selectedModelKey.value = modelKey(fallback);
    saveSelectedModelKey(selectedModelKey.value);
    applyModelSamplingDefaults(fallback);
  }
}

const selectedModelMode = computed(() => getChatModelMode(selectedModelInfo.value));

function normalizeImage(file: File): Promise<{ full: string; thumb: string }> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const dataURL = reader.result as string;
      resolve({ full: dataURL, thumb: dataURL });
    };
    reader.onerror = () => reject(new Error("Failed to read file"));
    reader.readAsDataURL(file);
  });
}

interface PendingImage {
  full: string;
  thumb: string;
}

interface PendingAudio {
  file: File;
  name: string;
  size: number;
  objectUrl: string;
  recorded?: boolean;
}

interface LocalAudioPreview {
  name: string;
  size: number;
  objectUrl: string;
  recorded?: boolean;
}

function messagePreviewKey(conversationID: string, messageIndex: number): string {
  return `${conversationID}:${messageIndex}`;
}

function setAudioPreview(key: string, preview: LocalAudioPreview) {
  const current = audioPreviews.value[key];
  if (current?.objectUrl) URL.revokeObjectURL(current.objectUrl);
  audioPreviews.value = { ...audioPreviews.value, [key]: preview };
}

function clearAudioPreviews() {
  Object.values(audioPreviews.value).forEach((preview) => {
    if (preview.objectUrl) URL.revokeObjectURL(preview.objectUrl);
  });
  audioPreviews.value = {};
}

function preferredAudioMimeType(): string {
  if (typeof MediaRecorder === "undefined") return "";
  const candidates = ["audio/webm;codecs=opus", "audio/webm", "audio/mp4", "audio/ogg;codecs=opus"];
  return candidates.find((mime) => MediaRecorder.isTypeSupported(mime)) || "";
}

function extensionForAudioMimeType(mime: string): string {
  if (mime.includes("mp4")) return "m4a";
  if (mime.includes("ogg")) return "ogg";
  return "webm";
}

function setPendingAudio(next: PendingAudio | null) {
  const current = pendingAudio.value;
  if (current?.objectUrl) URL.revokeObjectURL(current.objectUrl);
  pendingAudio.value = next;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function readNumCtx(): number | undefined {
  try {
    const raw = localStorage.getItem(contextStorageKey);
    const n = Number(raw);
    if (Number.isFinite(n) && n >= 1024) return n;
  } catch {
    /* ignore */
  }
  return undefined;
}

function defaultNumCtx(): number {
  return readNumCtx() || 8192;
}

function normalizeNumCtx(v: number | undefined): number {
  if (!v) return defaultNumCtx();
  for (const s of contextLengthSteps) {
    if (s === v) return v;
  }
  return defaultNumCtx();
}

function readNumParallel(): number | undefined {
  try {
    const raw = localStorage.getItem(parallelStorageKey);
    const n = Number(raw);
    if (parallelSteps.includes(n)) return n;
  } catch {
    /* ignore */
  }
  return undefined;
}

function defaultNumParallel(): number {
  return readNumParallel() || 4;
}

function relativeTime(dateStr: string): string {
  const now = Date.now();
  const then = new Date(dateStr).getTime();
  const diffMs = now - then;
  const mins = Math.floor(diffMs / 60000);
  if (mins < 1) return t("chat.justNow");
  if (mins < 60) return t("chat.minutesAgo", mins);
  const hours = Math.floor(mins / 60);
  if (hours < 24) return t("chat.hoursAgo", hours);
  return t("chat.daysAgo", Math.floor(hours / 24));
}

async function refreshConversationList() {
  try {
    const query = conversationSearchQuery.value.trim();
    const metas = query ? await searchConversations(query) : await listConversations();
    conversationMetas.value = metas;
  } catch {
    /* ignore */
  }
}

async function loadConversation(id: string) {
  try {
    const conv = await getConversation(id);
    activeConversation.value = conv;
    activeSessionId.value = conv.id;
  } catch {
    /* ignore */
  }
}

async function saveCurrentConversation() {
  const conv = activeConversation.value;
  if (!conv) return;
  try {
    await updateConversation(conv.id, {
      title: conv.title,
      model: conv.model,
      messages: conv.messages,
      settings: conv.settings,
    });
    refreshConversationList();
  } catch {
    /* non-fatal */
  }
}

async function migrateLocalStorage() {
  try {
    const raw = localStorage.getItem("csghub-chat-sessions");
    if (!raw) return;
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) return;

    for (const session of parsed) {
      if (!session.messages || session.messages.length === 0) continue;
      await createConversation({
        title: session.title || "Imported Chat",
        messages: session.messages,
        settings: {
          num_ctx: session.numCtx || defaultNumCtx(),
          num_parallel: session.numParallel || defaultNumParallel(),
        },
      });
    }
    localStorage.removeItem("csghub-chat-sessions");
  } catch {
    /* ignore migration errors */
  }
}

const SCROLL_NEAR_BOTTOM_PX = 96;
const recordingMinDurationMs = 800;
const recordingSilenceStopMs = 1500;
const recordingMaxDurationMs = 30000;
const recordingVoiceThreshold = 0.025;

function isNearScrollBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= SCROLL_NEAR_BOTTOM_PX;
}

export function Chat() {
  const messagesRef = useRef<HTMLDivElement>(null);
  const stickToBottomRef = useRef(true);
  const abortRef = useRef<AbortController | null>(null);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const recordingChunksRef = useRef<BlobPart[]>([]);
  const recordingStreamRef = useRef<MediaStream | null>(null);
  const recordingStopTimerRef = useRef<number | null>(null);
  const recordingVadFrameRef = useRef<number | null>(null);
  const recordingAudioContextRef = useRef<AudioContext | null>(null);
  void locale.value;

  const refreshCloudAuth = async (): Promise<CloudAuthStatus> => {
    const status = await getCloudAuthStatus();
    cloudAuth.value = status;
    return status;
  };

  const openCloudAuthDialog = async (message = "") => {
    cloudAuthError.value = message;
    showCloudAuthDialog.value = true;
    try {
      await refreshCloudAuth();
    } catch {
      /* ignore */
    }
  };

  const handleModelChange = (nextKey: string) => {
    const conv = activeConversation.value;
    if (conv && conv.messages.length > 0 && conv.model !== nextKey) {
      setModelContextBoundary(conv.id, nextKey, conv.messages.length);
    }
    selectedModelKey.value = nextKey;
    saveSelectedModelKey(nextKey);
    const model = availableModels.value.find((x) => modelKey(x) === nextKey);
    applyModelSamplingDefaults(model);
    const nextMode = getChatModelMode(model);
    const acceptsImageInput = nextMode === "vision" || isImageToImageModel(model);
    if (isASRModel(model) || !acceptsImageInput) {
      pendingImages.value = [];
    }
    if (!isASRModel(model)) {
      setPendingAudio(null);
    }
    if (model?.source === "cloud" && !hasCloudAuth(cloudAuth.value)) {
      void openCloudAuthDialog(t("chat.cloudLoginRequired", model.provider || configuredCloudProviderName()));
    }
  };

  const handleSaveCloudToken = async () => {
    const token = cloudTokenInput.value.trim();
    if (!token) {
      cloudAuthError.value = t("chat.cloudTokenEmpty");
      return;
    }

    isSavingCloudToken.value = true;
    cloudAuthError.value = "";
    try {
      const status = await saveCloudToken(token);
      cloudAuth.value = status;
      if (!status.authenticated) {
        cloudAuthError.value = t("chat.cloudLoginExpired", configuredCloudProviderName());
        return;
      }
      try {
        setAvailableModels(await getTags({ refresh: true }));
      } catch {
        /* ignore */
      }
      cloudTokenInput.value = "";
      showCloudAuthDialog.value = false;
    } catch (e: any) {
      cloudAuthError.value = e?.message || t("chat.failedResp");
    } finally {
      isSavingCloudToken.value = false;
    }
  };

  useEffect(() => {
    const refreshModels = () => {
      getTags({ refresh: true }).then((m) => {
        setAvailableModels(m);
      }).catch(() => {});
    };

    refreshModels();
    getPs().then((running) => {
      if (running.length > 0 && !selectedModelKey.value) {
        selectedModelKey.value = `local:${running[0].model || running[0].name}`;
        saveSelectedModelKey(selectedModelKey.value);
      }
    }).catch(() => {});
    getCloudAuthStatus().then((status) => {
      cloudAuth.value = status;
    }).catch(() => {});
    const refreshSettings = () => {
      getSettings().then((settings) => {
        cloudProviderName.value = settings.cloud_provider_name || settings.default_cloud_provider_name || "";
        cloudGatewayURL.value = settings.ai_gateway_url || settings.default_ai_gateway_url || "";
        const available = Boolean(settings.web_search?.enabled);
        webSearchAvailable.value = available;
        webSearchEnabled.value = readWebSearchEnabled(true);
      }).catch(() => {
        webSearchAvailable.value = false;
        webSearchEnabled.value = true;
      });
    };

    refreshSettings();

    (async () => {
      await migrateLocalStorage();
      await refreshConversationList();
      const metas = conversationMetas.value;
      if (metas.length > 0) {
        await loadConversation(metas[0].id);
      } else {
        const conv = await createConversation({});
        activeConversation.value = conv;
        activeSessionId.value = conv.id;
        await refreshConversationList();
      }
    })();

    const refreshAll = () => {
      refreshSettings();
      refreshModels();
    };
    const handleFocus = () => refreshAll();
    const handleProvidersChanged = () => {
      refreshAll();
    };
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        refreshAll();
      }
    };
    window.addEventListener("focus", handleFocus);
    window.addEventListener(providersChangedEvent, handleProvidersChanged);
    document.addEventListener("visibilitychange", handleVisibilityChange);
    return () => {
      window.removeEventListener("focus", handleFocus);
      window.removeEventListener(providersChangedEvent, handleProvidersChanged);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      if (recordingStopTimerRef.current !== null) {
        window.clearTimeout(recordingStopTimerRef.current);
        recordingStopTimerRef.current = null;
      }
      if (recordingVadFrameRef.current !== null) {
        window.cancelAnimationFrame(recordingVadFrameRef.current);
        recordingVadFrameRef.current = null;
      }
      recordingAudioContextRef.current?.close().catch(() => {});
      recordingAudioContextRef.current = null;
      if (mediaRecorderRef.current?.state === "recording") {
        mediaRecorderRef.current.stop();
      }
      recordingStreamRef.current?.getTracks().forEach((track) => track.stop());
      recordingStreamRef.current = null;
      setPendingAudio(null);
      clearAudioPreviews();
    };
  }, []);

  const syncStickToBottom = () => {
    const el = messagesRef.current;
    if (el) {
      stickToBottomRef.current = isNearScrollBottom(el);
    }
  };

  const scrollMessagesToBottom = (force = false) => {
    const el = messagesRef.current;
    if (!el) return;
    if (!force && !stickToBottomRef.current) return;
    el.scrollTop = el.scrollHeight;
    if (force) stickToBottomRef.current = true;
  };

  useEffect(() => {
    scrollMessagesToBottom(true);
  }, [activeConversation.value?.id, activeConversation.value?.messages.length]);

  useEffect(() => {
    scrollMessagesToBottom();
  }, [
    streamingContent.value,
    streamingSources.value.length,
    searchingQuery.value,
    searchPlanningQuery.value,
    searchSkippedReason.value,
  ]);

  const handleImageUpload = (e: Event) => {
    const files = (e.target as HTMLInputElement).files;
    if (!files) return;
    Array.from(files).forEach((file) => {
      normalizeImage(file)
        .then((img) => {
          pendingImages.value = [...pendingImages.value, img];
        })
        .catch((err) => {
          chatError.value = `${t("chat.failedResp")}: ${err?.message || err}`;
        });
    });
    (e.target as HTMLInputElement).value = "";
  };

  const removeImage = (idx: number) => {
    pendingImages.value = pendingImages.value.filter((_, i) => i !== idx);
  };

  const queueAudioTranscription = () => {
    window.setTimeout(() => {
      if (getChatModelMode(selectedModelInfo.value) !== "asr") return;
      if (!pendingAudio.value || isGenerating.value || isRecordingAudio.value) return;
      void handleSend();
    }, 0);
  };

  const handleAudioUpload = (e: Event) => {
    const files = (e.target as HTMLInputElement).files;
    const file = files?.[0];
    if (!file) return;
    setPendingAudio({
      file,
      name: file.name || t("chat.audioFile"),
      size: file.size,
      objectUrl: URL.createObjectURL(file),
    });
    (e.target as HTMLInputElement).value = "";
    queueAudioTranscription();
  };

  const removeAudio = () => {
    setPendingAudio(null);
  };

  const stopRecordingStream = () => {
    recordingStreamRef.current?.getTracks().forEach((track) => track.stop());
    recordingStreamRef.current = null;
  };

  const clearRecordingStopTimer = () => {
    if (recordingStopTimerRef.current !== null) {
      window.clearTimeout(recordingStopTimerRef.current);
      recordingStopTimerRef.current = null;
    }
    if (recordingVadFrameRef.current !== null) {
      window.cancelAnimationFrame(recordingVadFrameRef.current);
      recordingVadFrameRef.current = null;
    }
    recordingAudioContextRef.current?.close().catch(() => {});
    recordingAudioContextRef.current = null;
  };

  const startSilenceAutoStop = (stream: MediaStream, recorder: MediaRecorder) => {
    const AudioContextCtor = window.AudioContext || (window as any).webkitAudioContext;
    if (!AudioContextCtor) return;

    const audioContext = new AudioContextCtor() as AudioContext;
    const source = audioContext.createMediaStreamSource(stream);
    const analyser = audioContext.createAnalyser();
    analyser.fftSize = 1024;
    source.connect(analyser);
    recordingAudioContextRef.current = audioContext;

    const data = new Uint8Array(analyser.fftSize);
    const startedAt = performance.now();
    let lastVoiceAt = startedAt;

    const tick = () => {
      analyser.getByteTimeDomainData(data);
      let sum = 0;
      for (let i = 0; i < data.length; i += 1) {
        const centered = (data[i] - 128) / 128;
        sum += centered * centered;
      }
      const rms = Math.sqrt(sum / data.length);
      const now = performance.now();
      if (rms >= recordingVoiceThreshold) {
        lastVoiceAt = now;
      }
      if (
        recorder.state === "recording" &&
        now - startedAt >= recordingMinDurationMs &&
        now - lastVoiceAt >= recordingSilenceStopMs
      ) {
        recorder.stop();
        return;
      }
      if (recorder.state === "recording") {
        recordingVadFrameRef.current = window.requestAnimationFrame(tick);
      }
    };
    recordingVadFrameRef.current = window.requestAnimationFrame(tick);
  };

  const startAudioRecording = async () => {
    if (isRecordingAudio.value || isGenerating.value) return;
    if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
      chatError.value = t("chat.recordingUnsupported");
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mimeType = preferredAudioMimeType();
      const recorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
      recordingChunksRef.current = [];
      recordingStreamRef.current = stream;
      mediaRecorderRef.current = recorder;
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) recordingChunksRef.current.push(event.data);
      };
      recorder.onstop = () => {
        clearRecordingStopTimer();
        const type = recorder.mimeType || mimeType || "audio/webm";
        const blob = new Blob(recordingChunksRef.current, { type });
        recordingChunksRef.current = [];
        stopRecordingStream();
        isRecordingAudio.value = false;
        if (blob.size === 0) return;
        const ext = extensionForAudioMimeType(type);
        const file = new File([blob], `recording-${Date.now()}.${ext}`, { type });
        setPendingAudio({
          file,
          name: file.name,
          size: file.size,
          objectUrl: URL.createObjectURL(file),
          recorded: true,
        });
        queueAudioTranscription();
      };
      recorder.onerror = () => {
        clearRecordingStopTimer();
        stopRecordingStream();
        isRecordingAudio.value = false;
        chatError.value = t("chat.recordingFailed");
      };
      recorder.start();
      isRecordingAudio.value = true;
      startSilenceAutoStop(stream, recorder);
      recordingStopTimerRef.current = window.setTimeout(() => {
        if (recorder.state === "recording") {
          recorder.stop();
        }
      }, recordingMaxDurationMs);
      chatError.value = "";
    } catch (e: any) {
      clearRecordingStopTimer();
      stopRecordingStream();
      isRecordingAudio.value = false;
      chatError.value = e?.message || t("chat.recordingFailed");
    }
  };

  const stopAudioRecording = () => {
    clearRecordingStopTimer();
    if (mediaRecorderRef.current?.state === "recording") {
      mediaRecorderRef.current.stop();
    }
  };

  const handleSend = async () => {
    const text = inputText.value.trim();
    const currentModel = selectedModelInfo.value;
    const mode = getChatModelMode(currentModel);
    const imageMode = mode === "image";
    const imageEditMode = imageMode && isImageToImageModel(currentModel);
    const asrMode = mode === "asr";
    const visionMode = mode === "vision";
    const audio = pendingAudio.value;
    if (!currentModel || isGenerating.value) return;
    if (asrMode) {
      if (!audio || isRecordingAudio.value) return;
    } else if (imageEditMode && pendingImages.value.length === 0) {
      chatError.value = t("image.inputImageRequired");
      return;
    } else if (!text) {
      return;
    }

    if (currentModel.source === "cloud") {
      const provider = currentModel.provider || configuredCloudProviderName();
      try {
        const status = cloudAuth.value || await refreshCloudAuth();
        if (!hasCloudAuth(status)) {
          await openCloudAuthDialog(t("chat.cloudLoginRequired", provider));
          return;
        }
      } catch {
        await openCloudAuthDialog(t("chat.cloudLoginRequired", provider));
        return;
      }
    }

    const conv = activeConversation.value;
    if (!conv) {
      chatError.value = "No active session. Please create a new chat.";
      return;
    }

    const images = pendingImages.value;
    let userContent: ChatMessage["content"];
    let currentUserMessage: ChatMessage;

    if (asrMode && audio) {
      const audioLabel = audio.recorded ? t("chat.recordedAudio") : t("chat.audioFile");
      userContent = text
        ? `${audioLabel}: ${audio.name}\n${t("chat.audioPrompt")}: ${text}`
        : `${audioLabel}: ${audio.name}`;
      currentUserMessage = { role: "user", content: userContent };
    } else if (visionMode && images.length > 0) {
      const displayParts: ContentPart[] = images.map((img) => ({
        type: "image_url" as const,
        image_url: { url: img.thumb },
      }));
      displayParts.push({ type: "text" as const, text });
      userContent = displayParts;

      const apiParts: ContentPart[] = images.map((img) => ({
        type: "image_url" as const,
        image_url: { url: img.full },
      }));
      apiParts.push({ type: "text" as const, text });

      currentUserMessage = { role: "user", content: apiParts };
    } else if (imageEditMode && images.length > 0) {
      const displayParts: ContentPart[] = images.map((img) => ({
        type: "image_url" as const,
        image_url: { url: img.thumb },
      }));
      displayParts.push({ type: "text" as const, text });
      userContent = displayParts;
      currentUserMessage = { role: "user", content: userContent };
    } else {
      userContent = text;
      currentUserMessage = { role: "user", content: text };
    }
    const currentModelKey = modelKey(currentModel);
    const contextStartIndex = contextStartIndexForModel(conv, currentModelKey);
    const apiHistory = contextStartIndex > 0 ? conv.messages.slice(contextStartIndex) : conv.messages;
    const apiMessages = buildChatContextMessages(apiHistory, currentUserMessage);

    const userMessageIndex = conv.messages.length;
    conv.messages.push({ role: "user", content: userContent });
    if (asrMode && audio) {
      setAudioPreview(messagePreviewKey(conv.id, userMessageIndex), {
        name: audio.name,
        size: audio.size,
        objectUrl: URL.createObjectURL(audio.file),
        recorded: audio.recorded,
      });
    }
    if (conv.messages.length === 1) {
      conv.title = text.slice(0, 30) || (asrMode && audio ? audio.name.slice(0, 30) : "New Chat");
    }
    conv.model = currentModelKey;
    if (contextStartIndex > 0) {
      setModelContextBoundary(conv.id, currentModelKey, contextStartIndex);
    }
    activeConversation.value = { ...conv };
    inputText.value = "";
    pendingImages.value = [];
    if (asrMode) {
      setPendingAudio(null);
    }
    saveCurrentConversation();

    stickToBottomRef.current = true;
    isGenerating.value = true;
    streamingContent.value = "";
    searchingQuery.value = "";
    searchPlanningQuery.value = "";
    searchSkippedReason.value = "";
    streamingSources.value = [];

    const ac = new AbortController();
    abortRef.current = ac;

    const numCtx = normalizeNumCtx(conv.settings?.num_ctx);
    const numParallel = conv.settings?.num_parallel || defaultNumParallel();

    chatError.value = "";
    const responseStartedAt = Date.now();
    try {
      if (imageMode) {
        const encodedImages = imageEditMode ? images.map((img) => stripDataURL(img.full)) : [];
        const job = await createImageGenerationJob({
          model: currentModel.model || currentModel.name,
          source: currentModel.source,
          prompt: text,
          image: imageEditMode ? encodedImages[0] : undefined,
          images: encodedImages.length > 1 ? encodedImages.slice(1) : undefined,
        });
        let latest = job;
        while (!ac.signal.aborted) {
          if (latest.status === "succeeded") {
            const imageParts: ContentPart[] = (latest.result?.data || [])
              .map((item) => item.b64_json ? `data:image/png;base64,${item.b64_json}` : item.url || "")
              .filter(Boolean)
              .map((url) => ({ type: "image_url" as const, image_url: { url } }));
            if (imageParts.length === 0) {
              throw new Error("Image generation completed without image data.");
            }
            imageParts.push({ type: "text" as const, text: t("chat.imageGenerated") });
            conv.messages.push({
              role: "assistant",
              content: imageParts,
              meta: buildResponseMeta(t("chat.imageGenerated"), responseStartedAt),
            });
            activeConversation.value = { ...conv };
            saveCurrentConversation();
            break;
          }
          if (latest.status === "failed") {
            throw new Error(latest.error || t("chat.failedResp"));
          }
          if (latest.status === "cancelled") {
            throw new Error("Image generation cancelled.");
          }
          streamingContent.value = latest.status === "queued" ? t("chat.imageQueued") : t("chat.imageGenerating");
          await sleep(1500, ac.signal);
          latest = await getImageGenerationJob(job.id);
        }
        if (ac.signal.aborted) {
          try {
            await cancelImageGenerationJob(job.id);
          } catch {
            /* ignore cancellation cleanup errors */
          }
        }
        streamingContent.value = "";
      } else if (asrMode && audio) {
        let asrRuntimeReady = true;
        try {
          asrRuntimeReady = (await getASRRuntimeStatus()).ready;
        } catch {
          // If the status probe fails, keep the existing transcription path and surface its error.
        }
        streamingContent.value = asrRuntimeReady ? t("chat.transcribingAudio") : t("chat.preparingASRRuntime");
        let receivedTranscriptChunk = false;
        const response = await transcribeAudioStream({
          model: currentModel.model || currentModel.name,
          file: audio.file,
          prompt: text || undefined,
          response_format: "json",
        }, (chunk) => {
          if (chunk.text) {
            if (!receivedTranscriptChunk) {
              streamingContent.value = "";
              receivedTranscriptChunk = true;
            }
            streamingContent.value += chunk.text;
          }
        }, ac.signal);
        const transcript = response.text || "";
        conv.messages.push({
          role: "assistant",
          content: transcript || t("chat.emptyTranscription"),
          meta: buildResponseMeta(transcript || t("chat.emptyTranscription"), responseStartedAt),
        });
        activeConversation.value = { ...conv };
        streamingContent.value = "";
        saveCurrentConversation();
      } else {
        await streamChat(
        currentModel.model || currentModel.name,
        apiMessages,
        {
          temperature: temperature.value,
          top_p: topP.value,
          max_tokens: maxTokens.value,
          num_ctx: numCtx,
          num_parallel: numParallel,
          system: systemPrompt.value || undefined,
          source: currentModel.source,
          web_search: webSearchAvailable.value && webSearchEnabled.value
            ? { enabled: true, query: text }
            : { enabled: false },
        },
        (token, done) => {
          if (done) {
            searchingQuery.value = "";
            const assistantContent = streamingContent.value;
            const sources = streamingSources.value;
            conv.messages.push({
              role: "assistant",
              content: assistantContent,
              meta: {
                ...buildResponseMeta(assistantContent, responseStartedAt),
                ...(sources.length > 0 ? { sources } : {}),
              },
            });
            activeConversation.value = { ...conv };
            streamingContent.value = "";
            streamingSources.value = [];
            saveCurrentConversation();
          } else {
            streamingContent.value += token;
          }
        },
        ac.signal,
        (query) => {
          searchingQuery.value = query;
        },
        (query, results) => {
          searchingQuery.value = "";
          streamingSources.value = results;
          if (query && conv.messages.length > 0) {
            activeConversation.value = { ...conv };
          }
        },
        (message) => {
          searchingQuery.value = "";
          searchSkippedReason.value = t("chat.webSearchFailed", message);
        },
        (query) => {
          searchPlanningQuery.value = query;
          searchSkippedReason.value = "";
        },
        (reason) => {
          searchPlanningQuery.value = "";
          searchSkippedReason.value = reason;
        },
        (route) => {
          if (route.action === "skip" && route.reason) {
            searchPlanningQuery.value = "";
            searchSkippedReason.value = route.reason;
          }
        },
        );
      }
    } catch (e: any) {
      const errMessage = e?.message || t("chat.failedResp");
      const localizedErrorMessage = localizeChatErrorMessage(errMessage, currentModel.provider || configuredCloudProviderName());
      if (streamingContent.value && !asrMode && (!ac.signal.aborted || !imageMode)) {
        const assistantContent = streamingContent.value;
        const sources = streamingSources.value;
        conv.messages.push({
          role: "assistant",
          content: assistantContent,
          meta: {
            ...buildResponseMeta(assistantContent, responseStartedAt),
            ...(sources.length > 0 ? { sources } : {}),
          },
        });
        activeConversation.value = { ...conv };
        streamingContent.value = "";
        streamingSources.value = [];
        saveCurrentConversation();
      } else if (!ac.signal.aborted) {
        if (currentModel.source === "cloud" && isCloudAuthErrorMessage(errMessage)) {
          await openCloudAuthDialog(localizedErrorMessage);
        } else {
          chatError.value = localizedErrorMessage;
        }
      }
    }

    searchingQuery.value = "";
    searchPlanningQuery.value = "";
    searchSkippedReason.value = "";
    streamingSources.value = [];
    if (asrMode || (imageMode && ac.signal.aborted)) {
      streamingContent.value = "";
    }
    isGenerating.value = false;
    abortRef.current = null;
  };

  const handleStop = () => {
    abortRef.current?.abort();
  };

  const handleNewSession = async () => {
    try {
      openConversationMenuId.value = "";
      const conv = await createConversation({});
      activeConversation.value = conv;
      activeSessionId.value = conv.id;
      await refreshConversationList();
    } catch {
      /* ignore */
    }
  };

  const handleConversationSearchInput = async (e: Event) => {
    conversationSearchQuery.value = (e.target as HTMLInputElement).value;
    await refreshConversationList();
  };

  const handleClearConversationSearch = async () => {
    conversationSearchQuery.value = "";
    await refreshConversationList();
  };

  const handleSelectConversation = async (id: string) => {
    if (id === activeSessionId.value) return;
    openConversationMenuId.value = "";
    await loadConversation(id);
  };

  const handleDeleteConversation = async (id: string, e: Event) => {
    e.stopPropagation();
    openConversationMenuId.value = "";
    if (!confirm(t("chat.deleteChatConfirm"))) return;
    try {
      await deleteConversation(id);
      await refreshConversationList();
      if (id === activeSessionId.value) {
        const metas = conversationMetas.value;
        if (metas.length > 0) {
          await loadConversation(metas[0].id);
        } else {
          const conv = await createConversation({});
          activeConversation.value = conv;
          activeSessionId.value = conv.id;
          await refreshConversationList();
        }
      }
    } catch {
      /* ignore */
    }
  };

  const handleRenameConversation = async (meta: ConversationMeta, e: Event) => {
    e.stopPropagation();
    openConversationMenuId.value = "";
    const nextTitle = prompt(t("chat.renameChatPrompt"), meta.title)?.trim();
    if (!nextTitle || nextTitle === meta.title) return;
    try {
      const updated = await updateConversation(meta.id, { title: nextTitle });
      if (meta.id === activeSessionId.value && activeConversation.value) {
        activeConversation.value = {
          ...activeConversation.value,
          title: updated.title || nextTitle,
        };
      }
      await refreshConversationList();
    } catch {
      chatError.value = t("chat.renameChatFailed");
    }
  };

  const handleClearHistory = () => {
    const conv = activeConversation.value;
    if (!conv || conv.messages.length === 0) return;
    if (!confirm(t("chat.clearConfirm"))) return;
    conv.messages = [];
    conv.title = "New Chat";
    clearModelContextBoundary(conv.id);
    activeConversation.value = { ...conv };
    saveCurrentConversation();
  };

  const handleKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const conv = activeConversation.value;
  const messages = conv?.messages || [];
  const hasConversationSearch = conversationSearchQuery.value.trim().length > 0;
  const modelMode = selectedModelMode.value;
  const imageMode = modelMode === "image";
  const imageEditMode = imageMode && isImageToImageModel(selectedModelInfo.value);
  const asrMode = modelMode === "asr";
  const visionMode = modelMode === "vision";
  const canSend = asrMode
    ? Boolean(pendingAudio.value && !isRecordingAudio.value && selectedModelInfo.value)
    : imageEditMode
      ? Boolean(inputText.value.trim() && pendingImages.value.length > 0 && selectedModelInfo.value)
      : Boolean(inputText.value.trim() && selectedModelInfo.value);

  return (
    <div class="flex h-full min-h-0 gap-0 bg-[#f7f8fb] p-4">
      {/* Conversation history */}
      {showSidebar.value && (
        <aside class="mr-4 flex h-full min-h-0 w-[280px] flex-shrink-0 flex-col rounded-[22px] border border-gray-200 bg-white shadow-sm">
          <div class="flex items-center justify-between border-b border-gray-100 px-5 py-4">
            <span class="text-sm font-semibold text-gray-900">{t("chat.conversationHistory")}</span>
            <button
              onClick={() => (showSidebar.value = false)}
              class="rounded-lg p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600"
              title={t("chat.hideSidebar")}
            >
              <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
          <div class="px-4 py-3">
            <button
              onClick={handleNewSession}
              class="flex w-full items-center justify-center gap-2 rounded-xl border border-gray-200 bg-white px-3 py-2 text-sm font-medium text-gray-700 transition-colors hover:border-blue-200 hover:bg-blue-50 hover:text-blue-600"
            >
              <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
              </svg>
              {t("chat.newChat")}
            </button>
            <div class="relative mt-3">
              <svg class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35m1.35-5.65a7 7 0 11-14 0 7 7 0 0114 0z" />
              </svg>
              <input
                type="text"
                value={conversationSearchQuery.value}
                onInput={handleConversationSearchInput}
                placeholder={t("chat.searchConversations")}
                class="h-9 w-full rounded-xl border border-gray-200 bg-white px-9 text-sm text-gray-700 outline-none transition-colors placeholder:text-gray-400 focus:border-blue-300 focus:ring-2 focus:ring-blue-100"
              />
              {hasConversationSearch && (
                <button
                  type="button"
                  onClick={handleClearConversationSearch}
                  class="absolute right-2 top-1/2 rounded-lg p-1 text-gray-400 transition-colors -translate-y-1/2 hover:bg-gray-100 hover:text-gray-600"
                  title={t("chat.clearConversationSearch")}
                >
                  <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              )}
            </div>
          </div>
          <div class="flex-1 overflow-auto px-3 pb-4">
            {conversationMetas.value.length === 0 && (
              <div class="mt-8 px-4 text-center text-xs text-gray-400">
                {hasConversationSearch ? t("chat.noConversationSearchResults") : t("chat.noConversations")}
              </div>
            )}
            {conversationMetas.value.map((meta) => (
              <div
                key={meta.id}
                onClick={() => handleSelectConversation(meta.id)}
                class={`group relative mb-1 flex cursor-pointer items-center gap-3 rounded-xl px-3 py-2.5 transition-colors ${
                  meta.id === activeSessionId.value ? "bg-blue-50 text-blue-700" : "text-gray-700 hover:bg-gray-50"
                }`}
              >
                <div class="min-w-0 flex-1">
                  <div class="truncate text-sm font-medium">{meta.title}</div>
                  <div class="mt-0.5 flex items-center gap-2">
                    <span class={`text-[11px] ${meta.id === activeSessionId.value ? "text-blue-400" : "text-gray-400"}`}>
                      {relativeTime(meta.updated_at)}
                    </span>
                    {meta.msg_count > 0 && (
                      <span class={`text-[11px] ${meta.id === activeSessionId.value ? "text-blue-400" : "text-gray-400"}`}>
                        {t("chat.messages", meta.msg_count)}
                      </span>
                    )}
                  </div>
                </div>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    openConversationMenuId.value = openConversationMenuId.value === meta.id ? "" : meta.id;
                  }}
                  class={`flex-shrink-0 rounded-lg p-1 transition-all ${
                    meta.id === activeSessionId.value
                      ? "text-blue-400 hover:bg-blue-100 hover:text-blue-600"
                      : "text-gray-300 opacity-0 hover:bg-gray-100 hover:text-gray-500 group-hover:opacity-100"
                  }`}
                  title={t("chat.moreActions")}
                >
                  <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M6 12h.01M12 12h.01M18 12h.01" />
                  </svg>
                </button>
                {openConversationMenuId.value === meta.id && (
                  <div class="absolute right-3 top-10 z-10 w-28 overflow-hidden rounded-xl border border-gray-200 bg-white py-1 text-sm shadow-lg">
                    <button
                      onClick={(e) => handleRenameConversation(meta, e)}
                      class="block w-full px-3 py-2 text-left text-gray-700 hover:bg-gray-50"
                    >
                      {t("chat.renameChat")}
                    </button>
                    <button
                      onClick={(e) => handleDeleteConversation(meta.id, e)}
                      class="block w-full px-3 py-2 text-left text-red-600 hover:bg-red-50"
                    >
                      {t("chat.deleteChat")}
                    </button>
                  </div>
                )}
              </div>
            ))}
          </div>
        </aside>
      )}

      {/* Main Chat Area */}
      <section class="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-[22px] border border-gray-200 bg-white shadow-sm">
        {/* Header */}
        <div class="flex h-14 items-center justify-between border-b border-gray-100 bg-white px-5">
          <div class="flex items-center gap-3">
            <button
              onClick={() => (showSidebar.value = !showSidebar.value)}
              class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600"
              title={showSidebar.value ? t("chat.hideSidebar") : t("chat.showSidebar")}
            >
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M4 6h16M4 12h16M4 18h16" />
              </svg>
            </button>
            <button
              onClick={handleNewSession}
              class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600"
              title={t("chat.newChat")}
            >
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
              </svg>
            </button>
            <span class="max-w-xs truncate text-sm font-semibold text-gray-900">{conv?.title || t("chat.chat")}</span>
            <button
              onClick={(e) => {
                const meta = conversationMetas.value.find((item) => item.id === activeSessionId.value);
                if (meta) handleRenameConversation(meta, e);
              }}
              disabled={!conv}
              class="rounded-lg p-1 text-gray-300 transition-colors hover:bg-gray-100 hover:text-gray-500 disabled:cursor-not-allowed disabled:opacity-40"
              title={t("chat.renameChat")}
            >
              <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.651-1.651a1.875 1.875 0 112.652 2.652L8.25 18.403 4.5 19.5l1.097-3.75L16.862 4.487z" />
              </svg>
            </button>
          </div>
          <div class="flex items-center gap-2">
            <button
              onClick={handleClearHistory}
              class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500"
              title={t("chat.clearHistory")}
            >
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
              </svg>
            </button>
            <button
              onClick={() => (showSettings.value = !showSettings.value)}
              class={`rounded-lg p-1.5 transition-colors ${showSettings.value ? "bg-blue-50 text-blue-600" : "text-gray-400 hover:bg-gray-100 hover:text-gray-600"}`}
              title={t("chat.settings")}
            >
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4" />
              </svg>
            </button>
            <button class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600" title={t("chat.moreActions")}>
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M12 6h.01M12 12h.01M12 18h.01" />
              </svg>
            </button>
          </div>
        </div>

        {/* Messages */}
        <div
          ref={messagesRef}
          class="min-h-0 flex-1 overflow-auto bg-white px-6 py-8"
          onScroll={syncStickToBottom}
        >
          <div class="mx-auto flex min-h-full max-w-[760px] flex-col gap-5">
            {chatError.value && (
              <div class="flex items-start gap-2 rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                <svg class="mt-0.5 h-4 w-4 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
                <span class="flex-1 whitespace-pre-line">{chatError.value}</span>
                <button onClick={() => (chatError.value = "")} class="ml-auto flex-shrink-0 text-red-400 hover:text-red-600">&#x2715;</button>
              </div>
            )}
            {messages.length === 0 && !streamingContent.value && !chatError.value && (
              <div class="flex min-h-[360px] items-center justify-center text-center">
                <div>
                  <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-gray-50 text-gray-300">
                    <svg class="h-6 w-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M8 10h.01M12 10h.01M16 10h.01M9 16H5a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v8a2 2 0 01-2 2h-5l-5 4v-4z" />
                    </svg>
                  </div>
                  <div class="text-sm text-gray-400">{t("chat.startConv")}</div>
                </div>
              </div>
            )}
            {messages.map((m, i) => (
              <MessageBubble
                key={i}
                message={m}
                audioPreview={audioPreviews.value[messagePreviewKey(activeSessionId.value, i)]}
              />
            ))}
            {isGenerating.value && (
              <AssistantProgress
                query={searchingQuery.value}
                planningQuery={searchPlanningQuery.value}
                skippedReason={searchSkippedReason.value}
                sources={streamingSources.value}
                content={streamingContent.value}
              />
            )}
            {streamingContent.value && (
              <MessageBubble
                message={{
                  role: "assistant",
                  content: streamingContent.value,
                  meta: streamingSources.value.length > 0 ? { sources: streamingSources.value } : undefined,
                }}
                streaming
              />
            )}
          </div>
        </div>

        {/* Input */}
        <div class="flex-shrink-0 border-t border-gray-100 bg-white px-6 py-5">
          <div class="mx-auto max-w-[760px]">
            {pendingImages.value.length > 0 && (
              <div class="mb-3 flex flex-wrap gap-2">
                {pendingImages.value.map((img, i) => (
                  <div key={i} class="group relative">
                    <img src={img.thumb} class="h-16 w-16 rounded-xl border border-gray-200 object-cover" />
                    <button
                      onClick={() => removeImage(i)}
                      class="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full bg-red-500 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100"
                    >
                      x
                    </button>
                  </div>
                ))}
              </div>
            )}
            {pendingAudio.value && (
              <div class="mb-3 rounded-xl border border-cyan-100 bg-cyan-50 px-3 py-2">
                <div class="flex items-center gap-3">
                  <div class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-lg bg-white text-cyan-600">
                    <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M9 19V6l12-2v13M9 19c0 1.105-1.343 2-3 2s-3-.895-3-2 1.343-2 3-2 3 .895 3 2zm12-2c0 1.105-1.343 2-3 2s-3-.895-3-2 1.343-2 3-2 3 .895 3 2z" />
                    </svg>
                  </div>
                  <div class="min-w-0 flex-1">
                    <div class="truncate text-sm font-medium text-cyan-900">{pendingAudio.value.name}</div>
                    <div class="text-xs text-cyan-700">
                      {pendingAudio.value.recorded ? t("chat.recordedAudio") : t("chat.audioFile")} · {formatBytes(pendingAudio.value.size)}
                    </div>
                    <div class="mt-0.5 text-xs text-cyan-600">{t("chat.audioReadyHint")}</div>
                  </div>
                  <audio src={pendingAudio.value.objectUrl} controls class="h-8 max-w-[220px]" />
                  <button
                    onClick={removeAudio}
                    class="flex h-7 w-7 flex-shrink-0 items-center justify-center rounded-full bg-white text-cyan-500 hover:text-red-500"
                    title={t("chat.removeAudio")}
                  >
                    x
                  </button>
                </div>
              </div>
            )}
            <div class="rounded-[24px] border border-gray-200 bg-white p-3 shadow-[0_16px_50px_rgba(15,23,42,0.08)]">
              <textarea
                class="min-h-[46px] w-full resize-none border-0 bg-transparent px-2 text-sm leading-6 text-gray-900 placeholder:text-gray-400 focus:outline-none focus:ring-0"
                rows={2}
                placeholder={asrMode ? t("chat.askAudio") : imageEditMode ? t("chat.askImageEdit") : visionMode ? t("chat.askImage") : imageMode ? t("chat.askImageGenerate") : t("chat.askHelp")}
                value={inputText.value}
                onInput={(e) => (inputText.value = (e.target as HTMLTextAreaElement).value)}
                onKeyDown={handleKeyDown}
              />
              <div class="mt-2 flex items-center justify-between gap-3">
                <div class="flex min-w-0 items-center gap-2">
                  {asrMode ? (
                    <>
                      <label class="flex h-9 w-9 cursor-pointer items-center justify-center rounded-xl border border-gray-200 text-gray-500 transition-colors hover:border-cyan-200 hover:bg-cyan-50 hover:text-cyan-600" title={t("chat.uploadAudio")}>
                        <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                        </svg>
                        <input type="file" accept="audio/*,.mp3,.wav,.m4a,.flac,.webm" class="hidden" onChange={handleAudioUpload} />
                      </label>
                      <button
                        type="button"
                        onClick={isRecordingAudio.value ? stopAudioRecording : startAudioRecording}
                        disabled={isGenerating.value}
                        class={`relative flex h-9 w-9 items-center justify-center rounded-xl border transition-colors ${
                          isRecordingAudio.value
                            ? "border-red-200 bg-red-50 text-red-600"
                            : "border-gray-200 text-gray-500 hover:border-cyan-200 hover:bg-cyan-50 hover:text-cyan-600"
                        } disabled:cursor-not-allowed disabled:opacity-50`}
                        title={isRecordingAudio.value ? t("chat.stopRecording") : t("chat.startRecording")}
                      >
                        {isRecordingAudio.value && (
                          <>
                            <span class="pointer-events-none absolute -inset-1 rounded-2xl border border-red-300/70 animate-ping" />
                            <span class="pointer-events-none absolute -inset-2 rounded-3xl border border-red-200/70 animate-ping" style={{ animationDelay: "300ms" }} />
                          </>
                        )}
                        <svg class="relative z-10 h-5 w-5" fill={isRecordingAudio.value ? "currentColor" : "none"} viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                          <path stroke-linecap="round" stroke-linejoin="round" d="M12 18.75a6.75 6.75 0 006.75-6.75V7.5a6.75 6.75 0 10-13.5 0V12A6.75 6.75 0 0012 18.75z" />
                          <path stroke-linecap="round" stroke-linejoin="round" d="M12 18.75V22m-4 0h8" />
                        </svg>
                      </button>
                    </>
                  ) : visionMode || imageEditMode ? (
                    <label class="flex h-9 w-9 cursor-pointer items-center justify-center rounded-xl border border-gray-200 text-gray-500 transition-colors hover:border-blue-200 hover:bg-blue-50 hover:text-blue-600" title={t("chat.uploadImage")}>
                      <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                      </svg>
                      <input type="file" accept="image/*" multiple class="hidden" onChange={handleImageUpload} />
                    </label>
                  ) : (
                    <button
                      disabled
                      class="flex h-9 w-9 cursor-not-allowed items-center justify-center rounded-xl border border-gray-200 text-gray-300"
                      title={t("chat.uploadImage")}
                    >
                      <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                        <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v16m8-8H4" />
                      </svg>
                    </button>
                  )}
                  {webSearchAvailable.value && !asrMode && !imageMode && (
                    <button
                      type="button"
                      onClick={() => {
                        const next = !webSearchEnabled.value;
                        webSearchEnabled.value = next;
                        saveWebSearchEnabled(next);
                      }}
                      class={`h-9 rounded-xl border px-3 text-xs font-medium transition-colors ${
                        webSearchEnabled.value
                          ? "border-blue-200 bg-blue-50 text-blue-700"
                          : "border-gray-200 bg-white text-gray-400 hover:border-gray-300 hover:bg-gray-50"
                      }`}
                      title={t("chat.webSearchToggle")}
                      aria-pressed={webSearchEnabled.value}
                    >
                      {t("chat.webSearch")}
                    </button>
                  )}
                  <select
                    class="max-w-[260px] truncate rounded-full border border-gray-200 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500"
                    value={selectedModelKey.value}
                    onChange={(e) => handleModelChange((e.target as HTMLSelectElement).value)}
                  >
                    {availableModels.value.map((m) => (
                      <option key={modelKey(m)} value={modelKey(m)}>
                        {modelLabel(m)}
                      </option>
                    ))}
                  </select>
                </div>
                {isGenerating.value ? (
                  <button
                    onClick={handleStop}
                    class="flex h-9 w-9 items-center justify-center rounded-xl bg-red-500 text-white transition-colors hover:bg-red-600"
                    title={t("chat.stop")}
                  >
                    <svg class="h-4 w-4" fill="currentColor" viewBox="0 0 24 24">
                      <rect x="6" y="6" width="12" height="12" rx="1" />
                    </svg>
                  </button>
                ) : (
                  <button
                    onClick={handleSend}
                    disabled={!canSend}
                    class="flex h-9 w-9 items-center justify-center rounded-xl bg-blue-600 text-white transition-colors hover:bg-blue-700 disabled:cursor-not-allowed disabled:opacity-40"
                    title={asrMode ? t("chat.transcribe") : t("chat.send")}
                  >
                    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                      <path stroke-linecap="round" stroke-linejoin="round" d="M5 10l7-7m0 0l7 7m-7-7v18" />
                    </svg>
                  </button>
                )}
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Settings Panel */}
      {showSettings.value && (
        <div class="w-72 border-l border-gray-200 bg-white p-5 flex flex-col gap-5 overflow-auto">
          <div class="flex items-center justify-between">
            <h3 class="font-semibold text-gray-900">{t("chat.suggestion")}</h3>
            <button onClick={() => (showSettings.value = false)} class="text-gray-400 hover:text-gray-600">
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">{t("chat.systemPrompt")}</label>
            <textarea
              class="w-full border border-gray-200 rounded-lg px-3 py-2 text-sm resize-none focus:outline-none focus:ring-2 focus:ring-indigo-500"
              rows={3}
              placeholder={t("chat.placeholder")}
              value={systemPrompt.value}
              onInput={(e) => (systemPrompt.value = (e.target as HTMLTextAreaElement).value)}
            />
          </div>

          <SliderSetting label="max_tokens" value={maxTokens.value} min={1} max={8192} step={1} onChange={(v) => (maxTokens.value = v)} />
          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="text-sm font-medium text-gray-700">num_ctx</label>
              <span class="text-sm text-gray-500 tabular-nums">{normalizeNumCtx(conv?.settings?.num_ctx)}</span>
            </div>
            <input
              type="range"
              min={0}
              max={contextLengthSteps.length - 1}
              step={1}
              value={Math.max(0, contextLengthSteps.indexOf(normalizeNumCtx(conv?.settings?.num_ctx)))}
              onInput={(e) => {
                const idx = Number((e.target as HTMLInputElement).value);
                const c = activeConversation.value;
                if (!c) return;
                if (!c.settings) c.settings = {};
                c.settings.num_ctx = contextLengthSteps[idx] || defaultNumCtx();
                activeConversation.value = { ...c };
                saveCurrentConversation();
              }}
              class="w-full h-1.5 bg-gray-200 rounded-lg appearance-none cursor-pointer accent-indigo-600"
            />
            <div class="flex justify-between mt-1.5">
              {contextLengthLabels.map((label) => (
                <span key={label} class="text-[10px] text-gray-400">{label}</span>
              ))}
            </div>
          </div>
          <SliderSetting label="Temperature" value={temperature.value} min={0} max={2} step={0.05} onChange={(v) => (temperature.value = v)} />
          <SliderSetting label="Top-P" value={topP.value} min={0} max={1} step={0.05} onChange={(v) => (topP.value = v)} />

          <button
            onClick={() => {
              systemPrompt.value = "";
              temperature.value = defaultTemperatureForModel(selectedModelInfo.value);
              topP.value = 0.75;
              maxTokens.value = 4096;
            }}
            class="flex items-center justify-center gap-1.5 py-2 border border-gray-200 rounded-lg text-sm text-gray-600 hover:bg-gray-50 transition-colors"
          >
            <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            </svg>
            {t("chat.resetDefaults")}
          </button>
        </div>
      )}
      {showCloudAuthDialog.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4">
          <div class="w-full max-w-lg rounded-2xl bg-white p-6 shadow-2xl">
            {selectedModelInfo.value && (
              <div class="mb-4 flex items-center justify-between rounded-lg bg-indigo-50 px-3 py-2">
                <div class="flex items-center gap-2">
                  <svg class="w-4 h-4 text-indigo-600" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
                  </svg>
                  <span class="text-sm font-medium text-indigo-700">{modelLabel(selectedModelInfo.value)}</span>
                </div>
                <button
                  onClick={() => {
                    const modelName = selectedModelInfo.value?.model || selectedModelInfo.value?.name || "";
                    navigator.clipboard.writeText(modelName);
                  }}
                  class="rounded p-1 text-indigo-600 hover:bg-indigo-100 transition-colors"
                  title={t("chat.copyModel")}
                >
                  <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
                  </svg>
                </button>
              </div>
            )}
            <div class="flex items-start justify-between gap-4">
              <div>
                <h3 class="text-lg font-semibold text-gray-900">{t("chat.cloudLoginTitle", configuredCloudProviderName())}</h3>
                <p class="mt-2 text-sm leading-6 text-gray-500">{t("chat.cloudLoginDesc", configuredCloudProviderName())}</p>
              </div>
              <button
                onClick={() => {
                  showCloudAuthDialog.value = false;
                  cloudAuthError.value = "";
                }}
                class="rounded-lg p-1 text-gray-400 hover:text-gray-600"
                aria-label={t("chat.cloudCancel")}
              >
                <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            <div class="mt-5 rounded-xl border border-gray-200 bg-gray-50 px-4 py-3">
              <div class="text-xs font-medium uppercase tracking-wide text-gray-400">{t("chat.cloudGatewayLabel")}</div>
              <div class="mt-1 text-sm font-medium text-gray-800">{cloudGatewayURL.value || t("chat.cloudGatewayValue")}</div>
            </div>

            {cloudAuthError.value && (
              <div class="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                {cloudAuthError.value}
              </div>
            )}

            <div class="mt-5 flex flex-wrap gap-2">
              <button
                onClick={() => openExternalURL(cloudAuth.value?.login_url)}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 transition-colors"
              >
                {t("chat.cloudOpenLogin")}
              </button>
              <button
                onClick={() => openExternalURL(cloudAuth.value?.access_token_url)}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 transition-colors"
              >
                {t("chat.cloudOpenTokenPage")}
              </button>
            </div>

            <div class="mt-5">
              <label class="mb-2 block text-sm font-medium text-gray-700">{t("chat.cloudTokenLabel")}</label>
              <input
                type="password"
                autoComplete="off"
                spellcheck={false}
                class="w-full rounded-lg border border-gray-200 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500"
                placeholder={t("chat.cloudTokenPlaceholder")}
                value={cloudTokenInput.value}
                onInput={(e) => (cloudTokenInput.value = (e.target as HTMLInputElement).value)}
              />
              <p class="mt-2 text-xs leading-5 text-gray-500">{t("chat.cloudTokenHint")}</p>
            </div>

            <div class="mt-5 flex justify-end gap-2">
              <button
                onClick={() => {
                  showCloudAuthDialog.value = false;
                  cloudAuthError.value = "";
                }}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50 transition-colors"
              >
                {t("chat.cloudCancel")}
              </button>
              <button
                onClick={handleSaveCloudToken}
                disabled={isSavingCloudToken.value}
                class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-700 disabled:opacity-60 transition-colors"
              >
                {isSavingCloudToken.value ? t("chat.cloudSavingToken") : t("chat.cloudSaveToken")}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function MessageBubble({ message, streaming, audioPreview }: { message: ChatMessage; streaming?: boolean; audioPreview?: LocalAudioPreview }) {
  const isUser = message.role === "user";
  const content = message.content;
  const plainText = typeof content === "string"
    ? content
    : (content as ContentPart[])
      .filter((part) => part.type === "text" && part.text)
      .map((part) => part.text)
      .join("\n");
  const sources = message.meta?.sources || [];

  const renderContent = () => {
    if (typeof content === "string") {
      if (!isUser) {
        const embeddingResponse = parseEmbeddingChatResponse(content);
        if (embeddingResponse) {
          return <EmbeddingDataTable response={embeddingResponse} />;
        }
        const parsed = parseReasoningText(content);
        if (parsed.hasThinking) {
          if (!parsed.thinkingOpen) {
            return parsed.answer ? <MarkdownContent text={parsed.answer} /> : null;
          }
          const thinkingLabel = t("chat.thinkingLive");
          const preview = thinkingPreview(parsed.thinking);
          return (
            <>
              <details
                class={`group rounded-xl border border-amber-200 bg-amber-50/70 px-3 py-2 ${
                  parsed.answer ? "mb-3" : ""
                }`}
              >
                <summary class="flex cursor-pointer select-none items-center gap-2 text-xs font-medium text-amber-700">
                  <span class="shrink-0">{thinkingLabel}</span>
                  {preview && (
                    <span class="min-w-0 flex-1 truncate font-normal text-amber-800/80 group-open:hidden">
                      {preview}
                    </span>
                  )}
                </summary>
                {parsed.thinking && (
                  <div class="mt-2 whitespace-pre-wrap text-xs leading-relaxed text-amber-900">
                    {parsed.thinking}
                  </div>
                )}
              </details>
              {parsed.answer && <MarkdownContent text={parsed.answer} />}
            </>
          );
        }
      }
      return isUser ? <p class="whitespace-pre-wrap">{content}</p> : <MarkdownContent text={content} />;
    }
    if (Array.isArray(content)) {
      return (
        <>
          {(content as ContentPart[]).map((part, i) => {
            if (part.type === "image_url" && part.image_url) {
              return <img key={i} src={part.image_url.url} class="max-w-full rounded-lg mb-2 max-h-64" />;
            }
            if (part.type === "text" && part.text) {
              return <p key={i} class="whitespace-pre-wrap">{part.text}</p>;
            }
            return null;
          })}
        </>
      );
    }
    return null;
  };

  return (
    <div class={`flex w-full ${isUser ? "justify-end" : "justify-start"}`}>
      {!isUser && (
        <div class="mr-3 mt-1 flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-blue-50 text-xs font-semibold text-blue-600">
          AI
        </div>
      )}
      <div
        class={`text-sm leading-relaxed ${
          isUser
            ? "max-w-[72%] rounded-[18px] bg-gray-100 px-4 py-3 text-gray-900"
            : "max-w-[760px] flex-1 text-gray-800"
        } ${streaming ? "animate-pulse" : ""}`}
      >
        {renderContent()}
        {isUser && audioPreview && (
          <div class="mt-3 rounded-xl border border-gray-200 bg-white/80 px-3 py-2">
            <div class="mb-1 truncate text-xs text-gray-500">
              {audioPreview.recorded ? t("chat.recordedAudio") : t("chat.audioFile")} · {formatBytes(audioPreview.size)}
            </div>
            <audio src={audioPreview.objectUrl} controls class="h-8 w-full max-w-[260px]" />
          </div>
        )}
        {!isUser && !streaming && (
          <div class="mt-3 flex flex-wrap items-center gap-2 text-xs text-gray-300">
            <button
              onClick={() => plainText && navigator.clipboard.writeText(plainText)}
              class="rounded-md p-1 text-gray-300 transition-colors hover:bg-gray-50 hover:text-gray-500"
              title={t("chat.copyResponse")}
            >
              <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
            </button>
            <button class="rounded-md p-1 text-gray-300 transition-colors hover:bg-gray-50 hover:text-gray-500" title={t("chat.likeResponse")}>
              <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M14 9V5a3 3 0 00-3-3l-4 9v11h11.28a2 2 0 001.95-1.56l1.38-6A2 2 0 0019.66 12H14zM7 22H4a2 2 0 01-2-2v-7a2 2 0 012-2h3" />
              </svg>
            </button>
            <button class="rounded-md p-1 text-gray-300 transition-colors hover:bg-gray-50 hover:text-gray-500" title={t("chat.dislikeResponse")}>
              <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M10 15v4a3 3 0 003 3l4-9V2H5.72a2 2 0 00-1.95 1.56l-1.38 6A2 2 0 004.34 12H10zM17 2h3a2 2 0 012 2v7a2 2 0 01-2 2h-3" />
              </svg>
            </button>
            <MessageSourceLinks sources={sources} />
            {message.meta && <span class="ml-auto shrink-0">{formatResponseMeta(message.meta)}</span>}
          </div>
        )}
      </div>
    </div>
  );
}

function AssistantProgress({ query, planningQuery, skippedReason, sources, content }: { query: string; planningQuery: string; skippedReason: string; sources: WebSearchResult[]; content: string }) {
  const parsed = content ? parseReasoningText(content) : null;
  const hasAnswer = Boolean(parsed?.answer) || (content && !parsed?.hasThinking);
  const isThinking = Boolean(content && parsed?.hasThinking && !parsed.answer);
  const steps = [
    {
      key: "planning",
      label: planningQuery ? t("chat.progressDecidingSearch") : t("chat.progressSkippedSearch"),
      done: Boolean(skippedReason || query || sources.length > 0 || content),
      active: Boolean(planningQuery),
      visible: Boolean(planningQuery || skippedReason),
    },
    {
      key: "search",
      label: query ? t("chat.progressSearching", query) : sources.length > 0 ? t("chat.progressSearched") : t("chat.progressPreparing"),
      done: sources.length > 0 || Boolean(content),
      active: Boolean(query),
      visible: Boolean(query || sources.length > 0),
    },
    {
      key: "sources",
      label: sources.length > 0 ? t("chat.progressFoundSources", sources.length) : t("chat.progressFindingSources"),
      done: sources.length > 0,
      active: !query && sources.length === 0 && !content,
      visible: Boolean(query || sources.length > 0),
    },
    {
      key: "thinking",
      label: isThinking ? t("chat.progressThinking") : hasAnswer ? t("chat.progressThought") : t("chat.progressThinking"),
      done: hasAnswer,
      active: isThinking || (!query && sources.length > 0 && !hasAnswer),
      visible: true,
    },
    {
      key: "answering",
      label: hasAnswer ? t("chat.progressAnswering") : t("chat.progressWaitingAnswer"),
      done: false,
      active: hasAnswer,
      visible: true,
    },
  ].filter((step) => step.visible);

  return (
    <div class="flex justify-start">
      <div class="ml-11 mb-1 w-full max-w-[760px] rounded-2xl border border-gray-100 bg-white px-4 py-3 shadow-sm">
        <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500">
          {steps.map((step, idx) => (
            <div key={step.key} class="flex items-center gap-2">
              <span
                class={`flex h-5 w-5 items-center justify-center rounded-full border text-[10px] ${
                  step.done
                    ? "border-blue-200 bg-blue-50 text-blue-600"
                    : step.active
                      ? "border-amber-200 bg-amber-50 text-amber-600"
                      : "border-gray-200 bg-gray-50 text-gray-400"
                }`}
              >
                {step.done ? (
                  <svg class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="3">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                ) : step.active ? <span class="h-2 w-2 animate-pulse rounded-full bg-current" /> : idx + 1}
              </span>
              <span class={step.active ? "font-medium text-gray-800" : "text-gray-500"}>{step.label}</span>
              {idx < steps.length - 1 && <span class="text-gray-200">/</span>}
            </div>
          ))}
        </div>
        {skippedReason && !query && sources.length === 0 && !hasAnswer && (
          <div class="mt-2 text-xs text-gray-400">{t("chat.progressSkippedReason", skippedReason)}</div>
        )}
      </div>
    </div>
  );
}

function SliderSetting({
  label,
  value,
  min,
  max,
  step,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (v: number) => void;
}) {
  return (
    <div>
      <div class="flex items-center justify-between mb-1.5">
        <label class="text-sm font-medium text-gray-700">{label}</label>
        <span class="text-sm text-gray-500 tabular-nums">{value}</span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onInput={(e) => onChange(parseFloat((e.target as HTMLInputElement).value))}
        class="w-full h-1.5 bg-gray-200 rounded-lg appearance-none cursor-pointer accent-indigo-600"
      />
    </div>
  );
}

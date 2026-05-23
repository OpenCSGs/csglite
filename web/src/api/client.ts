import { stripReasoningText } from "../reasoning";

export interface ModelInfo {
  name: string;
  model: string;
  size: number;
  format: string;
  modified_at: string;
  label?: string;
  display_name?: string;
  source?: string;
  provider?: string;
  category?: string;
  pipeline_tag?: string;
  input_modalities?: string[];
  output_modalities?: string[];
  has_mmproj?: boolean;
  context_window?: number;
  description?: string;
  license?: string;
  llm_type?: string;
  owned_by?: string;
  pricing?: ModelPricing;
}

export interface ModelPricing {
  input_token_price?: ModelTokenPrice;
  output_token_price?: ModelTokenPrice;
}

export interface ModelTokenPrice {
  currency?: string;
  price_per_million: number;
}

export interface LocalModelSearchResponse {
  query?: string;
  format?: string;
  pipeline_tag?: string;
  limit: number;
  offset: number;
  total: number;
  has_more: boolean;
  models: ModelInfo[];
}

export interface RunningModel {
  name: string;
  model: string;
  size: number;
  format: string;
  status?: "running" | "loading" | string;
  expires_at: string;
}

export interface ModelFileEntry {
  path: string;
  size: number;
  sha256?: string;
  lfs?: boolean;
  download_url: string;
}

export interface ModelManifestResponse {
  details: ModelInfo;
  files: ModelFileEntry[];
  local_inference: LocalInferenceSupport;
}

export interface ModelUploadResponse {
  status: string;
  model: string;
  details: ModelInfo;
  files: ModelFileEntry[];
}

export interface LocalModelUploadFile {
  file: File;
  path: string;
}

export interface LocalModelUploadOptions {
  model?: string;
  mode: "archive" | "directory" | "files";
  overwrite?: boolean;
  files: LocalModelUploadFile[];
}

export interface MarketplaceTag {
  name: string;
  category: string;
  show_name: string;
  group?: string;
  built_in?: boolean;
}

export interface MarketplaceRepository {
  http_clone_url?: string;
  ssh_clone_url?: string;
}

export interface MarketplaceModelMetadata {
  model_params?: number;
  tensor_type?: string;
  architecture?: string;
  mini_gpu_memory_gb?: number;
  mini_gpu_finetune_gb?: number;
  model_type?: string;
  class_name?: string;
}

export interface MarketplaceModel {
  id: number;
  name: string;
  path: string;
  description: string;
  likes: number;
  downloads: number;
  tags: MarketplaceTag[];
  license: string;
  created_at: string;
  updated_at: string;
  nickname?: string;
  repository_id?: number;
  private?: boolean;
  repository?: MarketplaceRepository;
  default_branch?: string;
  source?: string;
  sync_status?: string;
  metadata?: MarketplaceModelMetadata;
  hf_path?: string;
}

export interface MarketplaceModelQuantization {
  name: string;
  file_count: number;
  example_path: string;
}

export interface LocalInferenceSupport {
  supported: boolean;
  runtime?: "llama" | "diffusers";
  mode: "none" | "direct" | "convert" | "image";
  architecture?: string;
  runtime_architecture?: string;
}

export interface MarketplaceModelDetailResponse {
  details: MarketplaceModel;
  quantizations: MarketplaceModelQuantization[];
  local_inference: LocalInferenceSupport;
}

export interface MarketplaceDataset {
  id: number;
  name: string;
  path: string;
  description: string;
  likes: number;
  downloads: number;
  tags: MarketplaceTag[];
  license: string;
  created_at: string;
  updated_at: string;
}

export interface SystemInfo {
  cpu_cores: number;
  cpu_usage: number;
  cpu_clock: string;
  ram_used: number;
  ram_total: number;
  ram_info: string;
  gpu_name: string;
  gpu_vram_used: number;
  gpu_vram_total: number;
  gpu_usage_available: boolean;
  gpu_shared_memory: boolean;
}

export interface AppSettings {
  version: string;
  storage_dir: string;
  model_dir: string;
  dataset_dir: string;
  server_url: string;
  ai_gateway_url: string;
  default_server_url: string;
  default_ai_gateway_url: string;
  autostart: boolean;
  web_search: WebSearchSettings;
}

export interface WebSearchSettings {
  enabled: boolean;
  max_results: number;
  language?: string;
  providers?: string[];
  safe_search: number;
  timeout_seconds: number;
}

export interface ImageRuntimeStatus {
  ready: boolean;
  runtime_dir: string;
  venv_dir: string;
  python?: string;
  platform: string;
  arch: string;
  hardware: "cpu" | "mps" | "cuda" | "rocm";
  torch_index_url?: string;
  missing_packages?: string[];
  install_command?: string[];
  error?: string;
}

export interface ImageGenerationRequest {
  model: string;
  prompt: string;
  n?: number;
  size?: string;
  response_format?: "b64_json";
  seed?: number;
  negative_prompt?: string;
  steps?: number;
  cfg_scale?: number;
}

export interface ImageGenerationResponse {
  created: number;
  data: Array<{
    b64_json?: string;
    url?: string;
    revised_prompt?: string;
  }>;
}

export type ImageGenerationJobStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";

export interface ImageGenerationJobResponse {
  id: string;
  status: ImageGenerationJobStatus;
  created_at: string;
  updated_at: string;
  completed_at?: string;
  request: ImageGenerationRequest;
  result?: ImageGenerationResponse;
  error?: string;
}

export interface WebSearchResult {
  title: string;
  url: string;
  snippet?: string;
  engine?: string;
  category?: string;
  score?: number;
  published_at?: string;
}

export interface LocalAPIKeyInfo {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at?: string;
}

export interface LocalAPIKeysResponse {
  auth_enabled: boolean;
  keys: LocalAPIKeyInfo[];
}

export interface LocalAPIKeyCreateResponse {
  key: LocalAPIKeyInfo;
  api_key: string;
}

export interface LocalAPIUsageTotals {
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  local_tokens: number;
  cloud_tokens: number;
}

export interface LocalAPIUsageRow {
  api_key_id: string;
  api_key_name: string;
  model: string;
  source: string;
  source_type: string;
  source_name?: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  last_used_at: string;
}

export interface LocalAPIUsageSourceTotal {
  source: string;
  source_type: string;
  source_name?: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
}

export interface ProviderTagModelSelection {
  model: string;
  display_name?: string;
  description?: string;
}

export interface ProviderTagModelUpdateRequest {
  model?: string;
  display_name?: string;
  description?: string;
}

export interface LocalAPIUsageSummarySeries {
  name: string;
  type: "line";
  data: number[];
}

export interface LocalAPIUsageTotalSummary {
  xAxis: string[];
  series: LocalAPIUsageSummarySeries[];
}

export interface LocalAPIUsageResponse {
  period: string;
  from?: string;
  totals: LocalAPIUsageTotals;
  total_history: number;
  total_summary: LocalAPIUsageTotalSummary;
  source_totals: LocalAPIUsageSourceTotal[];
  rows: LocalAPIUsageRow[];
}

export interface LocalDirectoryEntry {
  name: string;
  path: string;
}

export interface LocalDirectoryBrowseResponse {
  current_path: string;
  parent_path?: string;
  home_path?: string;
  roots: string[];
  entries: LocalDirectoryEntry[];
}

export interface AIAppInfo {
  id: string;
  installed: boolean;
  managed: boolean;
  supported: boolean;
  disabled: boolean;
  status: "idle" | "installing" | "uninstalling" | "installed" | "failed" | "disabled";
  phase?: string;
  progress_mode: "percent" | "indeterminate";
  progress?: number;
  install_path?: string;
  version?: string;
  latest_version?: string;
  update_available?: boolean;
  model_id?: string;
  runtime_supported: boolean;
  runtime_running: boolean;
  runtime_status?: "running" | "stopped";
  log_path?: string;
  last_error?: string;
  disabled_reason?: string;
  updated_at: string;
}

export interface AIAppOpenResponse {
  url: string;
}

export interface CloudAuthStatus {
  auth_mode: string;
  has_token: boolean;
  authenticated: boolean;
  login_url: string;
  access_token_url: string;
  has_api_key: boolean;
  api_key_source?: "manual" | "builtin" | string;
  api_key_prefix?: string;
  api_key_error?: string;
  user?: CloudAuthUser | null;
}

export interface CloudAuthUser {
  username: string;
  nickname?: string;
  email?: string;
  avatar?: string;
  uuid?: string;
}

export type ChatContent = string | ContentPart[];

export interface ContentPart {
  type: "text" | "image_url";
  text?: string;
  image_url?: { url: string };
}

export interface ChatMessage {
  role: string;
  content: ChatContent;
  meta?: ChatMessageMeta;
}

export interface ChatMessageMeta {
  tokens?: number;
  speed?: number;
  duration_ms?: number;
  estimated?: boolean;
  sources?: WebSearchResult[];
}

export interface PullProgress {
  status: string;
  digest?: string;
  total?: number;
  completed?: number;
}

function previewResponseBody(text: string): string {
  return text.replace(/\s+/g, " ").trim().slice(0, 160);
}

function isLikelyHTML(text: string): boolean {
  const normalized = text.trim().toLowerCase();
  return normalized.startsWith("<!doctype html") || normalized.startsWith("<html");
}

function unexpectedJSONError(url: string, contentType: string, body: string): Error {
  if (isLikelyHTML(body)) {
    return new Error(`Expected JSON from ${url}, but received HTML. Check the API server port or dev proxy target.`);
  }

  const preview = previewResponseBody(body);
  if (preview) {
    return new Error(`Expected JSON from ${url}, but received ${contentType || "non-JSON response"}: ${preview}`);
  }

  return new Error(`Expected JSON from ${url}, but received ${contentType || "non-JSON response"}.`);
}

function extractErrorMessage(body: string, contentType: string, fallback: string): string {
  if (contentType.includes("application/json")) {
    try {
      const parsed = JSON.parse(body) as {
        error?: string | { message?: string };
        msg?: string;
        message?: string;
      };
      if (typeof parsed.error === "string" && parsed.error.trim()) {
        return parsed.error.trim();
      }
      if (parsed.error && typeof parsed.error === "object" && typeof parsed.error.message === "string" && parsed.error.message.trim()) {
        return parsed.error.message.trim();
      }
      if (typeof parsed.msg === "string" && parsed.msg.trim()) {
        return parsed.msg.trim();
      }
      if (typeof parsed.message === "string" && parsed.message.trim()) {
        return parsed.message.trim();
      }
    } catch {
      /* ignore */
    }
  }

  const preview = previewResponseBody(body);
  return preview || fallback;
}

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(url, init);
  const contentType = resp.headers.get("content-type") || "";
  const body = await resp.text();

  if (!resp.ok) {
    throw new Error(extractErrorMessage(body, contentType, resp.statusText));
  }

  if (!contentType.includes("application/json")) {
    throw unexpectedJSONError(url, contentType, body);
  }

  try {
    return JSON.parse(body) as T;
  } catch {
    throw unexpectedJSONError(url, contentType, body);
  }
}

export async function getTags(options?: { refresh?: boolean }): Promise<ModelInfo[]> {
  const query = new URLSearchParams();
  if (options?.refresh) {
    query.set("refresh", "1");
  }
  const url = query.toString() ? `/api/tags?${query}` : "/api/tags";
  const data = await fetchJSON<{ models: ModelInfo[] }>(url);
  return data.models || [];
}

export async function getProviderSelectedTags(provider: string): Promise<ModelInfo[]> {
  const query = new URLSearchParams({ provider });
  const data = await fetchJSON<{ models: ModelInfo[] }>(`/api/tags?${query}`);
  return data.models || [];
}

export async function getProviderManageTags(provider: string, category?: string): Promise<ModelInfo[]> {
  const query = new URLSearchParams({ provider });
  if (category?.trim()) query.set("category", category.trim());
  const data = await fetchJSON<{ models: ModelInfo[] }>(`/api/tags/manage?${query}`);
  return data.models || [];
}

export async function replaceProviderManageTags(provider: string, models: ProviderTagModelSelection[], category?: string): Promise<ModelInfo[]> {
  const query = new URLSearchParams({ provider });
  if (category?.trim()) query.set("category", category.trim());
  const data = await fetchJSON<{ models: ModelInfo[] }>(`/api/tags/manage?${query}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ models }),
  });
  return data.models || [];
}

export async function addProviderManageTag(provider: string, model: string): Promise<ModelInfo> {
  const query = new URLSearchParams({ provider });
  return fetchJSON<ModelInfo>(`/api/tags/manage?${query}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model }),
  });
}

export async function updateProviderManageTag(provider: string, currentModel: string, req: ProviderTagModelUpdateRequest): Promise<ModelInfo> {
  const query = new URLSearchParams({ provider, model: currentModel });
  return fetchJSON<ModelInfo>(`/api/tags/manage?${query}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function deleteProviderManageTag(provider: string, model: string): Promise<void> {
  const query = new URLSearchParams({ provider, model });
  await fetchJSON<{ status: string }>(`/api/tags/manage?${query}`, {
    method: "DELETE",
  });
}

export async function searchLocalModels(params?: {
  q?: string;
  format?: string;
  pipeline_tag?: string;
  limit?: number;
  offset?: number;
}): Promise<LocalModelSearchResponse> {
  const query = new URLSearchParams();
  if (params?.q?.trim()) query.set("q", params.q.trim());
  if (params?.format?.trim()) query.set("format", params.format.trim());
  if (params?.pipeline_tag?.trim()) query.set("pipeline_tag", params.pipeline_tag.trim());
  if (typeof params?.limit === "number") query.set("limit", String(params.limit));
  if (typeof params?.offset === "number") query.set("offset", String(params.offset));
  const url = query.toString() ? `/api/models/search?${query}` : "/api/models/search";
  return fetchJSON<LocalModelSearchResponse>(url);
}

function splitModelID(model: string): { namespace: string; name: string } {
  const trimmed = model.trim();
  const slash = trimmed.indexOf("/");
  if (slash <= 0 || slash === trimmed.length - 1) {
    throw new Error(`Invalid model ID: ${model}`);
  }
  return {
    namespace: trimmed.slice(0, slash),
    name: trimmed.slice(slash + 1),
  };
}

export async function getModelManifest(model: string): Promise<ModelManifestResponse> {
  const { namespace, name } = splitModelID(model);
  return fetchJSON<ModelManifestResponse>(`/api/models/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/manifest`);
}

export function uploadLocalModel(
  options: LocalModelUploadOptions,
  onProgress?: (percent: number) => void
): Promise<ModelUploadResponse> {
  return uploadLocalModelSession(options, onProgress);
}

async function uploadLocalModelSession(
  options: LocalModelUploadOptions,
  onProgress?: (percent: number) => void
): Promise<ModelUploadResponse> {
  const start = await fetchJSON<{ upload_id: string }>("/api/models/upload/start", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model: options.model?.trim() || undefined,
      mode: options.mode,
      overwrite: !!options.overwrite,
    }),
  });
  const uploadID = start.upload_id;
  const totalBytes = options.files.reduce((sum, item) => sum + (item.file.size || 0), 0);
  let completedBytes = 0;
  try {
    for (const item of options.files) {
      await uploadLocalModelSessionFile(uploadID, item, (loaded) => {
        if (totalBytes > 0) {
          onProgress?.(Math.min(99, Math.round(((completedBytes + loaded) / totalBytes) * 100)));
        }
      });
      completedBytes += item.file.size || 0;
      if (totalBytes > 0) {
        onProgress?.(Math.min(99, Math.round((completedBytes / totalBytes) * 100)));
      }
    }
    const resp = await fetchJSON<ModelUploadResponse>(`/api/models/upload/${encodeURIComponent(uploadID)}/complete`, {
      method: "POST",
    });
    onProgress?.(100);
    return resp;
  } catch (err) {
    try {
      await fetchJSON(`/api/models/upload/${encodeURIComponent(uploadID)}`, { method: "DELETE" });
    } catch {
      /* ignore cleanup errors */
    }
    throw err;
  }
}

function uploadLocalModelSessionFile(
  uploadID: string,
  item: LocalModelUploadFile,
  onProgress?: (loaded: number) => void
): Promise<void> {
  return new Promise((resolve, reject) => {
    const path = item.path || item.file.name;
    const params = new URLSearchParams();
    params.set("path", path);
    params.set("filename", item.file.name);
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", `/api/models/upload/${encodeURIComponent(uploadID)}/file?${params.toString()}`);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        onProgress?.(event.loaded);
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
        return;
      }
      let data: any = null;
      try {
        data = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        /* ignore parse errors */
      }
      reject(new Error(data?.error || data?.message || "upload failed"));
    };
    xhr.onerror = () => reject(new Error("upload connection failed"));
    xhr.onabort = () => reject(new Error("upload aborted"));
    xhr.ontimeout = () => reject(new Error("upload timed out"));
    xhr.send(item.file);
  });
}

export async function getPs(): Promise<RunningModel[]> {
  const data = await fetchJSON<{ models: RunningModel[] }>("/api/ps");
  return data.models || [];
}

export async function getCloudAuthStatus(): Promise<CloudAuthStatus> {
  return fetchJSON<CloudAuthStatus>("/api/cloud/auth");
}

export async function getSettings(): Promise<AppSettings> {
  return fetchJSON<AppSettings>("/api/settings");
}

export async function getImageRuntimeStatus(): Promise<ImageRuntimeStatus> {
  return fetchJSON<ImageRuntimeStatus>("/api/image-runtime");
}

export async function installImageRuntime(options?: { upgrade_packages?: boolean }): Promise<ImageRuntimeStatus> {
  return fetchJSON<ImageRuntimeStatus>("/api/image-runtime/install", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ upgrade_packages: options?.upgrade_packages || undefined }),
  });
}

export async function generateImage(req: ImageGenerationRequest): Promise<ImageGenerationResponse> {
  return fetchJSON<ImageGenerationResponse>("/v1/images/generations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...req, response_format: req.response_format || "b64_json" }),
  });
}

export async function createImageGenerationJob(req: ImageGenerationRequest): Promise<ImageGenerationJobResponse> {
  return fetchJSON<ImageGenerationJobResponse>("/api/images/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...req, response_format: req.response_format || "b64_json" }),
  });
}

export async function getImageGenerationJob(id: string): Promise<ImageGenerationJobResponse> {
  return fetchJSON<ImageGenerationJobResponse>(`/api/images/jobs/${encodeURIComponent(id)}`);
}

export async function getImageGenerationJobResult(id: string): Promise<ImageGenerationResponse> {
  return fetchJSON<ImageGenerationResponse>(`/api/images/jobs/${encodeURIComponent(id)}/result`);
}

export async function cancelImageGenerationJob(id: string): Promise<ImageGenerationJobResponse> {
  return fetchJSON<ImageGenerationJobResponse>(`/api/images/jobs/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export async function saveSettings(patch: {
  storage_dir?: string;
  model_dir?: string;
  dataset_dir?: string;
  server_url?: string;
  ai_gateway_url?: string;
  autostart?: boolean;
  web_search?: WebSearchSettings;
}): Promise<AppSettings> {
  return fetchJSON<AppSettings>("/api/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
}

export async function getLocalAPIKeys(): Promise<LocalAPIKeysResponse> {
  return fetchJSON<LocalAPIKeysResponse>("/api/api-keys");
}

export async function updateLocalAPIKeySettings(authEnabled: boolean): Promise<LocalAPIKeysResponse> {
  return fetchJSON<LocalAPIKeysResponse>("/api/api-keys/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ auth_enabled: authEnabled }),
  });
}

export async function createLocalAPIKey(name: string): Promise<LocalAPIKeyCreateResponse> {
  return fetchJSON<LocalAPIKeyCreateResponse>("/api/api-keys", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
}

export async function deleteLocalAPIKey(id: string): Promise<void> {
  await fetchJSON<{ status: string }>(`/api/api-keys/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export async function getLocalAPIUsage(period?: string, provider?: string): Promise<LocalAPIUsageResponse> {
  const params = new URLSearchParams();
  if (period) params.set("period", period);
  if (provider) params.set("provider", provider);
  const query = params.toString() ? `?${params}` : "";
  return fetchJSON<LocalAPIUsageResponse>(`/api/api-usage${query}`);
}

export async function browseLocalDirectories(path?: string): Promise<LocalDirectoryBrowseResponse> {
  return fetchJSON<LocalDirectoryBrowseResponse>("/api/settings/directories", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path: path || "" }),
  });
}

export async function saveCloudToken(token: string): Promise<CloudAuthStatus> {
  return fetchJSON<CloudAuthStatus>("/api/cloud/auth/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ token }),
  });
}

export async function clearCloudToken(): Promise<CloudAuthStatus> {
  return fetchJSON<CloudAuthStatus>("/api/cloud/auth/token", {
    method: "DELETE",
  });
}

export async function saveCloudAPIKey(apiKey: string): Promise<CloudAuthStatus> {
  return fetchJSON<CloudAuthStatus>("/api/cloud/api-key", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ api_key: apiKey }),
  });
}

export async function clearCloudAPIKey(): Promise<CloudAuthStatus> {
  return fetchJSON<CloudAuthStatus>("/api/cloud/api-key", {
    method: "DELETE",
  });
}

export async function stopModel(model: string): Promise<void> {
  await fetchJSON("/api/stop", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model }),
  });
}

export async function deleteModel(model: string): Promise<void> {
  await fetchJSON("/api/delete", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model }),
  });
}

export async function showModel(model: string) {
  return fetchJSON<{ details: ModelInfo }>("/api/show", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model }),
  });
}

export function pullModel(
  model: string,
  onProgress: (p: PullProgress) => void,
  signal?: AbortSignal
): Promise<void> {
  return new Promise((resolve, reject) => {
    fetch("/api/pull", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model }),
      signal,
    })
      .then((resp) => {
        if (!resp.ok || !resp.body) {
          reject(new Error("pull failed"));
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        let lastUpdate = 0;
        let pending: PullProgress | null = null;
        let flushTimer = 0;

        function flushPending() {
          if (pending) {
            onProgress(pending);
            pending = null;
          }
        }

        function processLine(line: string) {
          if (!line.startsWith("data: ")) return;
          try {
            const p: PullProgress = JSON.parse(line.slice(6));
            if (p.status === "success" || p.status.startsWith("error")) {
              clearTimeout(flushTimer);
              pending = null;
              onProgress(p);
              return;
            }
            const now = Date.now();
            if (now - lastUpdate >= 200) {
              lastUpdate = now;
              onProgress(p);
            } else {
              pending = p;
              clearTimeout(flushTimer);
              flushTimer = window.setTimeout(flushPending, 200);
            }
          } catch {
            /* skip */
          }
        }

        function read(): Promise<void> {
          return reader.read().then(({ done, value }) => {
            if (done) {
              clearTimeout(flushTimer);
              flushPending();
              resolve();
              return;
            }
            buf += decoder.decode(value, { stream: true });
            const lines = buf.split("\n");
            buf = lines.pop() || "";
            for (const line of lines) {
              processLine(line);
            }
            return read();
          });
        }

        read().catch((err) => {
          clearTimeout(flushTimer);
          reject(err);
        });
      })
      .catch(reject);
  });
}

function stripReasoningTags(text: string): string {
  return stripReasoningText(text);
}

function sanitizeMessageForAPI(m: ChatMessage): ChatMessage {
  if (typeof m.content === "string") {
    if (m.role === "assistant") {
      return { ...m, content: stripReasoningTags(m.content) };
    }
    return m;
  }

  const parts = (m.content as ContentPart[]).map((p) => {
    if (p.type === "text" && p.text && m.role === "assistant") {
      return { ...p, text: stripReasoningTags(p.text) };
    }
    return p;
  });
  return { ...m, content: parts };
}

function stripImagesFromOldMessages(msgs: ChatMessage[]): ChatMessage[] {
  if (msgs.length <= 1) return msgs;
  return msgs.map((m, i) => {
    if (i === msgs.length - 1) return sanitizeMessageForAPI(m);
    if (!Array.isArray(m.content)) return sanitizeMessageForAPI(m);
    const textParts = (m.content as ContentPart[])
      .filter((p) => p.type === "text")
      .map((p) => p.text || "")
      .join("");
    return sanitizeMessageForAPI({ ...m, content: textParts || "(image)" });
  });
}

// -- Conversation history API --

export interface ConversationMeta {
  id: string;
  title: string;
  model?: string;
  created_at: string;
  updated_at: string;
  msg_count: number;
}

export interface ConversationSettings {
  num_ctx?: number;
  num_parallel?: number;
}

export interface Conversation {
  id: string;
  title: string;
  model?: string;
  created_at: string;
  updated_at: string;
  messages: ChatMessage[];
  settings?: ConversationSettings;
}

export async function listConversations(): Promise<ConversationMeta[]> {
  const data = await fetchJSON<{ conversations: ConversationMeta[] }>("/api/conversations");
  return data.conversations || [];
}

export async function getConversation(id: string): Promise<Conversation> {
  return fetchJSON<Conversation>(`/api/conversations/${encodeURIComponent(id)}`);
}

export async function createConversation(init?: Partial<Conversation>): Promise<Conversation> {
  return fetchJSON<Conversation>("/api/conversations", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(init || {}),
  });
}

export async function updateConversation(id: string, patch: Partial<Conversation>): Promise<Conversation> {
  return fetchJSON<Conversation>(`/api/conversations/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(patch),
  });
}

export async function deleteConversation(id: string): Promise<void> {
  await fetchJSON<{ status: string }>(`/api/conversations/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export function streamChat(
  model: string,
  messages: ChatMessage[],
  options: { temperature?: number; top_p?: number; max_tokens?: number; num_ctx?: number; num_parallel?: number; system?: string; source?: string; web_search?: { enabled: boolean; query?: string } },
  onToken: (token: string, done: boolean) => void,
  signal?: AbortSignal,
  onSearching?: (query: string) => void,
  onSearchResults?: (query: string, results: WebSearchResult[]) => void,
  onSearchError?: (message: string) => void,
  onSearchPlanning?: (query: string) => void,
  onSearchSkipped?: (reason: string) => void,
  onSearchRoute?: (route: { action?: string; reason?: string; confidence?: number }) => void,
): Promise<void> {
  let msgs = stripImagesFromOldMessages([...messages]);
  if (options.system) {
    msgs.unshift({ role: "system", content: options.system });
  }

  return new Promise((resolve, reject) => {
    fetch("/api/chat", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "text/event-stream",
        "X-CSGHUB-Stream": "sse",
        "X-CSGHUB-Disable-Thinking": "true",
      },
      body: JSON.stringify({
        model,
        source: options.source,
        messages: msgs,
        stream: true,
        web_search: options.web_search,
        options: {
          temperature: options.temperature,
          top_p: options.top_p,
          max_tokens: options.max_tokens,
          num_ctx: options.num_ctx,
          num_parallel: options.num_parallel,
        },
      }),
      signal,
    })
      .then(async (resp) => {
        if (!resp.ok) {
          const contentType = resp.headers.get("content-type") || "";
          const errText = await resp.text().catch(() => resp.statusText);
          reject(new Error(`Error ${resp.status}: ${extractErrorMessage(errText, contentType, resp.statusText)}`));
          return;
        }
        if (!resp.body) {
          reject(new Error("No response body"));
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        function read(): Promise<void> {
          return reader.read().then(({ done, value }) => {
            if (done) {
              resolve();
              return;
            }
            buf += decoder.decode(value, { stream: true });
            const lines = buf.split("\n");
            buf = lines.pop() || "";
            for (const line of lines) {
              if (line.startsWith("data: ")) {
                try {
                  const data = JSON.parse(line.slice(6));
                  if (data.search_route && onSearchRoute) {
                    onSearchRoute(data.search_route);
                  } else if (data.search_planning && onSearchPlanning) {
                    onSearchPlanning(String(data.search_planning));
                  } else if (data.search_skipped && onSearchSkipped) {
                    onSearchSkipped(String(data.search_skipped));
                  } else if (data.searching && onSearching) {
                    onSearching(data.searching);
                  } else if (Array.isArray(data.search_results) && onSearchResults) {
                    onSearchResults(data.search_query || "", data.search_results as WebSearchResult[]);
                  } else if (data.search_error && onSearchError) {
                    onSearchError(String(data.search_error));
                  } else if (data.message?.content) {
                    onToken(data.message.content, false);
                  }
                  if (data.done) {
                    onToken("", true);
                  }
                } catch {
                  /* skip */
                }
              }
            }
            return read();
          });
        }

        read().catch(reject);
      })
      .catch(reject);
  });
}

export interface LoadProgress {
  status: string;
  step?: string;
  current?: number;
  total?: number;
}

export interface LoadModelOptions {
  keep_alive?: string;
  num_ctx?: number;
  num_parallel?: number;
  n_gpu_layers?: number;
  cache_type_k?: string;
  cache_type_v?: string;
  dtype?: string;
}

export function loadModel(
  model: string,
  onProgress: (p: LoadProgress) => void,
  options?: LoadModelOptions,
  signal?: AbortSignal
): Promise<void> {
  return new Promise((resolve, reject) => {
    fetch("/api/load", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model,
        stream: true,
        keep_alive: options?.keep_alive || undefined,
        num_ctx: options?.num_ctx,
        num_parallel: options?.num_parallel,
        n_gpu_layers: options?.n_gpu_layers,
        cache_type_k: options?.cache_type_k || undefined,
        cache_type_v: options?.cache_type_v || undefined,
        dtype: options?.dtype || undefined,
      }),
      signal,
    })
      .then(async (resp) => {
        if (!resp.ok) {
          const contentType = resp.headers.get("content-type") || "";
          const errText = await resp.text().catch(() => resp.statusText);
          reject(new Error(extractErrorMessage(errText, contentType, resp.statusText || "load failed")));
          return;
        }
        if (!resp.body) {
          reject(new Error("load failed"));
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        let settled = false;

        function processLine(line: string) {
          if (!line.startsWith("data: ")) return;
          try {
            const p: LoadProgress = JSON.parse(line.slice(6));
            onProgress(p);
            if (p.status === "ready") {
              settled = true;
              resolve();
            } else if (p.status.startsWith("error") || p.status.startsWith("image_runtime_required")) {
              settled = true;
              reject(new Error(p.status));
            }
          } catch {
            /* skip */
          }
        }

        function read(): Promise<void> {
          return reader.read().then(({ done, value }) => {
            if (done) {
              if (!settled) {
                reject(new Error("load did not reach ready state"));
              }
              return;
            }
            buf += decoder.decode(value, { stream: true });
            const lines = buf.split("\n");
            buf = lines.pop() || "";
            for (const line of lines) {
              processLine(line);
            }
            return read();
          });
        }

        read().catch(reject);
      })
      .catch(reject);
  });
}

export async function runModel(model: string): Promise<void> {
  const stream = false;
  await fetchJSON("/api/generate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model, prompt: "hi", stream }),
  });
}

export interface DatasetInfo {
  name: string;
  dataset: string;
  size: number;
  files: number;
  modified_at: string;
  description?: string;
  license?: string;
}

export interface DatasetDownloadFile {
  path: string;
  size: number;
  sha256?: string;
  lfs?: boolean;
  download_url: string;
}

export interface DatasetManifestResponse {
  details: DatasetInfo;
  files: DatasetDownloadFile[];
}

export async function getDatasetTags(): Promise<DatasetInfo[]> {
  const data = await fetchJSON<{ datasets: DatasetInfo[] }>("/api/datasets");
  return data.datasets || [];
}

export async function searchDatasets(query: string, limit = 20, offset = 0): Promise<{ datasets: DatasetInfo[]; total: number; has_more: boolean }> {
  const params = new URLSearchParams({ q: query, limit: String(limit), offset: String(offset) });
  const data = await fetchJSON<{ datasets: DatasetInfo[]; total: number; has_more: boolean }>(`/api/datasets/search?${params}`);
  return { datasets: data.datasets || [], total: data.total || 0, has_more: data.has_more || false };
}

export async function getDatasetManifest(dataset: string): Promise<DatasetManifestResponse> {
  const { namespace, name } = splitModelID(dataset);
  return fetchJSON<DatasetManifestResponse>(`/api/datasets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/manifest`);
}

export function pullDataset(
  dataset: string,
  onProgress: (p: PullProgress) => void,
  signal?: AbortSignal
): Promise<void> {
  return new Promise((resolve, reject) => {
    fetch("/api/datasets/pull", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ dataset }),
      signal,
    })
      .then((resp) => {
        if (!resp.ok || !resp.body) {
          reject(new Error("pull failed"));
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        let lastUpdate = 0;
        let pending: PullProgress | null = null;
        let flushTimer = 0;

        function flushPending() {
          if (pending) {
            onProgress(pending);
            pending = null;
          }
        }

        function processLine(line: string) {
          if (!line.startsWith("data: ")) return;
          try {
            const p: PullProgress = JSON.parse(line.slice(6));
            if (p.status === "success" || p.status.startsWith("error")) {
              clearTimeout(flushTimer);
              pending = null;
              onProgress(p);
              return;
            }
            const now = Date.now();
            if (now - lastUpdate >= 200) {
              lastUpdate = now;
              onProgress(p);
            } else {
              pending = p;
              clearTimeout(flushTimer);
              flushTimer = window.setTimeout(flushPending, 200);
            }
          } catch {
            /* skip */
          }
        }

        function read(): Promise<void> {
          return reader.read().then(({ done, value }) => {
            if (done) {
              clearTimeout(flushTimer);
              flushPending();
              resolve();
              return;
            }
            buf += decoder.decode(value, { stream: true });
            const lines = buf.split("\n");
            buf = lines.pop() || "";
            for (const line of lines) {
              processLine(line);
            }
            return read();
          });
        }

        read().catch((err) => {
          clearTimeout(flushTimer);
          reject(err);
        });
      })
      .catch(reject);
  });
}

export async function deleteDataset(dataset: string): Promise<void> {
  await fetchJSON("/api/datasets/delete", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ dataset }),
  });
}

export interface DatasetFileEntry {
  name: string;
  size: number;
  is_dir: boolean;
  modified_at: string;
}

export interface DatasetFilesResponse {
  dataset: string;
  path: string;
  entries: DatasetFileEntry[];
}

export async function getDatasetFiles(
  dataset: string,
  path: string
): Promise<DatasetFilesResponse> {
  return fetchJSON<DatasetFilesResponse>("/api/datasets/files", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ dataset, path }),
  });
}

export async function getMarketplaceModels(params: {
  search?: string;
  sort?: string;
  framework?: string;
  page?: number;
  per?: number;
}): Promise<{ data: MarketplaceModel[]; total: number }> {
  const q = new URLSearchParams();
  if (params.search) q.set("search", params.search);
  q.set("sort", params.sort || "trending");
  if (params.framework) q.set("framework", params.framework);
  q.set("page", String(params.page || 1));
  q.set("per", String(params.per || 16));
  const resp = await fetchJSON<{ data: MarketplaceModel[]; total: number }>(
    `/api/marketplace/models?${q}`
  );
  return resp;
}

export async function getMarketplaceModelDetail(model: string): Promise<MarketplaceModelDetailResponse> {
  const { namespace, name } = splitModelID(model);
  return fetchJSON<MarketplaceModelDetailResponse>(
    `/api/marketplace/models/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}`
  );
}

export async function getMarketplaceDatasets(params: {
  search?: string;
  sort?: string;
  page?: number;
  per?: number;
}): Promise<{ data: MarketplaceDataset[]; total: number }> {
  const q = new URLSearchParams();
  if (params.search) q.set("search", params.search);
  q.set("sort", params.sort || "trending");
  q.set("page", String(params.page || 1));
  q.set("per", String(params.per || 16));
  const resp = await fetchJSON<{ data: MarketplaceDataset[]; total: number }>(
    `/api/marketplace/datasets?${q}`
  );
  return resp;
}

export async function getSystemInfo(): Promise<SystemInfo> {
  return fetchJSON<SystemInfo>("/api/system");
}

export async function getAIApps(): Promise<AIAppInfo[]> {
  const data = await fetchJSON<{ apps: AIAppInfo[] }>("/api/apps");
  return data.apps || [];
}

export async function installAIApp(appId: string): Promise<AIAppInfo> {
  return fetchJSON<AIAppInfo>("/api/apps/install", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appId }),
  });
}

export async function uninstallAIApp(appId: string): Promise<AIAppInfo> {
  return fetchJSON<AIAppInfo>("/api/apps/uninstall", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appId }),
  });
}

export async function startAIApp(appId: string, modelId?: string, source?: string): Promise<AIAppInfo> {
  return fetchJSON<AIAppInfo>("/api/apps/start", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appId, model_id: modelId, source }),
  });
}

export async function stopAIApp(appId: string): Promise<AIAppInfo> {
  return fetchJSON<AIAppInfo>("/api/apps/stop", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appId }),
  });
}

export async function openAIApp(appId: string, modelId?: string, workDir?: string, source?: string): Promise<AIAppOpenResponse> {
  return fetchJSON<AIAppOpenResponse>("/api/apps/open", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      app_id: appId,
      model_id: modelId || undefined,
      source: source || undefined,
      work_dir: workDir || undefined,
    }),
  });
}

export async function saveAIAppModel(appId: string, modelId: string, source?: string): Promise<void> {
  await fetch("/api/apps/model", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appId, model_id: modelId, source: source || undefined }),
  });
}

export function streamLogs(
  onLog: (line: string) => void,
  signal?: AbortSignal
): void {
  const evtSource = new EventSource("/api/logs");
  evtSource.onmessage = (e) => onLog(e.data);
  evtSource.onerror = () => {
    evtSource.close();
  };
  signal?.addEventListener("abort", () => evtSource.close());
}

export function streamAIAppLogs(
  appId: string,
  onLog: (line: string) => void,
  signal?: AbortSignal
): void {
  const q = new URLSearchParams({ app_id: appId });
  const evtSource = new EventSource(`/api/apps/logs?${q}`);
  evtSource.onmessage = (e) => onLog(e.data);
  evtSource.onerror = () => {
    evtSource.close();
  };
  signal?.addEventListener("abort", () => evtSource.close());
}

// Upgrade API
export interface UpgradeCheckResponse {
  current_version: string;
  latest_version: string;
  update_available: boolean;
  release_notes?: string;
  release_url?: string;
}

export async function checkUpgrade(): Promise<UpgradeCheckResponse> {
  return fetchJSON<UpgradeCheckResponse>("/api/upgrade/check");
}

export interface UpgradeProgressEvent {
  status: string;
  progress?: number;
  message?: string;
  version?: string;
}

export function upgradeWithProgress(
  onProgress: (event: UpgradeProgressEvent) => void,
  signal?: AbortSignal
): Promise<void> {
  return new Promise((resolve, reject) => {
    fetch("/api/upgrade", { method: "POST", signal })
      .then((resp) => {
        if (!resp.ok || !resp.body) {
          reject(new Error("upgrade failed"));
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        function processLine(line: string) {
          if (!line.startsWith("data: ")) return;
          try {
            onProgress(JSON.parse(line.slice(6)));
          } catch {
            /* skip malformed SSE frames */
          }
        }

        function read(): Promise<void> {
          return reader.read().then(({ done, value }) => {
            if (done) {
              if (buf.trim()) processLine(buf.trim());
              resolve();
              return;
            }
            buf += decoder.decode(value, { stream: true });
            const lines = buf.split("\n");
            buf = lines.pop() || "";
            for (const line of lines) processLine(line);
            return read();
          });
        }

        read().catch(reject);
      })
      .catch(reject);
  });
}

// Third-party Provider API
export interface ThirdPartyProvider {
  id: string;
  name: string;
  base_url: string;
  api_key?: string;
  provider?: string;
  enabled: boolean;
}

export interface ThirdPartyProvidersResponse {
  providers: ThirdPartyProvider[];
}

export interface ThirdPartyProviderCreateRequest {
  name: string;
  base_url: string;
  api_key: string;
  provider?: string;
  enabled: boolean;
}

export interface ThirdPartyProviderUpdateRequest {
  name?: string;
  base_url?: string;
  api_key?: string;
  provider?: string;
  enabled?: boolean;
}

export interface ThirdPartyProviderValidateRequest {
  id?: string;
  name?: string;
  base_url: string;
  api_key?: string;
  provider?: string;
  enabled: boolean;
}

export interface ThirdPartyProviderValidateResponse {
  valid: boolean;
  model_count: number;
}

export async function getProviders(): Promise<ThirdPartyProvider[]> {
  const resp = await fetchJSON<ThirdPartyProvidersResponse>("/api/providers?source=third_party");
  return resp.providers || [];
}

export async function validateProvider(req: ThirdPartyProviderValidateRequest): Promise<ThirdPartyProviderValidateResponse> {
  return fetchJSON<ThirdPartyProviderValidateResponse>("/api/providers/validate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function createProvider(req: ThirdPartyProviderCreateRequest): Promise<ThirdPartyProvider> {
  return fetchJSON<ThirdPartyProvider>("/api/providers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function updateProvider(id: string, req: ThirdPartyProviderUpdateRequest): Promise<ThirdPartyProvider> {
  return fetchJSON<ThirdPartyProvider>(`/api/providers/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function deleteProvider(id: string): Promise<void> {
  await fetchJSON<{ status: string }>(`/api/providers/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

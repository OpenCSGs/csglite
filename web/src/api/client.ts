import { stripReasoningText } from "../reasoning";
import { locale } from "../i18n";

export interface ModelInfo {
  name: string;
  model: string;
  size: number;
  format: string;
  modified_at: string;
  label?: string;
  display_name?: string;
  source?: string;
  origin?: string;
  provider?: string;
  artifact_source?: ArtifactSource;
  repository?: string;
  requested_revision?: string;
  resolved_revision?: string;
  category?: string;
  pipeline_tag?: string;
  input_modalities?: string[];
  output_modalities?: string[];
  has_mmproj?: boolean;
  context_window?: number;
  max_model_len?: number;
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
  /** Current load/conversion step for models in the loading state. */
  step?: string;
  step_current?: number;
  step_total?: number;
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

export interface ModelConfigResponse {
  model: string;
  // Per-model context window; 0 means the model follows the global setting.
  num_ctx: number;
  // Context length declared by the model itself; 0 when unknown.
  model_max_num_ctx: number;
  // What this model would use with no per-model setting.
  global_num_ctx: number;
  // What a load without a request-level context length would use right now.
  effective_num_ctx: number;
  // Per-model llama-server slot count; 0 means the model follows the global setting.
  num_parallel: number;
  // What this model would use with no per-model slot count.
  global_num_parallel: number;
  // What a load without a request-level slot count would use right now.
  effective_num_parallel: number;
  // Per-model GGUF quantization; "" means the model follows the repository default.
  dtype: string;
  // Per-model idle window before the model is unloaded ("30s", "1h", "-1" for
  // never); "" means the model follows the runtime default.
  keep_alive: string;
  // What this model would use with no per-model keep-alive. It differs by
  // runtime: recognition and speech models idle out later than text models.
  global_keep_alive: string;
  // What a load without a request-level keep-alive would use right now.
  effective_keep_alive: string;
  // Which runtime serves this model. The llama.cpp load options mean nothing
  // for a model served by a Python runtime, and the pipeline tag cannot say
  // which one it is: an embedding model runs on llama.cpp when its weights
  // convert to GGUF and in the Python runtime when they do not.
  runtime?: "llama" | "python-asr" | "python-tts" | "diffusers";
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
  repo_size?: number;
  artifact_source?: ArtifactSource;
  revision?: string;
  provider?: MarketplaceModelProviderMetadata;
}

export type ArtifactSource = "opencsg" | "huggingface" | "modelscope";

export interface MarketplaceModelProviderMetadata {
  huggingface?: {
    author?: string;
    pipeline_tag?: string;
    library_name?: string;
    languages?: string[];
    base_models?: string[];
    original_tags?: string[];
    gated?: boolean;
    sha?: string;
  };
  modelscope?: {
    display_name?: string;
    tasks?: string[];
    libraries?: string[];
    model_type?: string;
    original_tags?: string[];
    gated?: boolean;
  };
}

export interface MarketplaceModelExtra {
  repo_id: number;
  size: number;
  last_commit_size?: number;
}

export interface MarketplaceModelQuantization {
  name: string;
  file_count: number;
  example_path: string;
}

export interface MarketplaceLocalModelStatus {
  downloaded: boolean;
  full_name?: string;
  public_id?: string;
}

export interface LocalInferenceSupport {
  supported: boolean;
  runtime?: "llama" | "diffusers" | "python-asr" | "python-tts";
  mode: "none" | "direct" | "convert" | "image" | "asr" | "tts" | "embedding";
  architecture?: string;
  runtime_architecture?: string;
}

export interface MarketplaceModelDetailResponse {
  details: MarketplaceModel;
  quantizations: MarketplaceModelQuantization[];
  local_inference: LocalInferenceSupport;
  local_model: MarketplaceLocalModelStatus;
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
  nickname?: string;
  repository_id?: number;
  private?: boolean;
  repository?: MarketplaceRepository;
  default_branch?: string;
  source?: string;
  sync_status?: string;
  hf_path?: string;
  repo_size?: number;
  file_count?: number;
  artifact_source?: ArtifactSource;
  revision?: string;
  provider?: {
    huggingface?: {
      author?: string;
      languages?: string[];
      task_categories?: string[];
      pretty_name?: string;
      original_tags?: string[];
      gated?: boolean;
      sha?: string;
    };
    modelscope?: {
      display_name?: string;
      languages?: string[];
      tasks?: string[];
      original_tags?: string[];
      gated?: boolean;
    };
  };
}

export interface MarketplaceDatasetDetailResponse {
  details: MarketplaceDataset;
  local_dataset: {
    downloaded: boolean;
    dataset?: string;
  };
}

export interface MarketplaceDatasetExtras {
  repo_size?: number;
  file_count?: number;
  revision?: string;
  available: boolean;
  timed_out?: boolean;
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
  cloud_provider_name: string;
  default_server_url: string;
  default_ai_gateway_url: string;
  default_cloud_provider_name: string;
  huggingface_endpoint: string;
  huggingface_token_configured: boolean;
  modelscope_endpoint: string;
  modelscope_token_configured: boolean;
  marketplace_model_source: ArtifactSource;
  marketplace_dataset_source: ArtifactSource;
  desktop_mode: boolean;
  local_api_url?: string;
  autostart: boolean;
  llama_use_model_max_ctx: boolean;
  llama_num_parallel: number;
  web_search: WebSearchSettings;
  observability: ObservabilitySettings;
  hidden_nav_items: string[];
  /** "Enterprise" while a license is in effect, otherwise "Community". */
  edition: string;
  license_status: LicenseStatus;
  license: LicenseSummary | null;
  /** Boolean feature keys currently in effect. */
  features: string[];
  /** Integer quota keys mapped to their value; 0 means unlimited. */
  limits: Record<string, number>;
  feature_catalog: LicenseFeatureDefinition[];
}

export type LicenseStatus = "none" | "not_started" | "valid" | "grace" | "expired" | "invalid";

export interface LicenseSummary {
  key: string;
  company: string;
  email: string;
  product: string;
  edition: string;
  max_user: number;
  start_time: string;
  expire_time: string;
  version?: string;
}

export interface LicenseState {
  status: LicenseStatus;
  edition: string;
  license: LicenseSummary | null;
  features: string[];
  limits: Record<string, number>;
  grace_until?: string;
  reason?: string;
  warnings?: string[];
  source?: string;
  file_path?: string;
  checked_at: string;
}

export interface LicenseVerifyResponse {
  valid: boolean;
  status: LicenseStatus;
  license: LicenseSummary | null;
  features: string[];
  limits: Record<string, number>;
  reason?: string;
  warnings?: string[];
}

export interface LicenseFeatureDefinition {
  key: string;
  type: "boolean" | "int";
  /** Enterprise-only; consults the license. Ungated features are always enabled. */
  gated: boolean;
  default_value: unknown;
  nav_item?: string;
  since?: string;
  enabled: boolean;
}

/** Error code carried by 403 responses from license-gated routes. */
export const FEATURE_NOT_LICENSED = "feature_not_licensed";

export function getLicense(): Promise<LicenseState> {
  return fetchJSON<LicenseState>("/api/license");
}

export function verifyLicense(data: string): Promise<LicenseVerifyResponse> {
  return fetchJSON<LicenseVerifyResponse>("/api/license/verify", {
    method: "POST",
    body: JSON.stringify({ data }),
  });
}

export function importLicense(data: string): Promise<LicenseState> {
  return fetchJSON<LicenseState>("/api/license", {
    method: "PUT",
    body: JSON.stringify({ data }),
  });
}

export function deleteLicense(): Promise<LicenseState> {
  return fetchJSON<LicenseState>("/api/license", { method: "DELETE" });
}

export function getLicenseFeatures(): Promise<LicenseFeatureDefinition[]> {
  return fetchJSON<LicenseFeatureDefinition[]>("/api/license/features");
}

export interface ObservabilitySettings {
  retention_days: number;
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

export type ASRRuntimeStatus = ImageRuntimeStatus;

export type TTSRuntimeStatus = ImageRuntimeStatus;

export interface SpeechVoice {
  id: string;
  label?: string;
  language?: string;
  gender?: string;
}

export interface SpeechVoicesResponse {
  model: string;
  sample_rate?: number;
  streaming: boolean;
  backend?: string;
  voices: SpeechVoice[];
}

export interface AudioSpeechRequest {
  model: string;
  input: string;
  voice?: string;
  response_format?: "mp3" | "wav" | "pcm" | "opus" | "flac" | "aac";
  speed?: number;
  instructions?: string;
  source?: string;
}

export interface AudioTranscriptionRequest {
  model: string;
  file: File;
  source?: string;
  language?: string;
  prompt?: string;
  response_format?: "json" | "verbose_json" | "text";
  temperature?: number;
  hotwords?: string[];
  itn?: boolean;
}

export interface AudioTranscriptionStreamChunk {
  text?: string;
  response?: AudioTranscriptionResponse;
  done?: boolean;
  error?: string;
}

export interface AudioTranscriptionSegment {
  id?: number;
  start?: number;
  end?: number;
  text: string;
}

export interface AudioTranscriptionResponse {
  text: string;
  task?: string;
  language?: string;
  duration?: number;
  segments?: AudioTranscriptionSegment[];
  backend?: string;
  metadata?: Record<string, unknown>;
}

export interface ImageGenerationRequest {
  model: string;
  source?: string;
  prompt: string;
  n?: number;
  size?: string;
  response_format?: "b64_json";
  seed?: number;
  negative_prompt?: string;
  steps?: number;
  cfg_scale?: number;
  image?: string;
  images?: string[];
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

export interface ImageGenerationJobListResponse {
  jobs: ImageGenerationJobResponse[];
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
  pool_requests: number;
  fallback_count: number;
  limited_count: number;
}

export interface LocalAPIUsageRow {
  api_key_id: string;
  api_key_name: string;
  model: string;
  source: string;
  source_type: string;
  source_name?: string;
  pool_id?: string;
  pool_name?: string;
  pool_model?: string;
  member_model?: string;
  fallback_count?: number;
  limited_count?: number;
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

export interface LocalAPIUsageKeyTotal {
  api_key_id: string;
  api_key_name: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  models: number;
  last_used_at: string;
}

export interface LocalAPIUsagePoolMemberTotal extends LocalAPIUsageSourceTotal {
  model: string;
  fallback_count: number;
  limited_count: number;
}

export interface LocalAPIUsagePoolTotal {
  pool_id: string;
  pool_name: string;
  pool_model: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  fallback_count: number;
  limited_count: number;
  members: LocalAPIUsagePoolMemberTotal[];
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
  key_totals: LocalAPIUsageKeyTotal[];
  pool_totals: LocalAPIUsagePoolTotal[];
  rows: LocalAPIUsageRow[];
}

export interface ObservabilityRequest {
  id: string;
  request_id?: string;
  trace_id: string;
  b3_trace_id?: string;
  thread_id?: string;
  started_at: string;
  completed_at: string;
  method: string;
  path: string;
  protocol: string;
  status: "completed" | "failed" | string;
  status_code: number;
  stream: boolean;
  model: string;
  source?: string;
  source_type?: string;
  source_name?: string;
  api_key_id?: string;
  api_key_name?: string;
  pool_id?: string;
  pool_name?: string;
  pool_model?: string;
  actual_member_id?: string;
  member_model?: string;
  pool_policy?: string;
  router_profile_id?: string;
  router_profile_version?: number;
  router_profile_schema_version?: number;
  router_algorithm?: string;
  routing_text_version?: string;
  router_confidence?: number;
  router_margin?: number;
  router_similarity?: number;
  semantic_routed?: boolean;
  semantic_cluster?: number;
  semantic_cluster_id?: string;
  semantic_distance?: number;
  semantic_ood?: boolean;
  semantic_fallback?: boolean;
  semantic_fallback_reason?: string;
  price_input_per_million: number;
  price_output_per_million: number;
  estimated_cost: number;
  cost_currency?: string;
  cost_known: boolean;
  fallback_count: number;
  limited_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  cache_read_input_tokens: number;
  cache_creation_input_tokens: number;
  cache_eligible_input_tokens: number;
  cache_hit_rate: number;
  duration_ms: number;
  first_token_latency_ms: number;
  error_message?: string;
  request_body?: string;
  response_body?: string;
  request_body_truncated: boolean;
  response_body_truncated: boolean;
}

export interface ObservabilityRequestSummary {
  requests: number;
  succeeded: number;
  failed: number;
  total_tokens: number;
  average_latency_ms: number;
}

export interface ObservabilityRequestListResponse {
  items: ObservabilityRequest[];
  total: number;
  limit: number;
  offset: number;
  summary: ObservabilityRequestSummary;
}

export interface ObservabilityTrace {
  trace_id: string;
  thread_id?: string;
  started_at: string;
  completed_at: string;
  status: "completed" | "failed" | string;
  request_count: number;
  models: string[];
  total_tokens: number;
  duration_ms: number;
}

export interface ObservabilityTraceListResponse {
  items: ObservabilityTrace[];
  total: number;
  limit: number;
  offset: number;
}

export interface ObservabilityTraceDetailResponse {
  trace: ObservabilityTrace;
  requests: ObservabilityRequest[];
}

export type DatasetExportFormat = "openai_messages" | "sharegpt" | "alpaca" | "prompt_completion";
export type DatasetRedactionPolicy = "redact" | "exclude" | "detect";

export interface DatasetExportTraceFilter {
  from?: string;
  to?: string;
  status?: string;
  model?: string;
  source?: string;
  q?: string;
}

export interface DatasetExportRequest {
  trace_ids?: string[];
  filter?: DatasetExportTraceFilter;
  format: DatasetExportFormat;
  redaction_policy?: DatasetRedactionPolicy;
  confirmed?: boolean;
  dataset_name?: string;
}

export interface DatasetExportRisk {
  type: string;
  count: number;
}

export interface DatasetExportFile {
  path: string;
  size: number;
  sha256: string;
}

export interface DatasetExportPreview {
  selected: number;
  exported: number;
  excluded: number;
  degraded: number;
  risks: DatasetExportRisk[];
  sample?: unknown;
}

export interface DatasetExport extends DatasetExportPreview {
  id: string;
  dataset_id: string;
  format: DatasetExportFormat;
  created_at: string;
  files: DatasetExportFile[];
  download_url: string;
}

export interface DatasetExportJob {
  id: string;
  status: "queued" | "running" | "completed" | "failed";
  created_at: string;
  updated_at: string;
  error?: string;
  export?: DatasetExport;
}

export interface DatasetPublishRequest {
  create: boolean;
  name: string;
  nickname?: string;
  description?: string;
  private: boolean;
  confirm_public?: boolean;
  license?: string;
}

export interface DatasetPublishResponse {
  dataset_id: string;
  revision: string;
  url: string;
  agentichub_url: string;
  files: DatasetExportFile[];
}

export interface ObservabilityQuery {
  from?: string;
  to?: string;
  status?: string;
  model?: string;
  source?: string;
  api_key_id?: string;
  q?: string;
  limit?: number;
  offset?: number;
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
  model_source?: string;
  provider_mode?: "native" | "opencsg";
  provider_group?: string;
  provider_switch_supported?: boolean;
  provider_drifted?: boolean;
  model_bindings?: AIAppModelBindings;
  model_slots?: AIAppModelSlot[];
  runtime_supported: boolean;
  runtime_running: boolean;
  runtime_status?: "running" | "stopped";
  log_path?: string;
  last_error?: string;
  disabled_reason?: string;
  updated_at: string;
}

export interface AIAppModelBinding {
  task: string;
  model_id: string;
  source: string;
}

export type AIAppModelBindings = AIAppModelBinding[];

export interface AIAppModelSlot {
  task: string;
  required: boolean;
  binding?: AIAppModelBinding;
}

export interface AIAppOpenResponse {
  url?: string;
  mode?: "url" | "desktop";
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
  type: "text" | "image_url" | "audio_url";
  text?: string;
  image_url?: { url: string };
  audio_url?: { url: string; mime?: string };
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
  total_bytes?: number;
  completed_bytes?: number;
}

export interface PullJob {
  id: string;
  status: string;
  kind: "model" | "dataset";
  name: string;
  artifact_source?: ArtifactSource;
  revision?: string;
  quant?: string;
  quants?: string[];
  created_at: string;
  updated_at: string;
  completed_at?: string;
  progress: PullProgress;
  error?: string;
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

function withLocaleHeader(init?: RequestInit): RequestInit {
  const headers = new Headers(init?.headers);
  if (!headers.has("Accept-Language")) {
    headers.set("Accept-Language", locale.value === "zh" ? "zh-CN" : "en-US");
  }
  return { ...init, headers };
}

async function fetchJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(url, withLocaleHeader(init));
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
    query.set("_", Date.now().toString());
  }
  const url = query.toString() ? `/api/tags?${query}` : "/api/tags";
  const data = await fetchJSON<{ models: ModelInfo[] }>(url, { cache: "no-store" });
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

function splitLocalDatasetID(dataset: string): { namespace: string; name: string; artifactSource: ArtifactSource } {
  const parts = dataset.trim().split("/").filter(Boolean);
  if (parts.length === 2) {
    return { namespace: parts[0], name: parts[1], artifactSource: "opencsg" };
  }
  if (
    parts.length === 3 &&
    (parts[0] === "opencsg" || parts[0] === "huggingface" || parts[0] === "modelscope")
  ) {
    return { namespace: parts[1], name: parts[2], artifactSource: parts[0] };
  }
  throw new Error(`Invalid dataset ID: ${dataset}`);
}

export async function getModelManifest(model: string): Promise<ModelManifestResponse> {
  const trimmed = model.trim();
  if (!trimmed.includes("/")) {
    return fetchJSON<ModelManifestResponse>(`/api/models/${encodeURIComponent(trimmed)}/manifest`);
  }
  const { namespace, name } = splitModelID(trimmed);
  return fetchJSON<ModelManifestResponse>(`/api/models/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/manifest`);
}

function modelConfigPath(model: string): string {
  const trimmed = model.trim();
  if (!trimmed.includes("/")) {
    return `/api/models/${encodeURIComponent(trimmed)}/config`;
  }
  const { namespace, name } = splitModelID(trimmed);
  return `/api/models/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/config`;
}

export async function getModelConfig(model: string): Promise<ModelConfigResponse> {
  return fetchJSON<ModelConfigResponse>(modelConfigPath(model));
}

export interface ModelConfigUpdate {
  num_ctx?: number;
  num_parallel?: number;
  dtype?: string;
  keep_alive?: string;
}

// Every setting is optional: omitting one leaves the saved value untouched, so
// a caller that shows only some of the fields cannot clear the rest. A 0 or an
// empty string clears that setting and returns the model to the global default.
export async function setModelConfig(
  model: string,
  update: ModelConfigUpdate
): Promise<ModelConfigResponse> {
  return fetchJSON<ModelConfigResponse>(modelConfigPath(model), {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(update),
  });
}

export function uploadLocalModel(
  options: LocalModelUploadOptions,
  onProgress?: (percent: number) => void
): Promise<ModelUploadResponse> {
  return uploadLocalModelSession(options, onProgress);
}

const MODEL_UPLOAD_CHUNK_SIZE = 32 * 1024 * 1024;
const MODEL_UPLOAD_CHUNK_RETRIES = 3;

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

async function uploadLocalModelSessionFile(
  uploadID: string,
  item: LocalModelUploadFile,
  onProgress?: (loaded: number) => void
): Promise<void> {
  if (item.file.size === 0) {
    await uploadLocalModelChunk(uploadID, item, item.file, 0, 0);
    onProgress?.(0);
    return;
  }

  let offset = 0;
  while (offset < item.file.size) {
    const end = Math.min(offset + MODEL_UPLOAD_CHUNK_SIZE, item.file.size);
    const chunk = item.file.slice(offset, end);
    let response: ModelUploadChunkResponse | null = null;
    for (let attempt = 0; attempt <= MODEL_UPLOAD_CHUNK_RETRIES; attempt += 1) {
      try {
        response = await uploadLocalModelChunk(uploadID, item, chunk, offset, item.file.size, (loaded) => {
          onProgress?.(Math.min(item.file.size, offset + loaded));
        });
        break;
      } catch (err) {
        if (!(err instanceof ModelUploadTransportError) || attempt === MODEL_UPLOAD_CHUNK_RETRIES) {
          throw err;
        }
        await delayModelUploadRetry(250 * 2 ** attempt);
      }
    }
    if (!response || response.next_offset !== end) {
      throw new Error(`upload offset mismatch: expected ${end}, received ${response?.next_offset ?? "none"}`);
    }
    offset = response.next_offset;
    onProgress?.(offset);
  }
}

type ModelUploadChunkResponse = {
  status: string;
  bytes: number;
  next_offset: number;
  complete: boolean;
};

class ModelUploadTransportError extends Error {}

function delayModelUploadRetry(delayMs: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, delayMs));
}

function uploadLocalModelChunk(
  uploadID: string,
  item: LocalModelUploadFile,
  chunk: Blob,
  offset: number,
  total: number,
  onProgress?: (loaded: number) => void
): Promise<ModelUploadChunkResponse> {
  return new Promise((resolve, reject) => {
    const path = item.path || item.file.name;
    const params = new URLSearchParams();
    params.set("path", path);
    params.set("filename", item.file.name);
    const xhr = new XMLHttpRequest();
    xhr.open("PUT", `/api/models/upload/${encodeURIComponent(uploadID)}/file?${params.toString()}`);
    if (total > 0) {
      xhr.setRequestHeader("Content-Range", `bytes ${offset}-${offset + chunk.size - 1}/${total}`);
    }
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        onProgress?.(event.loaded);
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve(JSON.parse(xhr.responseText) as ModelUploadChunkResponse);
        } catch {
          reject(new Error("upload returned an invalid response"));
        }
        return;
      }
      let data: any = null;
      try {
        data = xhr.responseText ? JSON.parse(xhr.responseText) : null;
      } catch {
        /* ignore parse errors */
      }
      const committedOffset = offset + chunk.size;
      if (xhr.status === 409 && data?.expected_offset === committedOffset) {
        resolve({
          status: "ok",
          bytes: chunk.size,
          next_offset: committedOffset,
          complete: committedOffset === total,
        });
        return;
      }
      if (xhr.status >= 500) {
        reject(new ModelUploadTransportError(data?.error || data?.message || `upload failed (${xhr.status})`));
        return;
      }
      reject(new Error(data?.error || data?.message || "upload failed"));
    };
    xhr.onerror = () => reject(new ModelUploadTransportError("upload connection failed"));
    xhr.onabort = () => reject(new Error("upload aborted"));
    xhr.ontimeout = () => reject(new ModelUploadTransportError("upload timed out"));
    xhr.send(chunk);
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

export async function openExternalURL(url: string): Promise<boolean> {
  const resp = await fetch("/api/system/open-external", withLocaleHeader({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
  }));
  if (resp.status === 409) return false;
  if (!resp.ok) {
    const contentType = resp.headers.get("content-type") || "";
    const body = await resp.text();
    throw new Error(extractErrorMessage(body, contentType, resp.statusText || "failed to open external link"));
  }
  return true;
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

export async function getASRRuntimeStatus(): Promise<ASRRuntimeStatus> {
  return fetchJSON<ASRRuntimeStatus>("/api/asr-runtime");
}

export async function installASRRuntime(options?: { upgrade_packages?: boolean }): Promise<ASRRuntimeStatus> {
  return fetchJSON<ASRRuntimeStatus>("/api/asr-runtime/install", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ upgrade_packages: options?.upgrade_packages || undefined }),
  });
}

export async function getTTSRuntimeStatus(): Promise<TTSRuntimeStatus> {
  return fetchJSON<TTSRuntimeStatus>("/api/tts-runtime");
}

export async function installTTSRuntime(options?: { upgrade_packages?: boolean }): Promise<TTSRuntimeStatus> {
  return fetchJSON<TTSRuntimeStatus>("/api/tts-runtime/install", {
    method: "POST",
    body: JSON.stringify({ upgrade_packages: options?.upgrade_packages === true }),
  });
}

// The model id travels as a query parameter because a source-scoped id has
// three slash-separated segments and cannot be a path parameter.
export async function getTTSVoices(model: string): Promise<SpeechVoicesResponse> {
  return fetchJSON<SpeechVoicesResponse>(`/api/tts-voices?model=${encodeURIComponent(model)}`);
}

// Returns an object URL for the synthesised audio; callers must revoke it.
export async function synthesizeSpeech(req: AudioSpeechRequest, signal?: AbortSignal): Promise<{ url: string; mime: string }> {
  const resp = await fetch("/v1/audio/speech", withLocaleHeader({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
    signal,
  }));
  const contentType = resp.headers.get("content-type") || "";
  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(extractErrorMessage(body, contentType, resp.statusText || "speech synthesis failed"));
  }
  const blob = await resp.blob();
  return { url: URL.createObjectURL(blob), mime: contentType.split(";")[0] || "audio/mpeg" };
}

export interface RealtimeSessionOptions {
  // Recognition model; omit for a session that only speaks.
  asrModel?: string;
  // Synthesis model; omit for a session that only listens.
  ttsModel?: string;
  voice?: string;
  // Conversation model. With one set the server writes and speaks the reply to
  // each finished turn itself; without it the session only transcribes and
  // speaks the text response.create supplies.
  model?: string;
}

export interface RealtimeCall {
  answer: string;
  callId: string;
}

// Opens a realtime WebRTC call: posts the SDP offer and returns the answer plus
// the call id to hang up with. The session object travels in the same request,
// which is the multipart form the OpenAI clients send.
export async function startRealtimeCall(offer: string, options: RealtimeSessionOptions): Promise<RealtimeCall> {
  const session: Record<string, unknown> = { type: "realtime" };
  if (options.model) session.model = options.model;
  const audio: Record<string, unknown> = {};
  if (options.asrModel) audio.input = { transcription: { model: options.asrModel } };
  if (options.ttsModel) {
    audio.output = options.voice
      ? { model: options.ttsModel, voice: options.voice }
      : { model: options.ttsModel };
  }
  if (Object.keys(audio).length > 0) session.audio = audio;

  const form = new FormData();
  form.append("sdp", offer);
  form.append("session", JSON.stringify(session));
  const resp = await fetch("/v1/realtime/calls", withLocaleHeader({
    method: "POST",
    headers: { Accept: "application/sdp" },
    body: form,
  }));
  const contentType = resp.headers.get("content-type") || "";
  const body = await resp.text();
  if (!resp.ok) {
    throw new Error(extractErrorMessage(body, contentType, resp.statusText || "realtime call failed"));
  }
  return { answer: body, callId: (resp.headers.get("Location") || "").split("/").pop() || "" };
}

export async function endRealtimeCall(callId: string): Promise<void> {
  if (!callId) return;
  await fetch(`/v1/realtime/calls/${encodeURIComponent(callId)}`, withLocaleHeader({ method: "DELETE" }));
}

export async function transcribeAudio(req: AudioTranscriptionRequest, signal?: AbortSignal): Promise<AudioTranscriptionResponse> {
  const form = new FormData();
  form.set("model", req.model);
  form.set("file", req.file, req.file.name || "audio");
  form.set("response_format", req.response_format || "json");
  if (req.source) form.set("source", req.source);
  if (req.language) form.set("language", req.language);
  if (req.prompt) form.set("prompt", req.prompt);
  if (typeof req.temperature === "number") form.set("temperature", String(req.temperature));
  if (req.hotwords && req.hotwords.length > 0) form.set("hotwords", JSON.stringify(req.hotwords));
  if (typeof req.itn === "boolean") form.set("itn", String(req.itn));

  const resp = await fetch("/v1/audio/transcriptions", withLocaleHeader({
    method: "POST",
    body: form,
    signal,
  }));
  const contentType = resp.headers.get("content-type") || "";
  const body = await resp.text();
  if (!resp.ok) {
    throw new Error(extractErrorMessage(body, contentType, resp.statusText || "transcription failed"));
  }
  if (req.response_format === "text" || contentType.startsWith("text/plain")) {
    return { text: body };
  }
  if (!contentType.includes("application/json")) {
    throw unexpectedJSONError("/v1/audio/transcriptions", contentType, body);
  }
  try {
    return JSON.parse(body) as AudioTranscriptionResponse;
  } catch {
    throw unexpectedJSONError("/v1/audio/transcriptions", contentType, body);
  }
}

export function transcribeAudioStream(
  req: AudioTranscriptionRequest,
  onChunk: (chunk: AudioTranscriptionStreamChunk) => void,
  signal?: AbortSignal
): Promise<AudioTranscriptionResponse> {
  const form = new FormData();
  form.set("model", req.model);
  form.set("file", req.file, req.file.name || "audio");
  form.set("response_format", req.response_format || "json");
  form.set("stream", "true");
  if (req.source) form.set("source", req.source);
  if (req.language) form.set("language", req.language);
  if (req.prompt) form.set("prompt", req.prompt);
  if (typeof req.temperature === "number") form.set("temperature", String(req.temperature));
  if (req.hotwords && req.hotwords.length > 0) form.set("hotwords", JSON.stringify(req.hotwords));
  if (typeof req.itn === "boolean") form.set("itn", String(req.itn));

  return new Promise((resolve, reject) => {
    fetch("/v1/audio/transcriptions", withLocaleHeader({
      method: "POST",
      headers: { Accept: "text/event-stream" },
      body: form,
      signal,
    }))
      .then(async (resp) => {
        const contentType = resp.headers.get("content-type") || "";
        if (!resp.ok || !resp.body) {
          const body = await resp.text();
          reject(new Error(extractErrorMessage(body, contentType, resp.statusText || "transcription failed")));
          return;
        }
        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        let finalText = "";

        function processLine(line: string) {
          if (!line.startsWith("data: ")) return;
          const chunk = JSON.parse(line.slice(6)) as AudioTranscriptionStreamChunk;
          if (chunk.error) {
            throw new Error(chunk.error);
          }
          if (chunk.done) {
            if (typeof chunk.text === "string") {
              if (!finalText && chunk.text) {
                onChunk({ ...chunk, done: false });
              }
              finalText = chunk.text;
            }
            return;
          }
          if (typeof chunk.text === "string") {
            finalText += chunk.text;
          }
          onChunk(chunk);
        }

        function read(): Promise<void> {
          return reader.read().then(({ done, value }) => {
            if (done) {
              resolve({ text: finalText });
              return;
            }
            buf += decoder.decode(value, { stream: true });
            const lines = buf.split("\n");
            buf = lines.pop() || "";
            for (const line of lines) {
              if (!line.trim()) continue;
              processLine(line);
            }
            return read();
          });
        }

        return read();
      })
      .catch(reject);
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

export async function listImageGenerationJobs(): Promise<ImageGenerationJobListResponse> {
  return fetchJSON<ImageGenerationJobListResponse>("/api/images/jobs");
}

export async function getImageGenerationJobResult(id: string): Promise<ImageGenerationResponse> {
  return fetchJSON<ImageGenerationResponse>(`/api/images/jobs/${encodeURIComponent(id)}/result`);
}

export async function cancelImageGenerationJob(id: string): Promise<ImageGenerationJobResponse> {
  return fetchJSON<ImageGenerationJobResponse>(`/api/images/jobs/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

export async function deleteImageGenerationJob(id: string): Promise<ImageGenerationJobResponse> {
  return cancelImageGenerationJob(id);
}

export async function saveSettings(patch: {
  storage_dir?: string;
  model_dir?: string;
  dataset_dir?: string;
  server_url?: string;
  ai_gateway_url?: string;
  cloud_provider_name?: string;
  huggingface_endpoint?: string;
  huggingface_token?: string;
  modelscope_endpoint?: string;
  modelscope_token?: string;
  marketplace_model_source?: ArtifactSource;
  marketplace_dataset_source?: ArtifactSource;
  autostart?: boolean;
  llama_use_model_max_ctx?: boolean;
  llama_num_parallel?: number;
  web_search?: WebSearchSettings;
  observability?: ObservabilitySettings;
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

export async function getLocalAPIUsage(period?: string, provider?: string, pool?: string): Promise<LocalAPIUsageResponse> {
  const params = new URLSearchParams();
  if (period) params.set("period", period);
  if (provider) params.set("provider", provider);
	if (pool) params.set("pool", pool);
  const query = params.toString() ? `?${params}` : "";
  return fetchJSON<LocalAPIUsageResponse>(`/api/api-usage${query}`);
}

function observabilityQueryString(query?: ObservabilityQuery): string {
  const params = new URLSearchParams();
  if (query?.from) params.set("from", query.from);
  if (query?.to) params.set("to", query.to);
  if (query?.status) params.set("status", query.status);
  if (query?.model) params.set("model", query.model);
  if (query?.source) params.set("source", query.source);
  if (query?.api_key_id) params.set("api_key_id", query.api_key_id);
  if (query?.q) params.set("q", query.q);
  if (typeof query?.limit === "number") params.set("limit", String(query.limit));
  if (typeof query?.offset === "number") params.set("offset", String(query.offset));
  const value = params.toString();
  return value ? `?${value}` : "";
}

export async function getObservabilityRequests(query?: ObservabilityQuery): Promise<ObservabilityRequestListResponse> {
  return fetchJSON<ObservabilityRequestListResponse>(`/api/observability/requests${observabilityQueryString(query)}`);
}

export async function getObservabilityRequest(id: string): Promise<ObservabilityRequest> {
  return fetchJSON<ObservabilityRequest>(`/api/observability/requests/${encodeURIComponent(id)}`);
}

export interface ObservabilityFacetValue {
  value: string;
  label?: string;
  count: number;
}

export interface ObservabilityFacets {
  models: ObservabilityFacetValue[];
  routes: ObservabilityFacetValue[];
}

export async function getObservabilityFacets(): Promise<ObservabilityFacets> {
  return fetchJSON<ObservabilityFacets>("/api/observability/facets");
}

export async function getObservabilityTraces(query?: ObservabilityQuery): Promise<ObservabilityTraceListResponse> {
  return fetchJSON<ObservabilityTraceListResponse>(`/api/observability/traces${observabilityQueryString(query)}`);
}

export async function getObservabilityTrace(traceID: string): Promise<ObservabilityTraceDetailResponse> {
  return fetchJSON<ObservabilityTraceDetailResponse>(`/api/observability/traces/${encodeURIComponent(traceID)}`);
}

export async function previewTraceDatasetExport(request: DatasetExportRequest): Promise<DatasetExportPreview> {
  return fetchJSON<DatasetExportPreview>("/api/observability/dataset-exports/preview", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
}

export async function createTraceDatasetExport(request: DatasetExportRequest): Promise<DatasetExportJob> {
  return fetchJSON<DatasetExportJob>("/api/observability/dataset-exports", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
}

export async function getTraceDatasetExportJob(jobID: string): Promise<DatasetExportJob> {
  return fetchJSON<DatasetExportJob>(`/api/observability/dataset-export-jobs/${encodeURIComponent(jobID)}`);
}

export async function publishLocalDataset(dataset: string, request: DatasetPublishRequest): Promise<DatasetPublishResponse> {
  const { namespace, name, artifactSource } = splitLocalDatasetID(dataset);
  const query = artifactSource === "opencsg" ? "" : `?artifact_source=${encodeURIComponent(artifactSource)}`;
  return fetchJSON<DatasetPublishResponse>(
    `/api/datasets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/publish${query}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(request),
    },
  );
}

export function localDatasetExportURL(dataset: string): string {
  const { namespace, name, artifactSource } = splitLocalDatasetID(dataset);
  const query = artifactSource === "opencsg" ? "" : `?artifact_source=${encodeURIComponent(artifactSource)}`;
  return `/api/datasets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/export${query}`;
}

export async function clearObservabilityData(): Promise<void> {
  await fetchJSON<{ status: string }>("/api/observability", { method: "DELETE" });
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

export async function createPullJob(model: string, options?: string | { quant?: string; quants?: string[]; artifactSource?: ArtifactSource; revision?: string }): Promise<PullJob> {
  const normalizedOptions = typeof options === "string" ? { quant: options } : options;
  const quants = normalizedOptions?.quants?.map((value) => value.trim()).filter(Boolean);
  return fetchJSON<PullJob>("/api/pull/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model,
      quant: normalizedOptions?.quant || undefined,
      quants: quants && quants.length > 0 ? quants : undefined,
      artifact_source: normalizedOptions?.artifactSource || undefined,
      revision: normalizedOptions?.revision?.trim() || undefined,
    }),
  });
}

export async function createDatasetPullJob(
  dataset: string,
  options?: { artifactSource?: ArtifactSource; revision?: string },
): Promise<PullJob> {
  return fetchJSON<PullJob>("/api/datasets/pull/jobs", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      dataset,
      artifact_source: options?.artifactSource,
      revision: options?.revision?.trim() || undefined,
    }),
  });
}

export async function getPullJob(jobId: string): Promise<PullJob> {
  return fetchJSON<PullJob>(`/api/pull/jobs/${encodeURIComponent(jobId)}`);
}

export async function cancelPullJob(jobId: string): Promise<PullJob> {
  return fetchJSON<PullJob>(`/api/pull/jobs/${encodeURIComponent(jobId)}`, {
    method: "DELETE",
  });
}

export async function clearPartialModelPull(
  model: string,
  options?: { artifactSource?: ArtifactSource; revision?: string },
): Promise<{ status: string; path: string }> {
  return fetchJSON<{ status: string; path: string }>("/api/pull/partial", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model,
      artifact_source: options?.artifactSource,
      revision: options?.revision,
    }),
  });
}

export async function clearPartialDatasetPull(
  dataset: string,
  options?: { artifactSource?: ArtifactSource; revision?: string },
): Promise<{ status: string; path: string }> {
  return fetchJSON<{ status: string; path: string }>("/api/datasets/pull/partial", {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      dataset,
      artifact_source: options?.artifactSource,
      revision: options?.revision,
    }),
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

export async function searchConversations(query: string): Promise<ConversationMeta[]> {
  const params = new URLSearchParams();
  const trimmed = query.trim();
  if (trimmed) {
    params.set("q", trimmed);
  }
  const suffix = params.toString() ? `?${params.toString()}` : "";
  const data = await fetchJSON<{ conversations: ConversationMeta[] }>(`/api/conversations/search${suffix}`);
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
  options: { temperature?: number; top_p?: number; max_tokens?: number; num_ctx?: number; system?: string; source?: string; thread_id?: string; trace_id?: string; web_search?: { enabled: boolean; query?: string } },
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
        ...(options.thread_id ? { "X-CSGLite-Thread-ID": options.thread_id } : {}),
        ...(options.trace_id ? { "X-CSGLite-Trace-ID": options.trace_id } : {}),
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
  speculative?: {
    types?: string[];
    draft_model?: string;
    draft_n_max?: number;
    draft_n_min?: number;
    draft_p_min?: number;
  };
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
        speculative: options?.speculative,
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
  origin?: string;
  description?: string;
  license?: string;
  artifact_source?: ArtifactSource;
  repository?: string;
  requested_revision?: string;
  resolved_revision?: string;
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
  const { namespace, name, artifactSource } = splitLocalDatasetID(dataset);
  const query = artifactSource === "opencsg" ? "" : `?artifact_source=${encodeURIComponent(artifactSource)}`;
  return fetchJSON<DatasetManifestResponse>(
    `/api/datasets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/manifest${query}`,
  );
}

export function pullDataset(
  dataset: string,
  onProgress: (p: PullProgress) => void,
  signal?: AbortSignal,
  options?: { artifactSource?: ArtifactSource; revision?: string },
): Promise<void> {
  return new Promise((resolve, reject) => {
    fetch("/api/datasets/pull", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        dataset,
        artifact_source: options?.artifactSource,
        revision: options?.revision?.trim() || undefined,
      }),
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
  task?: string;
  modelParamsMin?: string;
  modelParamsMax?: string;
  page?: number;
  per?: number;
  artifactSource?: ArtifactSource;
}): Promise<{ data: MarketplaceModel[]; total: number; has_more?: boolean; total_exact?: boolean }> {
  const q = new URLSearchParams();
  if (params.search) q.set("search", params.search);
  q.set("sort", params.sort || "trending");
  if (params.framework) q.set("framework", params.framework);
  if (params.task) q.set("task", params.task);
  if (params.modelParamsMin) q.set("model_params_min", params.modelParamsMin);
  if (params.modelParamsMax) q.set("model_params_max", params.modelParamsMax);
  if (params.artifactSource) q.set("artifact_source", params.artifactSource);
  q.set("page", String(params.page || 1));
  q.set("per", String(params.per || 16));
  const resp = await fetchJSON<{ data: MarketplaceModel[]; total: number; has_more?: boolean; total_exact?: boolean }>(
    `/api/marketplace/models?${q}`
  );
  return resp;
}

export async function getMarketplaceModelExtras(repoIDs: number[]): Promise<MarketplaceModelExtra[]> {
  const resp = await fetchJSON<{ data: MarketplaceModelExtra[] }>("/api/marketplace/models/extra", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ repo_ids: repoIDs }),
  });
  return resp.data || [];
}

export async function getMarketplaceModelDetail(model: string, options?: { artifactSource?: ArtifactSource; revision?: string }): Promise<MarketplaceModelDetailResponse> {
  const { namespace, name } = splitModelID(model);
  const q = new URLSearchParams();
  if (options?.artifactSource) q.set("artifact_source", options.artifactSource);
  if (options?.revision?.trim()) q.set("revision", options.revision.trim());
  const suffix = q.size > 0 ? `?${q}` : "";
  return fetchJSON<MarketplaceModelDetailResponse>(
    `/api/marketplace/models/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}${suffix}`
  );
}

export async function getMarketplaceDatasets(params: {
  search?: string;
  sort?: string;
  task?: string;
  language?: string;
  license?: string;
  page?: number;
  per?: number;
  artifactSource?: ArtifactSource;
}): Promise<{ data: MarketplaceDataset[]; total: number; has_more?: boolean; total_exact?: boolean }> {
  const q = new URLSearchParams();
  if (params.search) q.set("search", params.search);
  q.set("sort", params.sort || "trending");
  if (params.task) q.set("task", params.task);
  if (params.language) q.set("language", params.language);
  if (params.license) q.set("license", params.license);
  if (params.artifactSource) q.set("artifact_source", params.artifactSource);
  q.set("page", String(params.page || 1));
  q.set("per", String(params.per || 16));
  const resp = await fetchJSON<{ data: MarketplaceDataset[]; total: number; has_more?: boolean; total_exact?: boolean }>(
    `/api/marketplace/datasets?${q}`
  );
  return resp;
}

export async function getMarketplaceDatasetDetail(
  dataset: string,
  options?: { artifactSource?: ArtifactSource; revision?: string },
): Promise<MarketplaceDatasetDetailResponse> {
  const { namespace, name } = splitModelID(dataset);
  const q = new URLSearchParams();
  if (options?.artifactSource) q.set("artifact_source", options.artifactSource);
  if (options?.revision?.trim()) q.set("revision", options.revision.trim());
  const suffix = q.size > 0 ? `?${q}` : "";
  return fetchJSON<MarketplaceDatasetDetailResponse>(
    `/api/marketplace/datasets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}${suffix}`,
  );
}

export async function getMarketplaceDatasetExtras(
  dataset: string,
  options?: { artifactSource?: ArtifactSource; revision?: string; signal?: AbortSignal },
): Promise<MarketplaceDatasetExtras> {
  const { namespace, name } = splitModelID(dataset);
  const q = new URLSearchParams();
  if (options?.artifactSource) q.set("artifact_source", options.artifactSource);
  if (options?.revision?.trim()) q.set("revision", options.revision.trim());
  const suffix = q.size > 0 ? `?${q}` : "";
  return fetchJSON<MarketplaceDatasetExtras>(
    `/api/marketplace/datasets/${encodeURIComponent(namespace)}/${encodeURIComponent(name)}/extras${suffix}`,
    { signal: options?.signal },
  );
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

export async function switchAIAppProvider(
  appId: string,
  providerMode: "native" | "opencsg",
  modelId?: string,
  source?: string
): Promise<AIAppInfo> {
  return fetchJSON<AIAppInfo>("/api/apps/provider", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      app_id: appId,
      provider_mode: providerMode,
      model_id: modelId || undefined,
      source: source || undefined,
    }),
  });
}

export async function setAIAppPath(appId: string, path: string): Promise<AIAppInfo> {
  return fetchJSON<AIAppInfo>("/api/apps/path", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ app_id: appId, path }),
  });
}

export async function saveAIAppModel(
  appId: string,
  modelId?: string,
  source?: string,
  modelBindings?: AIAppModelBindings
): Promise<void> {
  const resp = await fetch("/api/apps/model", withLocaleHeader({
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      app_id: appId,
      model_id: modelId || undefined,
      source: source || undefined,
      model_bindings: modelBindings,
    }),
  }));
  if (!resp.ok) {
    const contentType = resp.headers.get("content-type") || "";
    const body = await resp.text();
    throw new Error(extractErrorMessage(body, contentType, resp.statusText || "failed to save app model"));
  }
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
  headers?: ProviderHeader[];
}

export interface ProviderHeader {
  name: string;
  value: string;
}

export interface ThirdPartyProvidersResponse {
  providers: ThirdPartyProvider[];
}

export interface ThirdPartyProviderCreateRequest {
  name: string;
  base_url: string;
  api_key?: string;
  provider?: string;
  enabled: boolean;
  headers?: ProviderHeader[];
}

export interface ThirdPartyProviderUpdateRequest {
  name?: string;
  base_url?: string;
  api_key?: string;
  provider?: string;
  enabled?: boolean;
  headers?: ProviderHeader[];
}

export interface ThirdPartyProviderValidateRequest {
  id?: string;
  name?: string;
  base_url: string;
  api_key?: string;
  provider?: string;
  enabled: boolean;
  headers?: ProviderHeader[];
  probe?: boolean;
}

export interface ThirdPartyProviderValidateResponse {
  valid: boolean;
  model_count: number;
}

export interface ProviderPoolMember {
  id: string;
  source: string;
  model: string;
  priority?: number;
  weight?: number;
  requests_per_minute?: number;
  tokens_per_minute?: number;
  max_concurrent?: number;
}

export type ProviderPoolPolicyType = "priority_weight" | "semantic";

export interface ProviderPoolPolicy {
  type: ProviderPoolPolicyType | string;
  label?: string;
  experimental: boolean;
  available: boolean;
  reason?: string;
}

export interface ProviderPool {
  id: string;
  name: string;
  model: string;
  enabled: boolean;
  policy?: ProviderPoolPolicyType | string;
  policy_available?: boolean;
  policy_unavailable_reason?: string;
  members: ProviderPoolMember[];
}

export interface ProviderPoolCreateRequest {
  name: string;
  model: string;
  enabled?: boolean;
  policy?: ProviderPoolPolicyType | string;
  members: ProviderPoolMember[];
}

export interface ProviderPoolUpdateRequest {
  name?: string;
  model?: string;
  enabled?: boolean;
  policy?: ProviderPoolPolicyType | string;
  members?: ProviderPoolMember[];
}

export interface ProviderPoolRouterSuggestion {
  id: string;
  reason: string;
  qualified_query_count: number;
  new_query_count: number;
  member_compatible: boolean;
  status: string;
  created_at: string;
  updated_at: string;
}

export interface ProviderPoolRouterEvaluationRequest {
  evaluation_mode?: "absolute_v1" | "listwise_v2";
  base_profile_id?: string;
  judge_model: string;
  max_queries: number;
  repeats: number;
  max_output_tokens: number;
  request_timeout_seconds: number;
  budget_currency: string;
  budget_amount: number;
  allow_unknown_pricing: boolean;
}

export interface ProviderPoolRouterEvaluationPreview {
  evaluation_mode: "absolute_v1" | "listwise_v2";
  eligible_snapshot_count: number;
  selected_snapshot_count: number;
  direct_candidate_calls: number;
  judge_calls: number;
  max_judge_calls: number;
  max_total_calls: number;
  judge_prompt_tokens: number;
  max_judge_token_exposure: number;
  max_token_exposure: number;
  known_judge_estimated_cost: number;
  known_estimated_cost: number;
  currency: string;
  unknown_price_members: Array<{ source: string; model: string }>;
  judge_price_known: boolean;
  requires_unknown_pricing_consent: boolean;
  limits: {
    max_queries: number;
    max_repeats: number;
    max_output_tokens: number;
    max_request_timeout_seconds: number;
  };
}

export type ProviderPoolRouterJobStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";

export interface ProviderPoolRouterEvaluationJob {
  id: string;
  evaluation_mode: "absolute_v1" | "listwise_v2";
  base_profile_id?: string;
  member_compatible: boolean;
  judge_model: string;
  max_queries: number;
  repeats: number;
  max_output_tokens: number;
  request_timeout_seconds: number;
  budget_currency: string;
  budget_amount: number;
  allow_unknown_pricing: boolean;
  direct_candidate_calls?: number;
  judge_calls?: number;
  max_judge_calls?: number;
  judge_prompt_tokens?: number;
  max_judge_token_exposure?: number;
  max_token_exposure?: number;
  known_judge_estimated_cost?: number;
  known_estimated_cost?: number;
  estimate_currency?: string;
  unknown_pricing?: boolean;
  current: number;
  total: number;
  phase?: string;
  cancellation_requested: boolean;
  status: ProviderPoolRouterJobStatus;
  error?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  updated_at: string;
}

export interface ProviderPoolRouterBaselineResult {
  quality: number;
  cost: number;
}

export interface ProviderPoolRouterBaselines {
  best_single_model: ProviderPoolRouterBaselineResult;
  cheapest_model: ProviderPoolRouterBaselineResult;
  random_model: ProviderPoolRouterBaselineResult;
  oracle_model: ProviderPoolRouterBaselineResult;
}

export interface ProviderPoolRouterMetrics {
  query_count: number;
  cell_count: number;
  trial_count: number;
  repeats: number;
  response_outcomes: Record<string, number>;
  win_rate: number;
  spend: number;
  total_cost: number;
  currency?: string;
  cost_unit: string;
  monetary_spend_known: boolean;
  unknown_monetary_spend: boolean;
  train_query_count: number;
  held_out_query_count: number;
  cv_fold_count: number;
  train_utility: number;
  train_quality: number;
  train_cost_score: number;
  held_out_utility: number;
  held_out_quality: number;
  held_out_cost_score: number;
  all_clusters_one_member: boolean;
  semantic_differentiation: boolean;
  baselines: ProviderPoolRouterBaselines;
}

export interface ProviderPoolRouterProfile {
  id: string;
  version: number;
  schema_version: 1 | 2;
  router_algorithm?: string;
  member_compatible: boolean;
  member_fingerprint_drift: boolean;
  active: boolean;
  created_at: string;
  created_by?: string;
  source_job_id?: string;
  description?: string;
  generated_at: string;
  distance: string;
  cost_unit: string;
  fallback_member_id: string;
  metrics: ProviderPoolRouterMetrics;
  clusters?: Array<{
    id: string;
    target_member_id: string;
    target: { source: string; model: string };
    sample_count: number;
    distance_quantiles: { p50: number; p90: number; p95: number; p99: number };
    ood_threshold: number;
  }>;
  candidate_distribution?: Array<{
    member_id: string;
    target: { source: string; model: string };
    cluster_count: number;
    sample_count: number;
    fraction?: number;
  }>;
  activation_allowed: boolean;
  activation_blocked_reason?: string;
  validation_state?: string;
  feasible: boolean;
  collapsed_single_member: boolean;
  collapsed_quality_passed: boolean;
  v2?: {
    profile_algorithm: string;
    model_type: string;
    model_fallback_reason?: string;
    sample_count: number;
    query_group_count: number;
    round_count: number;
    cv_fold_count: number;
    target_quality_retention: number;
    confidence_level: number;
    baseline_best_single_model: ProviderPoolRouterQualityCost;
    routed: ProviderPoolRouterQualityCost;
    point_retention: number;
    conservative_retention: number;
    retention_lower_bound: number;
    savings?: number;
    savings_fraction?: number;
    savings_known: boolean;
    coverage: number;
    fallback_rate: number;
    low_confidence_rate: number;
    ood_rate: number;
    pairwise_metrics: ProviderPoolRouterPairwiseMetrics;
    thresholds: {
      minimum_confidence: number;
      minimum_margin: number;
      minimum_similarity: number;
      quality_slack: number;
    };
    optimize_known_cost: boolean;
    quality_feasible: boolean;
    known_cost_feasible: boolean;
    insufficient_evidence: boolean;
    collapsed_member_id?: string;
    warnings?: string[];
  };
}

export interface ProviderPoolRouterQualityCost {
  quality: number;
  cost?: number;
  cost_known: boolean;
  currency?: string;
}

export interface ProviderPoolRouterPairwiseMetrics {
  count: number;
  log_loss: number;
  brier: number;
  top_class_accuracy: number;
  ece: number;
}

export interface ProviderPoolRouterStatus {
  qualified_query_count: number;
  new_query_count: number;
  pending_suggestion?: ProviderPoolRouterSuggestion;
  running_job?: ProviderPoolRouterEvaluationJob;
  latest_job?: ProviderPoolRouterEvaluationJob;
  active_profile?: ProviderPoolRouterProfile;
  latest_candidate_profile?: ProviderPoolRouterProfile;
  rollback_target_profile?: ProviderPoolRouterProfile;
  current_profile_id?: string;
  semantic_differentiation: boolean;
}

export interface ProviderPoolRouterActivation {
  id: number;
  from_profile_id?: string;
  to_profile_id: string;
  action: string;
  reason: string;
  actor: string;
  created_at: string;
}

export async function getProviderPoolPolicies(): Promise<ProviderPoolPolicy[]> {
  const resp = await fetchJSON<{ policies: ProviderPoolPolicy[] }>("/api/provider-pool-policies");
  return resp.policies || [];
}

export async function getProviderPools(): Promise<ProviderPool[]> {
  const resp = await fetchJSON<{ pools: ProviderPool[] }>("/api/provider-pools");
  return resp.pools || [];
}

export async function createProviderPool(req: ProviderPoolCreateRequest): Promise<ProviderPool> {
  return fetchJSON<ProviderPool>("/api/provider-pools", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function updateProviderPool(id: string, req: ProviderPoolUpdateRequest): Promise<ProviderPool> {
  return fetchJSON<ProviderPool>(`/api/provider-pools/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
}

export async function deleteProviderPool(id: string): Promise<void> {
  await fetchJSON<{ status: string }>(`/api/provider-pools/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
}

function providerPoolRouterPath(id: string, suffix: string): string {
  return `/api/provider-pools/${encodeURIComponent(id)}/router${suffix}`;
}

export async function getProviderPoolRouterStatus(id: string): Promise<ProviderPoolRouterStatus> {
  return fetchJSON<ProviderPoolRouterStatus>(providerPoolRouterPath(id, "/status"));
}

export async function getProviderPoolRouterSuggestion(id: string): Promise<ProviderPoolRouterSuggestion> {
  return fetchJSON<ProviderPoolRouterSuggestion>(providerPoolRouterPath(id, "/suggestion"));
}

export async function previewProviderPoolRouterEvaluation(
  id: string,
  request: ProviderPoolRouterEvaluationRequest,
): Promise<ProviderPoolRouterEvaluationPreview> {
  return fetchJSON<ProviderPoolRouterEvaluationPreview>(providerPoolRouterPath(id, "/evaluations/preview"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
}

export async function createProviderPoolRouterEvaluation(
  id: string,
  request: ProviderPoolRouterEvaluationRequest,
): Promise<ProviderPoolRouterEvaluationJob> {
  return fetchJSON<ProviderPoolRouterEvaluationJob>(providerPoolRouterPath(id, "/evaluations"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
  });
}

export async function listProviderPoolRouterEvaluations(
  id: string,
  query?: { status?: ProviderPoolRouterJobStatus; limit?: number; offset?: number },
): Promise<{ items: ProviderPoolRouterEvaluationJob[]; limit: number; offset: number }> {
  const params = new URLSearchParams();
  if (query?.status) params.set("status", query.status);
  if (query?.limit !== undefined) params.set("limit", String(query.limit));
  if (query?.offset !== undefined) params.set("offset", String(query.offset));
  const suffix = params.size ? `?${params}` : "";
  return fetchJSON(providerPoolRouterPath(id, `/evaluations${suffix}`));
}

export async function getProviderPoolRouterEvaluation(id: string, jobID: string): Promise<ProviderPoolRouterEvaluationJob> {
  return fetchJSON(providerPoolRouterPath(id, `/evaluations/${encodeURIComponent(jobID)}`));
}

export async function cancelProviderPoolRouterEvaluation(id: string, jobID: string): Promise<ProviderPoolRouterEvaluationJob> {
  return fetchJSON(providerPoolRouterPath(id, `/evaluations/${encodeURIComponent(jobID)}`), { method: "DELETE" });
}

export async function listProviderPoolRouterProfiles(
  id: string,
  query?: { limit?: number; offset?: number },
): Promise<{ items: ProviderPoolRouterProfile[]; limit: number; offset: number }> {
  const params = new URLSearchParams();
  if (query?.limit !== undefined) params.set("limit", String(query.limit));
  if (query?.offset !== undefined) params.set("offset", String(query.offset));
  const suffix = params.size ? `?${params}` : "";
  return fetchJSON(providerPoolRouterPath(id, `/profiles${suffix}`));
}

export async function getProviderPoolRouterProfile(id: string, profileID: string): Promise<ProviderPoolRouterProfile> {
  return fetchJSON(providerPoolRouterPath(id, `/profiles/${encodeURIComponent(profileID)}`));
}

export async function activateProviderPoolRouterProfile(
  id: string,
  profileID: string,
  reason: string,
): Promise<ProviderPoolRouterActivation> {
  return fetchJSON(providerPoolRouterPath(id, `/profiles/${encodeURIComponent(profileID)}/activate`), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ actor: "local-ui", reason }),
  });
}

export async function rollbackProviderPoolRouterProfile(id: string, expectedCurrentProfileID: string, reason: string): Promise<ProviderPoolRouterActivation> {
  return fetchJSON(providerPoolRouterPath(id, "/rollback"), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ actor: "local-ui", reason, expected_current_profile_id: expectedCurrentProfileID }),
  });
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

// ---------------------------------------------------------------------------
// LAN compute cluster
// ---------------------------------------------------------------------------

/**
 * Error thrown by cluster calls. Unlike the plain Error thrown by fetchJSON it
 * keeps the structured fields of the JSON error body so callers can react to
 * `code === "feature_not_licensed"` (node cap reached, with `limit`/`current`)
 * or `code === "cluster_disabled"` (HTTP 503, feature disabled in this process).
 */
export class ApiError extends Error {
  status: number;
  code?: string;
  errorCode?: string;
  limit?: number;
  current?: number;

  constructor(message: string, status: number, details?: { code?: string; errorCode?: string; limit?: number; current?: number }) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = details?.code;
    this.errorCode = details?.errorCode;
    this.limit = details?.limit;
    this.current = details?.current;
  }
}

export const CLUSTER_DISABLED = "cluster_disabled";

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

/** True for a 403 whose body carries `code: "feature_not_licensed"`. */
export function isFeatureNotLicensedError(err: unknown): err is ApiError {
  return isApiError(err) && (err.code === FEATURE_NOT_LICENSED || err.errorCode === FEATURE_NOT_LICENSED);
}

/** True for the 503 returned while the cluster feature is disabled in this process. */
export function isClusterDisabledError(err: unknown): err is ApiError {
  return isApiError(err) && (err.code === CLUSTER_DISABLED || err.errorCode === CLUSTER_DISABLED);
}

function parseApiError(body: string, contentType: string, status: number, fallback: string): ApiError {
  const message = extractErrorMessage(body, contentType, fallback);
  if (contentType.includes("application/json")) {
    try {
      const parsed = JSON.parse(body) as { code?: unknown; errorCode?: unknown; limit?: unknown; current?: unknown };
      return new ApiError(message, status, {
        code: typeof parsed.code === "string" ? parsed.code : undefined,
        errorCode: typeof parsed.errorCode === "string" ? parsed.errorCode : undefined,
        limit: typeof parsed.limit === "number" ? parsed.limit : undefined,
        current: typeof parsed.current === "number" ? parsed.current : undefined,
      });
    } catch {
      /* fall through */
    }
  }
  return new ApiError(message, status);
}

async function fetchClusterJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  const resp = await fetch(url, withLocaleHeader({ ...init, headers }));
  const contentType = resp.headers.get("content-type") || "";
  const body = await resp.text();

  if (!resp.ok) {
    throw parseApiError(body, contentType, resp.status, resp.statusText);
  }
  if (!body.trim()) return undefined as T;
  if (!contentType.includes("application/json")) {
    throw unexpectedJSONError(url, contentType, body);
  }
  try {
    return JSON.parse(body) as T;
  } catch {
    throw unexpectedJSONError(url, contentType, body);
  }
}

export type ClusterNodeHealth = "unknown" | "healthy" | "suspect" | "down" | "probing";
export type ClusterNodeState = "active" | "drain" | "maintenance";
export type ClusterRoutingMode = "local_first" | "balanced";

export interface ClusterSummaryNode {
  uuid: string;
  name: string;
  hostname?: string;
  local: boolean;
  online: boolean;
  health: ClusterNodeHealth;
  state: ClusterNodeState;
  licensed: boolean;
  version?: string;
  addr?: string;
  gpu_name?: string;
  gpu_count: number;
  vram_total: number;
  vram_used: number;
  gpu_util?: number | null;
  cpu_cores: number;
  cpu_util?: number | null;
  ram_total: number;
  ram_used: number;
  disk_total: number;
  disk_free: number;
  loaded_models: string[];
  model_count: number;
  inflight: number;
}

export interface ClusterSummary {
  in_cluster: boolean;
  cluster_name?: string;
  cluster_uuid?: string;
  node_count: number;
  online_count: number;
  /** 0 means unlimited. */
  node_limit: number;
  licensed: boolean;
  vram_total: number;
  vram_used: number;
  inflight: number;
  nodes: ClusterSummaryNode[];
}

export interface ClusterSettings {
  accept_work: boolean;
  state: ClusterNodeState;
  weight: number;
  prefer_local: boolean;
  routing_mode: ClusterRoutingMode;
  affinity_max_queue: number;
  disk_reserve_gb: number;
  static_addresses: Record<string, string>;
}

export interface ClusterGPUStatus {
  index: number;
  name: string;
  vram_total: number;
  vram_used: number;
  util?: number;
  temperature?: number;
  power_draw?: number;
  power_limit?: number;
  throttled: boolean;
  shared_memory?: boolean;
  usage_known: boolean;
}

export interface ClusterNodeModelStatus {
  id: string;
  size: number;
  format?: string;
  pipeline_tag?: string;
  category?: string;
  loaded: boolean;
  loading: boolean;
  slots?: number;
  active: number;
  expires_at?: string;
  n_gpu_layers?: number;
  native_tool_streaming: boolean;
  perf?: {
    decode_tps?: number;
    prompt_tps?: number;
    load_seconds?: number;
    samples?: number;
  };
}

export interface ClusterNodeStatus {
  uuid: string;
  name: string;
  version: string;
  protocol: number;
  cluster_uuid?: string;
  licensed: boolean;
  node_limit: number;
  accept_work: boolean;
  state: ClusterNodeState;
  weight: number;
  api_port: number;
  cluster_port: number;
  hostname?: string;
  os?: string;
  arch?: string;
  gpus: ClusterGPUStatus[];
  cpu: { cores: number; load1?: number; util?: number; model?: string };
  ram: { total: number; used: number; unified: boolean };
  disk: { path: string; total: number; free: number; read_mbps_class?: string; read_mbps?: number; io_busy: boolean };
  net: { link_mbps?: number; addrs?: string[] };
  jobs: { pulling: string[]; converting: string[] };
  model_source: { server_url: string; hf_endpoint?: string; modelscope_endpoint?: string };
  models: ClusterNodeModelStatus[];
  inflight: number;
  uptime_sec: number;
  time: string;
}

export interface ClusterNodeView {
  uuid: string;
  name: string;
  local: boolean;
  health: ClusterNodeHealth;
  online: boolean;
  addr?: string;
  last_seen?: string;
  last_error?: string;
  joined_at?: string;
  static_address?: string;
  api_port?: number;
  status?: ClusterNodeStatus;
}

export interface ClusterInfo {
  uuid: string;
  name: string;
  created_at: string;
}

export interface ClusterView {
  node: { uuid: string; name: string; version: string; cluster_port: number; api_port: number };
  cluster: ClusterInfo | null;
  node_limit: number;
  licensed: boolean;
  join_token_available: boolean;
  settings: ClusterSettings;
  members: ClusterNodeView[];
  discovered_count: number;
  model_source_mixed: boolean;
  auto_form?: boolean;
  auto_form_paused?: boolean;
  /** False while the node is dormant: no listener, no multicast, no polling. */
  active?: boolean;
}

export interface ClusterCreateResponse {
  cluster: ClusterInfo;
  join_token: string;
}

export interface ClusterTokenResponse {
  token: string;
  available: boolean;
}

export interface ClusterCodeResponse {
  code: string;
  expires_at: string;
}

export interface DiscoveredClusterNode {
  uuid: string;
  name: string;
  addr: string;
  cluster_uuid?: string;
  version?: string;
  api_port?: number;
  licensed: boolean;
  seen: string;
  source: string;
}

export interface ClusterModelNode {
  uuid: string;
  name: string;
  loaded: boolean;
  online: boolean;
  local: boolean;
}

export interface ClusterModelDistribution {
  id: string;
  size: number;
  format?: string;
  pipeline_tag?: string;
  category?: string;
  nodes: ClusterModelNode[];
}

export interface ClusterSyncResult {
  node_uuid: string;
  node_name: string;
  skipped?: string;
  error?: string;
  job?: PullJob;
}

export interface ClusterSyncResponse {
  model: string;
  results: ClusterSyncResult[];
}

export interface ClusterExplainCandidate {
  uuid: string;
  name: string;
  local: boolean;
  eligible: boolean;
  excluded?: string;
  warm: boolean;
  affinity: boolean;
  estimated_seconds: number;
  factors?: string[];
  rank: number;
}

export interface ClusterExplainResponse {
  model: string;
  order: string[];
  candidates: ClusterExplainCandidate[];
}

export interface ClusterRecommendation {
  model: string;
  node_uuid: string;
  node_name: string;
  reason: string;
}

export interface ClusterSettingsUpdate {
  accept_work?: boolean;
  prefer_local?: boolean;
  routing_mode?: ClusterRoutingMode;
  affinity_max_queue?: number;
  disk_reserve_gb?: number;
  weight?: number;
  state?: ClusterNodeState;
}

export function getClusterSummary(): Promise<ClusterSummary> {
  return fetchClusterJSON<ClusterSummary>("/api/cluster/summary", { cache: "no-store" });
}

export function getCluster(): Promise<ClusterView> {
  return fetchClusterJSON<ClusterView>("/api/cluster", { cache: "no-store" });
}

export function createCluster(name: string): Promise<ClusterCreateResponse> {
  return fetchClusterJSON<ClusterCreateResponse>("/api/cluster", {
    method: "POST",
    body: JSON.stringify({ name: name.trim() }),
  });
}

export function leaveCluster(): Promise<ClusterView> {
  return fetchClusterJSON<ClusterView>("/api/cluster", { method: "DELETE" });
}

export function joinCluster(token: string, address?: string): Promise<ClusterView> {
  return fetchClusterJSON<ClusterView>("/api/cluster/join", {
    method: "POST",
    body: JSON.stringify({ token: token.trim(), address: address?.trim() || undefined }),
  });
}

export function getClusterToken(): Promise<ClusterTokenResponse> {
  return fetchClusterJSON<ClusterTokenResponse>("/api/cluster/token", { cache: "no-store" });
}

export function rotateClusterToken(): Promise<ClusterTokenResponse> {
  return fetchClusterJSON<ClusterTokenResponse>("/api/cluster/token/rotate", { method: "POST" });
}

export function getClusterCode(): Promise<ClusterCodeResponse> {
  return fetchClusterJSON<ClusterCodeResponse>("/api/cluster/code", { cache: "no-store" });
}

export async function enableCluster(): Promise<ClusterView> {
  return fetchClusterJSON<ClusterView>("/api/cluster/enable", { method: "POST" });
}

export async function disableCluster(): Promise<ClusterView> {
  return fetchClusterJSON<ClusterView>("/api/cluster/disable", { method: "POST" });
}

export async function getDiscoveredClusterNodes(): Promise<DiscoveredClusterNode[]> {
  const data = await fetchClusterJSON<{ nodes?: DiscoveredClusterNode[] }>("/api/cluster/discovered", { cache: "no-store" });
  return data?.nodes ?? [];
}

export function inviteClusterNode(code: string, options?: { uuid?: string; address?: string }): Promise<ClusterNodeView> {
  return fetchClusterJSON<ClusterNodeView>("/api/cluster/invite", {
    method: "POST",
    body: JSON.stringify({
      code: code.trim(),
      uuid: options?.uuid || undefined,
      address: options?.address?.trim() || undefined,
    }),
  });
}

export function updateClusterNode(uuid: string, patch: { name?: string; static_address?: string }): Promise<ClusterNodeView> {
  return fetchClusterJSON<ClusterNodeView>(`/api/cluster/nodes/${encodeURIComponent(uuid)}`, {
    method: "PUT",
    body: JSON.stringify(patch),
  });
}

export function removeClusterNode(uuid: string): Promise<ClusterView> {
  return fetchClusterJSON<ClusterView>(`/api/cluster/nodes/${encodeURIComponent(uuid)}`, { method: "DELETE" });
}

export function setClusterNodeState(uuid: string, state: ClusterNodeState): Promise<ClusterSettings> {
  return fetchClusterJSON<ClusterSettings>(`/api/cluster/nodes/${encodeURIComponent(uuid)}/state`, {
    method: "POST",
    body: JSON.stringify({ state }),
  });
}

export function pullClusterNodeModel(
  uuid: string,
  model: string,
  options?: { artifactSource?: ArtifactSource; revision?: string; quant?: string },
): Promise<PullJob> {
  return fetchClusterJSON<PullJob>(`/api/cluster/nodes/${encodeURIComponent(uuid)}/models/pull`, {
    method: "POST",
    body: JSON.stringify({
      model,
      artifact_source: options?.artifactSource || undefined,
      revision: options?.revision?.trim() || undefined,
      quant: options?.quant || undefined,
    }),
  });
}

export async function getClusterModels(): Promise<ClusterModelDistribution[]> {
  const data = await fetchClusterJSON<{ models?: ClusterModelDistribution[] }>("/api/cluster/models", { cache: "no-store" });
  return data?.models ?? [];
}

export function syncClusterModel(model: string, nodes: string[] | "all"): Promise<ClusterSyncResponse> {
  return fetchClusterJSON<ClusterSyncResponse>("/api/cluster/models/sync", {
    method: "POST",
    body: JSON.stringify({ model, nodes }),
  });
}

export function explainClusterScheduling(
  model: string,
  options?: { promptTokens?: number; maxTokens?: number },
): Promise<ClusterExplainResponse> {
  const query = new URLSearchParams({ model });
  if (options?.promptTokens !== undefined) query.set("prompt_tokens", String(options.promptTokens));
  if (options?.maxTokens !== undefined) query.set("max_tokens", String(options.maxTokens));
  return fetchClusterJSON<ClusterExplainResponse>(`/api/cluster/explain?${query.toString()}`, { cache: "no-store" });
}

export async function getClusterRecommendations(): Promise<ClusterRecommendation[]> {
  const data = await fetchClusterJSON<{ recommendations?: ClusterRecommendation[] }>("/api/cluster/recommendations", { cache: "no-store" });
  return data?.recommendations ?? [];
}

export function updateClusterSettings(patch: ClusterSettingsUpdate): Promise<ClusterSettings> {
  return fetchClusterJSON<ClusterSettings>("/api/cluster/settings", {
    method: "PUT",
    body: JSON.stringify(patch),
  });
}

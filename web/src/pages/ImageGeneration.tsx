import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import {
  cancelImageGenerationJob,
  clearCloudAPIKey,
  createImageGenerationJob,
  getImageGenerationJob,
  getImageRuntimeStatus,
  getCloudAuthStatus,
  getTags,
  installImageRuntime,
  listImageGenerationJobs,
  saveCloudToken,
} from "../api/client";
import type { CloudAuthStatus, ImageGenerationJobResponse, ImageRuntimeStatus, ModelInfo } from "../api/client";
import { ApiInfoDialog } from "../components/ApiInfoDialog";
import { locale, t } from "../i18n";
import { isImageToImageModel, stripDataURL } from "../utils/imageModels";

const defaultWidth = "1024";
const defaultHeight = "1024";
const defaultSteps = "20";
const defaultCFGScale = "7.5";
const defaultEditSteps = "50";
const defaultEditCFGScale = "4.0";
const minSize = 256;
const maxSize = 2048;
const minSteps = 1;
const maxSteps = 80;
const minCFGScale = 1;
const maxCFGScale = 20;

const models = signal<ModelInfo[]>([]);
const selectedModel = signal("");
const prompt = signal("");
const negativePrompt = signal("");
const width = signal(defaultWidth);
const height = signal(defaultHeight);
const steps = signal(defaultSteps);
const seed = signal("");
const cfgScale = signal(defaultCFGScale);
const loading = signal(false);
const installing = signal(false);
const error = signal("");
const runtime = signal<ImageRuntimeStatus | null>(null);
const imageJobs = signal<ImageGenerationJobResponse[]>([]);
const previewImage = signal<PreviewImage | null>(null);
const activeJobID = signal("");
const currentResultJobID = signal("");
const jobStatus = signal("");
const generationStartedAt = signal(0);
const elapsedSeconds = signal(0);
const negativeSuggestionsOpen = signal(false);
const advancedSettingsOpen = signal(false);
const inputImages = signal<string[]>([]);
const apiDialogOpen = signal(false);
const cloudAuthDialogOpen = signal(false);
const cloudAuth = signal<CloudAuthStatus | null>(null);
const cloudAuthLoaded = signal(false);
const cloudAuthError = signal("");
const cloudTokenInput = signal("");
const isSavingCloudToken = signal(false);
const providersChangedEvent = "csghub:providers-changed";

interface GenerationHistoryItem {
  id: string;
  createdAt: number;
  model: string;
  status: string;
  images: string[];
  prompt: string;
  negativePrompt: string;
  size: string;
  steps?: number;
  seed?: number;
  cfgScale?: number;
  error?: string;
}

interface PreviewImage {
  src: string;
  title: string;
}

interface SizePreset {
  key: string;
  labelKey: string;
  width: string;
  height: string;
}

interface ParameterPreset {
  key: string;
  labelKey: string;
  promptKey?: string;
  width: string;
  height: string;
  steps: string;
  cfgScale: string;
  seed: string;
  negativePrompt: string;
}

const sizePresets: SizePreset[] = [
  { key: "square", labelKey: "image.sizeSquare", width: "1024", height: "1024" },
  { key: "landscape", labelKey: "image.sizeLandscape", width: "1344", height: "768" },
  { key: "portrait", labelKey: "image.sizePortrait", width: "768", height: "1344" },
  { key: "classicLandscape", labelKey: "image.sizeClassicLandscape", width: "1152", height: "864" },
  { key: "classicPortrait", labelKey: "image.sizeClassicPortrait", width: "864", height: "1152" },
];

const parameterPresets: ParameterPreset[] = [
  {
    key: "default",
    labelKey: "image.presetDefault",
    width: defaultWidth,
    height: defaultHeight,
    steps: defaultSteps,
    cfgScale: defaultCFGScale,
    seed: "",
    negativePrompt: "",
  },
  {
    key: "portrait",
    labelKey: "image.presetPortrait",
    promptKey: "image.presetPortraitPrompt",
    width: "768",
    height: "1344",
    steps: "28",
    cfgScale: "7.5",
    seed: "",
    negativePrompt: "deformed face, bad hands, extra fingers, blurry",
  },
  {
    key: "landscape",
    labelKey: "image.presetLandscape",
    promptKey: "image.presetLandscapePrompt",
    width: "1344",
    height: "768",
    steps: "30",
    cfgScale: "8",
    seed: "",
    negativePrompt: "low quality, blurry, overexposed, watermark",
  },
  {
    key: "draft",
    labelKey: "image.presetDraft",
    promptKey: "image.presetDraftPrompt",
    width: "768",
    height: "768",
    steps: "12",
    cfgScale: "6",
    seed: "",
    negativePrompt: "low quality, blurry",
  },
];

const negativeSuggestionKeys = [
  "image.negativeLowQuality",
  "image.negativeBlurry",
  "image.negativeBadHands",
  "image.negativeWatermark",
  "image.negativeDistorted",
  "image.negativeExtraLimbs",
];

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function optionalNumber(value: string): number | undefined {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : undefined;
}

function optionalBoundedNumber(value: string, min: number, max: number): number | undefined {
  const n = optionalNumber(value);
  if (n === undefined) return undefined;
  return Math.min(max, Math.max(min, n));
}

function dimensionValue(value: string): number | undefined {
  const n = Number(value.trim());
  if (!Number.isInteger(n) || n < minSize || n > maxSize) {
    return undefined;
  }
  return n;
}

function currentSize(): string | null {
  const w = dimensionValue(width.value);
  const h = dimensionValue(height.value);
  if (!w || !h) return null;
  return `${w}x${h}`;
}

function applySizePreset(preset: SizePreset) {
  width.value = preset.width;
  height.value = preset.height;
}

function applyParameterPreset(preset: ParameterPreset) {
  if (preset.promptKey) {
    prompt.value = t(preset.promptKey);
  }
  width.value = preset.width;
  height.value = preset.height;
  steps.value = preset.steps;
  cfgScale.value = preset.cfgScale;
  seed.value = preset.seed;
  negativePrompt.value = preset.negativePrompt;
}

function resetParameters() {
  applyParameterPreset(parameterPresets[0]);
}

function randomizeSeed() {
  seed.value = String(Math.floor(Math.random() * 2147483647));
}

function appendNegativeTerm(term: string) {
  const existing = negativePrompt.value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
  const seen = new Set(existing.map((item) => item.toLowerCase()));
  if (!seen.has(term.toLowerCase())) {
    existing.push(term);
  }
  negativePrompt.value = existing.join(", ");
}

function imageDataURL(image: string): string {
  if (/^(https?:|blob:)/i.test(image)) return image;
  return image.startsWith("data:") ? image : `data:image/png;base64,${image}`;
}

function hasCloudAuth(status: CloudAuthStatus | null | undefined): boolean {
  return Boolean(status?.authenticated || status?.has_api_key);
}

function isCloudAuthErrorMessage(message: string): boolean {
  return /(AUTH-ERR-1|AUTH-ERR-5|login first|inference error 401|Error 401|Cloud login required|login expired|save an API Key|Failed to load OpenCSG built-in API Key|需要登录云端服务|保存 API Key)/i.test(message);
}

function openExternalURL(url?: string) {
  if (!url) return;
  window.open(url, "_blank", "noopener,noreferrer");
}

function historyGridClass(count: number): string {
  return count === 1 ? "grid gap-4" : "grid gap-4 md:grid-cols-2";
}

function historyImageClass(size: string): string {
  const [w, h] = size.split("x").map((part) => Number(part));
  if (!Number.isFinite(w) || !Number.isFinite(h) || w <= 0 || h <= 0) {
    return "aspect-square";
  }
  if (h > w) {
    return "aspect-[4/5]";
  }
  if (w > h) {
    return "aspect-[16/9]";
  }
  return "aspect-square";
}

function downloadImage(image: string, item: GenerationHistoryItem, index: number) {
  const a = document.createElement("a");
  a.href = imageDataURL(image);
  a.download = `csghub-image-${item.id}-${index + 1}.png`;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

function formatClock(seconds: number): string {
  const mins = Math.floor(seconds / 60);
  const secs = seconds % 60;
  return `${mins}:${String(secs).padStart(2, "0")}`;
}

function progressPercent(): number {
  if (!loading.value) return 0;
  const stepCount = optionalBoundedNumber(steps.value, minSteps, maxSteps) || Number(defaultSteps);
  const estimate = Math.max(25, stepCount * 2);
  return Math.min(92, Math.round((elapsedSeconds.value / estimate) * 100));
}

function applyModelParameterDefaults(model?: ModelInfo) {
  if (isImageToImageModel(model)) {
    steps.value = defaultEditSteps;
    cfgScale.value = defaultEditCFGScale;
    negativePrompt.value = "";
    return;
  }
  steps.value = defaultSteps;
  cfgScale.value = defaultCFGScale;
}

function historySizeLabel(size: string): string {
  return size === "input" ? t("image.sizeFollowsInput") : size;
}

function isRunningImageJob(job: ImageGenerationJobResponse): boolean {
  return job.status === "queued" || job.status === "running";
}

function jobStatusLabel(job: ImageGenerationJobResponse): string {
  if (job.status === "queued") return t("image.jobQueued");
  if (job.status === "running") return job.request.image || (job.request.images || []).length > 0 ? t("image.jobRunningEdit") : t("image.jobRunning");
  if (job.status === "succeeded") return t("image.succeeded");
  if (job.status === "failed") return t("image.failed");
  if (job.status === "cancelled") return t("image.cancelled");
  return job.status;
}

function jobElapsedSeconds(job: ImageGenerationJobResponse): number {
  const start = new Date(job.created_at).getTime();
  const end = job.completed_at ? new Date(job.completed_at).getTime() : Date.now();
  if (!Number.isFinite(start) || !Number.isFinite(end)) return 0;
  return Math.max(0, Math.floor((end - start) / 1000));
}

function imageJobToHistoryItem(job: ImageGenerationJobResponse): GenerationHistoryItem | null {
  const images = (job.result?.data || [])
    .map((item) => item.b64_json || item.url || "")
    .filter(Boolean);
  if (job.status === "succeeded" && images.length === 0) {
    return null;
  }
  return {
    id: job.id,
    createdAt: new Date(job.created_at).getTime(),
    model: job.request.model,
    status: job.status,
    images,
    prompt: job.request.prompt,
    negativePrompt: job.request.negative_prompt || "",
    size: job.request.image || (job.request.images || []).length > 0 ? "input" : (job.request.size || "input"),
    steps: job.request.steps,
    seed: job.request.seed,
    cfgScale: job.request.cfg_scale,
    error: job.error,
  };
}

async function readInputImageFile(file: File): Promise<string> {
  const dataUrl = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ""));
    reader.onerror = () => reject(reader.error || new Error("failed to read image"));
    reader.readAsDataURL(file);
  });
  return stripDataURL(dataUrl);
}

function imageModelKey(model: ModelInfo): string {
  return `${model.source || "local"}:${model.model || model.name}`;
}

function selectedModelInfo(): ModelInfo | undefined {
  return models.value.find((model) => imageModelKey(model) === selectedModel.value);
}

function imageModelLabel(model: ModelInfo): string {
  const label = model.display_name || model.label || model.model || model.name;
  const source = model.source === "cloud" ? t("chat.cloud") : model.source?.startsWith("provider:") ? model.provider || t("chat.provider") : t("chat.local");
  const kind = isImageToImageModel(model) ? t("image.modelTypeEdit") : t("image.modelTypeGenerate");
  return `${label} [${source} · ${kind}]`;
}

function localizeImageErrorMessage(message: string, model?: ModelInfo): string {
  const provider = model?.provider || t("chat.cloud");
  if (/Cloud login required|save an API Key/i.test(message)) {
    return t("image.cloudAuthRequired", provider);
  }
  if (/Failed to load OpenCSG built-in API Key/i.test(message)) {
    return t("image.cloudBuiltinAPIKeyFailed", provider);
  }
  return message;
}

async function refreshImageModels() {
  const allModels = await getTags({ refresh: true });
  const imageModels = allModels.filter((model) => model.pipeline_tag === "text-to-image" || model.pipeline_tag === "image-to-image");
  models.value = imageModels;
  if (!selectedModel.value || !imageModels.some((model) => imageModelKey(model) === selectedModel.value)) {
    selectedModel.value = imageModels[0] ? imageModelKey(imageModels[0]) : "";
  }
}

async function refreshRuntime() {
  try {
    runtime.value = await getImageRuntimeStatus();
  } catch {
    runtime.value = null;
  }
}

async function refreshImageJobs() {
  const list = await listImageGenerationJobs();
  imageJobs.value = list.jobs || [];
}

async function refreshCloudAuth(): Promise<CloudAuthStatus> {
  try {
    const status = await getCloudAuthStatus();
    cloudAuth.value = status;
    return status;
  } finally {
    cloudAuthLoaded.value = true;
  }
}

async function openCloudAuthDialog(message = "") {
  cloudAuthError.value = message;
  cloudAuthDialogOpen.value = true;
  try {
    await refreshCloudAuth();
  } catch {
    /* ignore */
  }
}

export function ImageGeneration() {
  void locale.value;

  useEffect(() => {
    let cancelled = false;
    refreshImageModels()
      .then(() => {
        if (cancelled) return;
      })
      .catch((err) => (error.value = err.message || String(err)));
    refreshRuntime();
    refreshImageJobs().catch((err) => (error.value = err.message || String(err)));
    refreshCloudAuth().catch(() => {});
    const jobsTimer = window.setInterval(() => {
      refreshImageJobs().catch(() => {});
    }, 3000);
    const onProvidersChanged = () => {
      refreshImageModels().catch((err) => (error.value = err.message || String(err)));
    };
    window.addEventListener(providersChangedEvent, onProvidersChanged);
    return () => {
      cancelled = true;
      window.clearInterval(jobsTimer);
      window.removeEventListener(providersChangedEvent, onProvidersChanged);
    };
  }, []);

  useEffect(() => {
    const model = selectedModelInfo();
    applyModelParameterDefaults(model);
    if (model?.source === "cloud" && cloudAuthLoaded.value && !hasCloudAuth(cloudAuth.value)) {
      void openCloudAuthDialog(t("chat.cloudLoginRequired", model.provider || t("chat.cloud")));
    }
  }, [selectedModel.value, cloudAuthLoaded.value, cloudAuth.value?.authenticated, cloudAuth.value?.has_api_key]);

  useEffect(() => {
    if (!loading.value || !generationStartedAt.value) return;
    const timer = window.setInterval(() => {
      elapsedSeconds.value = Math.max(0, Math.floor((Date.now() - generationStartedAt.value) / 1000));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [loading.value, generationStartedAt.value]);

  useEffect(() => {
    if (cloudAuthDialogOpen.value && hasCloudAuth(cloudAuth.value)) {
      cloudAuthDialogOpen.value = false;
      cloudAuthError.value = "";
    }
  }, [cloudAuth.value?.authenticated, cloudAuth.value?.has_api_key]);

  const runInstall = async () => {
    installing.value = true;
    error.value = "";
    try {
      runtime.value = await installImageRuntime();
    } catch (err: any) {
      error.value = err.message || String(err);
      await refreshRuntime();
    } finally {
      installing.value = false;
    }
  };

  const runGenerate = async () => {
    if (!selectedModel.value || !prompt.value.trim()) return;
    const currentModel = selectedModelInfo();
    if (!currentModel) return;
    if (currentModel.source === "cloud") {
      const provider = currentModel.provider || t("chat.cloud");
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
    const editing = isImageToImageModel(currentModel);
    if (editing && inputImages.value.length === 0) {
      error.value = t("image.inputImageRequired");
      return;
    }
    const requestSize = editing ? undefined : currentSize();
    if (!editing && !requestSize) {
      error.value = t("image.invalidSize", minSize, maxSize);
      return;
    }
    const requestSteps = optionalBoundedNumber(steps.value, minSteps, maxSteps);
    const requestSeed = optionalNumber(seed.value);
    const requestCFGScale = optionalBoundedNumber(cfgScale.value, minCFGScale, maxCFGScale);

    loading.value = true;
    error.value = "";
    jobStatus.value = t("image.jobQueued");
    generationStartedAt.value = Date.now();
    elapsedSeconds.value = 0;
    currentResultJobID.value = "";
    try {
      const job = await createImageGenerationJob({
        model: currentModel.model || currentModel.name,
        source: currentModel.source,
        prompt: prompt.value,
        negative_prompt: editing ? undefined : (negativePrompt.value.trim() || undefined),
        size: requestSize ?? undefined,
        steps: requestSteps,
        seed: requestSeed,
        cfg_scale: requestCFGScale,
        image: inputImages.value[0],
        images: inputImages.value.length > 1 ? inputImages.value.slice(1) : undefined,
      });
      activeJobID.value = job.id;
      await refreshImageJobs();
      let latest = job;
      while (loading.value) {
        if (latest.status === "succeeded") {
          await refreshImageJobs();
          currentResultJobID.value = latest.id;
          break;
        }
        if (latest.status === "failed") {
          const message = latest.error || t("image.failed");
          if (currentModel.source === "cloud" && isCloudAuthErrorMessage(message)) {
            await openCloudAuthDialog(localizeImageErrorMessage(message, currentModel));
          }
          throw new Error(message);
        }
        if (latest.status === "cancelled") {
          throw new Error(t("image.cancelled"));
        }
        jobStatus.value = latest.status === "queued" ? t("image.jobQueued") : (editing ? t("image.jobRunningEdit") : t("image.jobRunning"));
        await sleep(1500);
        latest = await getImageGenerationJob(job.id);
        imageJobs.value = [latest, ...imageJobs.value.filter((item) => item.id !== latest.id)];
      }
      await refreshRuntime();
    } catch (err: any) {
      const message = err.message || String(err);
      const localizedMessage = localizeImageErrorMessage(message, currentModel);
      if (currentModel.source === "cloud" && isCloudAuthErrorMessage(message)) {
        await openCloudAuthDialog(localizedMessage);
      } else {
        error.value = localizedMessage;
      }
      await refreshRuntime();
    } finally {
      activeJobID.value = "";
      jobStatus.value = "";
      generationStartedAt.value = 0;
      loading.value = false;
    }
  };

  const cancelGenerate = async () => {
    const id = activeJobID.value;
    if (id) {
      await cancelImageGenerationJob(id).catch(() => {});
    }
    await refreshImageJobs().catch(() => {});
    loading.value = false;
    jobStatus.value = "";
    generationStartedAt.value = 0;
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
        cloudAuthError.value = t("chat.cloudLoginExpired", currentModel?.provider || t("chat.cloud"));
        return;
      }
      // The image dialog saves account tokens; clear any stale manual API key
      // that may have been saved by older builds so cloud calls use the
      // account's built-in API key.
      cloudAuth.value = await clearCloudAPIKey();
      cloudTokenInput.value = "";
      cloudAuthDialogOpen.value = false;
      error.value = "";
      await refreshImageModels();
    } catch (err: any) {
      cloudAuthError.value = err?.message || t("chat.failedResp");
    } finally {
      isSavingCloudToken.value = false;
    }
  };

  const rt = runtime.value;
  const currentModel = selectedModelInfo();
  const selectedModelIsCloud = currentModel?.source === "cloud";
  const selectedModelIsEdit = isImageToImageModel(currentModel);
  const selectedSizeKey = sizePresets.find((preset) => preset.width === width.value && preset.height === height.value)?.key || "";
  const progress = progressPercent();
  const runningJobs = imageJobs.value.filter(isRunningImageJob);
  const historyItems = imageJobs.value
    .filter((job) => !isRunningImageJob(job))
    .map(imageJobToHistoryItem)
    .filter((item): item is GenerationHistoryItem => Boolean(item));
  const latestResultJob = imageJobs.value.find((job) => job.id === currentResultJobID.value && job.status === "succeeded");
  const latestResult = latestResultJob ? imageJobToHistoryItem(latestResultJob) : null;

  const handleInputImageChange = async (event: Event) => {
    const input = event.target as HTMLInputElement;
    const files = Array.from(input.files || []);
    input.value = "";
    if (files.length === 0) return;
    try {
      const encoded = await Promise.all(files.map((file) => readInputImageFile(file)));
      inputImages.value = [...inputImages.value, ...encoded];
      error.value = "";
    } catch (err: any) {
      error.value = err.message || String(err);
    }
  };

  return (
    <div class="mx-auto max-w-6xl space-y-6 p-8">
      <div class="flex items-start justify-between gap-4">
        <div>
          <h1 class="text-2xl font-bold text-gray-900">{t("image.title")}</h1>
          <p class="mt-2 text-sm text-gray-500">{t("image.subtitle")}</p>
        </div>
        <div class="flex items-center gap-2">
          {historyItems.length > 0 && (
            <a href="/images/history" class="rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50">
              {t("image.history")}
              <span class="ml-1 text-xs text-gray-400">({historyItems.length})</span>
            </a>
          )}
        {currentModel && (
          <button
            type="button"
            onClick={() => (apiDialogOpen.value = true)}
            class="rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50"
          >
            {t("dash.apiInfo")}
          </button>
        )}
        </div>
      </div>

      {rt && !rt.ready && !selectedModelIsCloud && (
        <section class="rounded-xl border border-amber-200 bg-amber-50 p-4">
          <div class="flex items-start justify-between gap-4">
            <div>
              <h2 class="font-semibold text-amber-900">{t("image.runtimeRequired")}</h2>
              <p class="mt-1 text-sm text-amber-800">{rt.error || t("image.runtimeMissing")}</p>
              <p class="mt-1 text-xs text-amber-700">
                {rt.platform}/{rt.arch} · {rt.hardware}
              </p>
            </div>
            <button
              onClick={runInstall}
              disabled={installing.value}
              class="rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
            >
              {installing.value ? t("image.installing") : t("image.installRuntime")}
            </button>
          </div>
        </section>
      )}

      {error.value && (
        <div class="flex flex-col gap-3 rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 sm:flex-row sm:items-center sm:justify-between">
          <span>{error.value}</span>
          {isCloudAuthErrorMessage(error.value) && (
            <button
              type="button"
              onClick={() => openCloudAuthDialog(error.value)}
              class="shrink-0 rounded-lg bg-red-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-red-700"
            >
              {t("image.openCloudAuth")}
            </button>
          )}
        </div>
      )}

      <div class="grid gap-6 lg:grid-cols-[360px_1fr]">
        <section class="space-y-4 rounded-xl border border-gray-200 bg-white p-5">
          <label class="block">
            <span class="text-sm font-medium text-gray-700">{t("image.model")}</span>
            <select
              class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm"
              value={selectedModel.value}
              onChange={(e) => (selectedModel.value = (e.target as HTMLSelectElement).value)}
            >
              {models.value.map((model) => (
                <option key={imageModelKey(model)} value={imageModelKey(model)}>
                  {imageModelLabel(model)}
                </option>
              ))}
            </select>
          </label>
          <label class="block">
            <span class="text-sm font-medium text-gray-700">{t("image.prompt")}</span>
            <textarea
              class="mt-1 min-h-32 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm"
              value={prompt.value}
              onInput={(e) => (prompt.value = (e.target as HTMLTextAreaElement).value)}
              placeholder={t("image.promptPlaceholder")}
            />
          </label>
          {selectedModelIsEdit && (
            <div class="block space-y-2">
              <div>
                <span class="text-sm font-medium text-gray-700">{t("image.inputImage")}</span>
                <p class="mt-1 text-xs text-gray-500">{t("image.inputImageHint")}</p>
              </div>
              <input
                type="file"
                accept="image/png,image/jpeg,image/webp"
                multiple
                onChange={handleInputImageChange}
                class="block w-full text-sm text-gray-600 file:mr-3 file:rounded-lg file:border-0 file:bg-indigo-50 file:px-3 file:py-2 file:text-sm file:font-medium file:text-indigo-700 hover:file:bg-indigo-100"
              />
              {inputImages.value.length > 0 && (
                <div class="grid grid-cols-2 gap-2">
                  {inputImages.value.map((image, index) => (
                    <div key={`${index}-${image.slice(0, 16)}`} class="relative overflow-hidden rounded-lg border border-gray-200 bg-gray-50">
                      <img src={imageDataURL(image)} alt="" class="aspect-square w-full object-cover" />
                      <button
                        type="button"
                        onClick={() => {
                          inputImages.value = inputImages.value.filter((_, itemIndex) => itemIndex !== index);
                        }}
                        class="absolute right-2 top-2 rounded-md bg-white/90 px-2 py-1 text-xs font-medium text-gray-700 shadow hover:bg-white"
                      >
                        {t("image.removeInputImage")}
                      </button>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
          <details
            class="rounded-xl border border-gray-200 bg-gray-50 p-3"
            open={advancedSettingsOpen.value}
            onToggle={(e) => (advancedSettingsOpen.value = (e.currentTarget as HTMLDetailsElement).open)}
          >
            <summary class="cursor-pointer text-sm font-medium text-gray-700">{t("image.advancedSettings")}</summary>
            <div class="mt-4 space-y-4">
          {!selectedModelIsEdit && (
          <>
          <label class="block">
            <span class="text-sm font-medium text-gray-700">{t("image.negativePrompt")}</span>
            <textarea
              class="mt-1 min-h-20 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm"
              value={negativePrompt.value}
              onInput={(e) => (negativePrompt.value = (e.target as HTMLTextAreaElement).value)}
              placeholder={t("image.negativePlaceholder")}
            />
            <p class="mt-1 text-xs text-gray-500">{t("image.negativeHint")}</p>
          </label>
          <details
            class="rounded-lg border border-gray-200 bg-white p-3"
            open={negativeSuggestionsOpen.value}
            onToggle={(e) => (negativeSuggestionsOpen.value = (e.currentTarget as HTMLDetailsElement).open)}
          >
            <summary class="cursor-pointer text-sm font-medium text-gray-700">{t("image.negativeSuggestions")}</summary>
            <div class="mt-3 flex flex-wrap gap-2">
              {negativeSuggestionKeys.map((key) => (
                <button
                  key={key}
                  type="button"
                  onClick={() => appendNegativeTerm(t(key))}
                  class="rounded-full border border-gray-200 bg-white px-3 py-1 text-xs text-gray-600 hover:border-indigo-300 hover:text-indigo-700"
                >
                  {t(key)}
                </button>
              ))}
            </div>
          </details>

          <div class="space-y-3 rounded-xl border border-gray-200 bg-white p-3">
            <div class="flex items-center justify-between gap-3">
              <span class="text-sm font-medium text-gray-700">{t("image.presets")}</span>
              <button type="button" onClick={resetParameters} class="text-xs font-medium text-indigo-600 hover:text-indigo-700">
                {t("image.reset")}
              </button>
            </div>
            <p class="text-xs text-gray-500">{t("image.presetsHint")}</p>
            <div class="grid grid-cols-2 gap-2">
              {parameterPresets.map((preset) => (
                <button
                  key={preset.key}
                  type="button"
                  onClick={() => applyParameterPreset(preset)}
                  title={preset.promptKey ? t(preset.promptKey) : t("image.presetKeepsPrompt")}
                  class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-xs font-medium text-gray-600 hover:border-indigo-300 hover:text-indigo-700"
                >
                  {t(preset.labelKey)}
                  <span class="mt-0.5 block truncate text-[11px] font-normal text-gray-400">
                    {preset.promptKey ? t("image.presetFillsPrompt") : t("image.presetKeepsPrompt")}
                  </span>
                </button>
              ))}
            </div>
          </div>
          </>
          )}

          <div class="space-y-3">
            {!selectedModelIsEdit && (
            <div>
              <div class="mb-2 flex items-center justify-between">
                <span class="text-sm font-medium text-gray-700">{t("image.size")}</span>
                <span class="text-xs text-gray-500">{t("image.sizeHint")}</span>
              </div>
              <div class="grid grid-cols-2 gap-2">
                {sizePresets.map((preset) => (
                  <button
                    key={preset.key}
                    type="button"
                    onClick={() => applySizePreset(preset)}
                    class={`rounded-lg border px-3 py-2 text-xs font-medium ${
                      selectedSizeKey === preset.key
                        ? "border-indigo-500 bg-indigo-50 text-indigo-700"
                        : "border-gray-200 bg-white text-gray-600 hover:border-indigo-300"
                    }`}
                  >
                    {t(preset.labelKey)}
                    <span class="ml-1 text-gray-400">{preset.width}x{preset.height}</span>
                  </button>
                ))}
              </div>
              <div class="mt-3 grid grid-cols-2 gap-3">
                <NumberField label={t("image.width")} value={width.value} min={minSize} max={maxSize} onInput={(v) => (width.value = v)} />
                <NumberField label={t("image.height")} value={height.value} min={minSize} max={maxSize} onInput={(v) => (height.value = v)} />
              </div>
            </div>
            )}

            <NumberField
              label={t("image.steps")}
              value={steps.value}
              min={minSteps}
              max={maxSteps}
              onInput={(v) => (steps.value = v)}
              hint={selectedModelIsEdit ? t("image.stepsHintEdit") : t("image.stepsHint")}
            />
            <div>
              <NumberField label={t("image.seed")} value={seed.value} onInput={(v) => (seed.value = v)} hint={t("image.seedHint")} />
              <button type="button" onClick={randomizeSeed} class="mt-2 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50">
                {t("image.randomSeed")}
              </button>
            </div>
            <NumberField
              label={selectedModelIsEdit ? t("image.cfgScaleEdit") : t("image.cfgScale")}
              value={cfgScale.value}
              min={minCFGScale}
              max={maxCFGScale}
              step="0.5"
              onInput={(v) => (cfgScale.value = v)}
              hint={selectedModelIsEdit ? t("image.cfgHintEdit") : t("image.cfgHint")}
            />
          </div>
            </div>
          </details>
          <div class="space-y-2">
            <button
              onClick={runGenerate}
              disabled={loading.value || !selectedModel.value || !prompt.value.trim()}
              class="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-50"
            >
              {loading.value ? (jobStatus.value || (selectedModelIsEdit ? t("image.editing") : t("image.generating"))) : (selectedModelIsEdit ? t("image.edit") : t("image.generate"))}
            </button>
            {loading.value && (
              <button
                type="button"
                onClick={cancelGenerate}
                class="w-full rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 hover:bg-gray-50"
              >
                {t("image.cancel")}
              </button>
            )}
          </div>
        </section>

        <section class="min-h-[520px] space-y-5 rounded-xl border border-gray-200 bg-white p-5">
          <div class="flex items-center justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-gray-900">{t("image.jobs")}</h2>
              <p class="text-xs text-gray-500">{t("image.jobsHint")}</p>
            </div>
            <button type="button" onClick={() => refreshImageJobs().catch((err) => (error.value = err.message || String(err)))} class="rounded-lg border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50">
              {t("image.refreshJobs")}
            </button>
          </div>

          {loading.value && (
            <div class="rounded-xl border border-indigo-100 bg-indigo-50 p-4">
              <div class="flex items-center justify-between text-sm">
                <span class="font-medium text-indigo-900">{jobStatus.value || (selectedModelIsEdit ? t("image.editing") : t("image.generating"))}</span>
                <span class="text-indigo-700">{t("image.elapsed", formatClock(elapsedSeconds.value))}</span>
              </div>
              <div class="mt-3 h-2 overflow-hidden rounded-full bg-indigo-100">
                <div class="h-full rounded-full bg-indigo-600 transition-all duration-500" style={{ width: `${progress}%` }} />
              </div>
              <p class="mt-2 text-xs text-indigo-700">{t("image.progressHint")}</p>
            </div>
          )}

          {latestResult && latestResult.images.length > 0 && (
            <article class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm">
              <div class="space-y-3 border-b border-gray-100 bg-gray-50/60 p-4">
                <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                  <div class="min-w-0 space-y-1">
                    <span class="inline-flex rounded-full bg-green-50 px-2.5 py-1 text-xs font-medium text-green-700">{t("image.latestResult")}</span>
                    <p class="line-clamp-2 text-sm font-semibold leading-6 text-gray-900">{latestResult.prompt}</p>
                  </div>
                  <time class="shrink-0 text-left text-xs text-gray-400 sm:text-right" dateTime={new Date(latestResult.createdAt).toISOString()}>
                    {new Date(latestResult.createdAt).toLocaleString()}
                  </time>
                </div>
                <div class="flex flex-wrap gap-2 text-xs text-gray-600">
                  <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{historySizeLabel(latestResult.size)}</span>
                  <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.steps")}: {latestResult.steps || "-"}</span>
                  <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.seed")}: {latestResult.seed ?? t("image.random")}</span>
                </div>
              </div>
              <div class={`p-4 ${historyGridClass(latestResult.images.length)}`}>
                {latestResult.images.map((img, i) => (
                  <div key={`${latestResult.id}-latest-${i}`} class="overflow-hidden rounded-xl border border-gray-200 bg-gray-50">
                    <button
                      type="button"
                      onClick={() => (previewImage.value = { src: imageDataURL(img), title: latestResult.prompt })}
                      class={`block w-full overflow-hidden bg-gray-100 ${historyImageClass(latestResult.size)}`}
                    >
                      <img src={imageDataURL(img)} alt={latestResult.prompt} class="h-full w-full object-contain transition-opacity duration-200 hover:opacity-95" />
                    </button>
                    <div class="flex items-center justify-between gap-3 bg-white px-3 py-2">
                      <span class="truncate text-xs text-gray-400">{latestResult.images.length > 1 ? `${i + 1}/${latestResult.images.length}` : historySizeLabel(latestResult.size)}</span>
                      <button
                        type="button"
                        onClick={() => downloadImage(img, latestResult, i)}
                        class="rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50"
                      >
                        {t("image.download")}
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            </article>
          )}

          {runningJobs.length > 0 && (
          <div class="rounded-2xl border border-gray-200 bg-gray-50/60 p-4">
            <div class="mb-3 flex items-center justify-between gap-3">
              <div>
                <h3 class="text-sm font-semibold text-gray-900">{t("image.runningJobs")}</h3>
                <p class="text-xs text-gray-500">{t("image.runningJobsHint")}</p>
              </div>
              <span class="rounded-full bg-white px-2.5 py-1 text-xs text-gray-500 ring-1 ring-gray-200">{runningJobs.length}</span>
            </div>
            {runningJobs.length === 0 ? (
              <div class="rounded-xl border border-dashed border-gray-200 bg-white px-4 py-6 text-center text-sm text-gray-400">
                {t("image.noRunningJobs")}
              </div>
            ) : (
              <div class="space-y-3">
                {runningJobs.map((job) => (
                  <article key={job.id} class="rounded-xl border border-indigo-100 bg-white p-4">
                    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                      <div class="min-w-0 space-y-1">
                        <div class="flex flex-wrap items-center gap-2">
                          <span class="rounded-full bg-indigo-50 px-2.5 py-1 text-xs font-medium text-indigo-700">{jobStatusLabel(job)}</span>
                          <span class="text-xs text-gray-400">{t("image.elapsed", formatClock(jobElapsedSeconds(job)))}</span>
                        </div>
                        <p class="line-clamp-2 text-sm font-semibold text-gray-900">{job.request.prompt}</p>
                        <p class="truncate text-xs text-gray-500">{t("image.model")}: {job.request.model}</p>
                      </div>
                      <button
                        type="button"
                        onClick={async () => {
                          await cancelImageGenerationJob(job.id).catch(() => {});
                          await refreshImageJobs().catch(() => {});
                          if (activeJobID.value === job.id) {
                            loading.value = false;
                            activeJobID.value = "";
                            jobStatus.value = "";
                            generationStartedAt.value = 0;
                          }
                        }}
                        class="shrink-0 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50"
                      >
                        {t("image.cancel")}
                      </button>
                    </div>
                  </article>
                ))}
              </div>
            )}
          </div>
          )}
        </section>
      </div>

      {previewImage.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-6" onClick={() => (previewImage.value = null)}>
          <div class="max-h-full max-w-5xl rounded-xl bg-white p-4 shadow-xl" onClick={(e) => e.stopPropagation()}>
            <div class="mb-3 flex items-center justify-between gap-4">
              <p class="truncate text-sm font-medium text-gray-700">{previewImage.value.title}</p>
              <button type="button" onClick={() => (previewImage.value = null)} class="rounded-lg border border-gray-200 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-50">
                {t("image.closePreview")}
              </button>
            </div>
            <img src={previewImage.value.src} class="max-h-[78vh] max-w-full rounded-lg object-contain" />
          </div>
        </div>
      )}
      {cloudAuthDialogOpen.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4">
          <div class="w-full max-w-lg rounded-2xl bg-white p-6 shadow-2xl">
            <div class="flex items-start justify-between gap-4">
              <div>
                <h3 class="text-lg font-semibold text-gray-900">{t("chat.cloudLoginTitle", currentModel?.provider || t("chat.cloud"))}</h3>
                <p class="mt-2 text-sm leading-6 text-gray-500">{t("chat.cloudLoginDesc", currentModel?.provider || t("chat.cloud"))}</p>
              </div>
              <button
                onClick={() => {
                  cloudAuthDialogOpen.value = false;
                  cloudAuthError.value = "";
                }}
                class="rounded-lg p-1 text-gray-400 hover:text-gray-600"
                aria-label={t("chat.cloudCancel")}
              >
                <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            {cloudAuthError.value && (
              <div class="mt-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
                {cloudAuthError.value}
              </div>
            )}

            <div class="mt-5 flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => openExternalURL(cloudAuth.value?.login_url)}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50"
              >
                {t("chat.cloudOpenLogin")}
              </button>
              <button
                type="button"
                onClick={() => openExternalURL(cloudAuth.value?.access_token_url)}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50"
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
                type="button"
                onClick={() => {
                  cloudAuthDialogOpen.value = false;
                  cloudAuthError.value = "";
                }}
                class="rounded-lg border border-gray-200 px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50"
              >
                {t("chat.cloudCancel")}
              </button>
              <button
                type="button"
                onClick={handleSaveCloudToken}
                disabled={isSavingCloudToken.value}
                class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white transition-colors hover:bg-indigo-700 disabled:opacity-60"
              >
                {isSavingCloudToken.value ? t("chat.cloudSavingToken") : t("chat.cloudSaveToken")}
              </button>
            </div>
          </div>
        </div>
      )}
      {apiDialogOpen.value && currentModel && (
        <ApiInfoDialog
          model={currentModel.model || currentModel.name}
          pipelineTag={currentModel.pipeline_tag}
          onClose={() => (apiDialogOpen.value = false)}
        />
      )}
    </div>
  );
}

function NumberField({
  label,
  value,
  onInput,
  min,
  max,
  step,
  hint,
}: {
  label: string;
  value: string;
  onInput: (value: string) => void;
  min?: number;
  max?: number;
  step?: string;
  hint?: string;
}) {
  return (
    <label class="block">
      <span class="flex items-center gap-1 text-sm font-medium text-gray-700">
        {label}
        {hint && <span class="cursor-help rounded-full bg-gray-100 px-1.5 text-xs text-gray-500" title={hint}>?</span>}
      </span>
      <input
        type="number"
        min={min}
        max={max}
        step={step || "1"}
        class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm"
        value={value}
        onInput={(e) => onInput((e.target as HTMLInputElement).value)}
      />
      {hint && <p class="mt-1 text-xs text-gray-500">{hint}</p>}
    </label>
  );
}


import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import {
  cancelImageGenerationJob,
  createImageGenerationJob,
  getImageGenerationJob,
  getImageRuntimeStatus,
  getTags,
  installImageRuntime,
} from "../api/client";
import type { ImageRuntimeStatus, ModelInfo } from "../api/client";
import { locale, t } from "../i18n";

const defaultWidth = "1024";
const defaultHeight = "1024";
const defaultSteps = "20";
const defaultCFGScale = "7.5";
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
const history = signal<GenerationHistoryItem[]>([]);
const previewImage = signal<PreviewImage | null>(null);
const activeJobID = signal("");
const jobStatus = signal("");
const generationStartedAt = signal(0);
const elapsedSeconds = signal(0);
const negativeSuggestionsOpen = signal(false);
const providersChangedEvent = "csghub:providers-changed";

interface GenerationHistoryItem {
  id: string;
  createdAt: number;
  images: string[];
  prompt: string;
  negativePrompt: string;
  size: string;
  steps?: number;
  seed?: number;
  cfgScale?: number;
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
  return image.startsWith("data:") ? image : `data:image/png;base64,${image}`;
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

function imageModelKey(model: ModelInfo): string {
  return `${model.source || "local"}:${model.model || model.name}`;
}

function selectedModelInfo(): ModelInfo | undefined {
  return models.value.find((model) => imageModelKey(model) === selectedModel.value);
}

function imageModelLabel(model: ModelInfo): string {
  const label = model.display_name || model.label || model.model || model.name;
  const source = model.source === "cloud" ? t("chat.cloud") : model.source?.startsWith("provider:") ? model.provider || t("chat.provider") : t("chat.local");
  return `${label} [${source}]`;
}

async function refreshImageModels() {
  const allModels = await getTags({ refresh: true });
  const imageModels = allModels.filter((model) => model.pipeline_tag === "text-to-image");
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
    const onProvidersChanged = () => {
      refreshImageModels().catch((err) => (error.value = err.message || String(err)));
    };
    window.addEventListener(providersChangedEvent, onProvidersChanged);
    return () => {
      cancelled = true;
      window.removeEventListener(providersChangedEvent, onProvidersChanged);
      if (activeJobID.value) {
        cancelImageGenerationJob(activeJobID.value).catch(() => {});
      }
    };
  }, []);

  useEffect(() => {
    if (!loading.value || !generationStartedAt.value) return;
    const timer = window.setInterval(() => {
      elapsedSeconds.value = Math.max(0, Math.floor((Date.now() - generationStartedAt.value) / 1000));
    }, 1000);
    return () => window.clearInterval(timer);
  }, [loading.value, generationStartedAt.value]);

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
    const requestSize = currentSize();
    if (!requestSize) {
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
    try {
      const job = await createImageGenerationJob({
        model: currentModel.model || currentModel.name,
        source: currentModel.source,
        prompt: prompt.value,
        negative_prompt: negativePrompt.value.trim() || undefined,
        size: requestSize,
        steps: requestSteps,
        seed: requestSeed,
        cfg_scale: requestCFGScale,
      });
      activeJobID.value = job.id;
      let latest = job;
      while (loading.value) {
        if (latest.status === "succeeded") {
          const images = (latest.result?.data || []).map((item) => item.b64_json || "").filter(Boolean);
          if (images.length > 0) {
            history.value = [{
              id: job.id,
              createdAt: Date.now(),
              images,
              prompt: prompt.value,
              negativePrompt: negativePrompt.value.trim(),
              size: requestSize,
              steps: requestSteps,
              seed: requestSeed,
              cfgScale: requestCFGScale,
            }, ...history.value].slice(0, 24);
          }
          break;
        }
        if (latest.status === "failed") {
          throw new Error(latest.error || t("image.failed"));
        }
        if (latest.status === "cancelled") {
          throw new Error(t("image.cancelled"));
        }
        jobStatus.value = latest.status === "queued" ? t("image.jobQueued") : t("image.jobRunning");
        await sleep(1500);
        latest = await getImageGenerationJob(job.id);
      }
      await refreshRuntime();
    } catch (err: any) {
      error.value = err.message || String(err);
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
    loading.value = false;
    jobStatus.value = "";
    generationStartedAt.value = 0;
  };

  const rt = runtime.value;
  const currentModel = selectedModelInfo();
  const selectedModelIsCloud = currentModel?.source === "cloud";
  const selectedSizeKey = sizePresets.find((preset) => preset.width === width.value && preset.height === height.value)?.key || "";
  const progress = progressPercent();

  return (
    <div class="mx-auto max-w-6xl space-y-6 p-8">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{t("image.title")}</h1>
        <p class="mt-2 text-sm text-gray-500">{t("image.subtitle")}</p>
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

      {error.value && <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error.value}</div>}

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
            class="rounded-lg border border-gray-200 bg-gray-50 p-3"
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

          <div class="space-y-3 rounded-xl border border-gray-200 bg-gray-50 p-3">
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

          <div class="space-y-3">
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

            <NumberField
              label={t("image.steps")}
              value={steps.value}
              min={minSteps}
              max={maxSteps}
              onInput={(v) => (steps.value = v)}
              hint={t("image.stepsHint")}
            />
            <div>
              <NumberField label={t("image.seed")} value={seed.value} onInput={(v) => (seed.value = v)} hint={t("image.seedHint")} />
              <button type="button" onClick={randomizeSeed} class="mt-2 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50">
                {t("image.randomSeed")}
              </button>
            </div>
            <NumberField
              label={t("image.cfgScale")}
              value={cfgScale.value}
              min={minCFGScale}
              max={maxCFGScale}
              step="0.5"
              onInput={(v) => (cfgScale.value = v)}
              hint={t("image.cfgHint")}
            />
          </div>
          <div class="space-y-2">
            <button
              onClick={runGenerate}
              disabled={loading.value || !selectedModel.value || !prompt.value.trim()}
              class="w-full rounded-lg bg-indigo-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-indigo-700 disabled:opacity-50"
            >
              {loading.value ? (jobStatus.value || t("image.generating")) : t("image.generate")}
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

        <section class="min-h-[520px] rounded-xl border border-gray-200 bg-white p-5">
          <div class="mb-4 flex items-center justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-gray-900">{t("image.results")}</h2>
              <p class="text-xs text-gray-500">{t("image.resultsHint")}</p>
            </div>
            {history.value.length > 0 && (
              <button type="button" onClick={() => (history.value = [])} class="rounded-lg border border-gray-200 px-3 py-1.5 text-xs text-gray-600 hover:bg-gray-50">
                {t("image.clearHistory")}
              </button>
            )}
          </div>

          {loading.value && (
            <div class="mb-4 rounded-xl border border-indigo-100 bg-indigo-50 p-4">
              <div class="flex items-center justify-between text-sm">
                <span class="font-medium text-indigo-900">{jobStatus.value || t("image.generating")}</span>
                <span class="text-indigo-700">{t("image.elapsed", formatClock(elapsedSeconds.value))}</span>
              </div>
              <div class="mt-3 h-2 overflow-hidden rounded-full bg-indigo-100">
                <div class="h-full rounded-full bg-indigo-600 transition-all duration-500" style={{ width: `${progress}%` }} />
              </div>
              <p class="mt-2 text-xs text-indigo-700">{t("image.progressHint")}</p>
            </div>
          )}

          {history.value.length === 0 ? (
            <div class="flex h-full min-h-[420px] items-center justify-center rounded-xl border border-dashed border-gray-200 px-6 text-center text-sm text-gray-400">
              {t("image.empty")}
            </div>
          ) : (
            <div class="space-y-5">
              {history.value.map((item) => (
                <article key={item.id} class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm">
                  <div class="space-y-3 border-b border-gray-100 bg-gray-50/60 p-4">
                    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                      <div class="min-w-0 space-y-1">
                        <p class="line-clamp-2 text-sm font-semibold leading-6 text-gray-900">{item.prompt}</p>
                        {item.negativePrompt && (
                          <p class="line-clamp-1 text-xs leading-5 text-gray-500">
                            <span class="font-medium text-gray-600">{t("image.negativeLabel")}:</span> {item.negativePrompt}
                          </p>
                        )}
                      </div>
                      <time class="shrink-0 text-left text-xs text-gray-400 sm:text-right" dateTime={new Date(item.createdAt).toISOString()}>
                        {new Date(item.createdAt).toLocaleString()}
                      </time>
                    </div>
                    <div class="flex flex-wrap gap-2 text-xs text-gray-600">
                      <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{item.size}</span>
                      <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.steps")}: {item.steps || "-"}</span>
                      <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.cfgScale")}: {item.cfgScale || "-"}</span>
                      <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.seed")}: {item.seed ?? t("image.random")}</span>
                    </div>
                  </div>
                  <div class={`p-4 ${historyGridClass(item.images.length)}`}>
                    {item.images.map((img, i) => (
                      <div key={`${item.id}-${i}`} class="overflow-hidden rounded-xl border border-gray-200 bg-gray-50">
                        <button
                          type="button"
                          onClick={() => (previewImage.value = { src: imageDataURL(img), title: item.prompt })}
                          class={`block w-full overflow-hidden bg-gray-100 ${historyImageClass(item.size)}`}
                        >
                          <img src={imageDataURL(img)} alt={item.prompt} class="h-full w-full object-contain transition-opacity duration-200 hover:opacity-95" />
                        </button>
                        <div class="flex items-center justify-between gap-3 bg-white px-3 py-2">
                          <span class="truncate text-xs text-gray-400">{item.images.length > 1 ? `${i + 1}/${item.images.length}` : item.size}</span>
                          <button
                            type="button"
                            onClick={() => downloadImage(img, item, i)}
                            class="rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50"
                          >
                            {t("image.download")}
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                </article>
              ))}
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


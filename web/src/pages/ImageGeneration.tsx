import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import {
  cancelImageGenerationJob,
  createImageGenerationJob,
  getImageGenerationJob,
  getImageRuntimeStatus,
  installImageRuntime,
  searchLocalModels,
} from "../api/client";
import type { ImageRuntimeStatus, ModelInfo } from "../api/client";
import { locale, t } from "../i18n";

const models = signal<ModelInfo[]>([]);
const selectedModel = signal("");
const prompt = signal("");
const negativePrompt = signal("");
const size = signal("1024x1024");
const steps = signal("20");
const seed = signal("");
const cfgScale = signal("");
const loading = signal(false);
const installing = signal(false);
const error = signal("");
const runtime = signal<ImageRuntimeStatus | null>(null);
const images = signal<string[]>([]);
const activeJobID = signal("");
const jobStatus = signal("");

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => window.setTimeout(resolve, ms));
}

function optionalNumber(value: string): number | undefined {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : undefined;
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
    searchLocalModels({ pipeline_tag: "text-to-image", limit: 100 })
      .then((resp) => {
        if (cancelled) return;
        models.value = resp.models || [];
        if (!selectedModel.value && resp.models?.[0]) {
          selectedModel.value = resp.models[0].model || resp.models[0].name;
        }
      })
      .catch((err) => (error.value = err.message || String(err)));
    refreshRuntime();
    return () => {
      cancelled = true;
      if (activeJobID.value) {
        cancelImageGenerationJob(activeJobID.value).catch(() => {});
      }
    };
  }, []);

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
    loading.value = true;
    error.value = "";
    jobStatus.value = t("image.jobQueued");
    try {
      const job = await createImageGenerationJob({
        model: selectedModel.value,
        prompt: prompt.value,
        negative_prompt: negativePrompt.value.trim() || undefined,
        size: size.value,
        steps: optionalNumber(steps.value),
        seed: optionalNumber(seed.value),
        cfg_scale: optionalNumber(cfgScale.value),
      });
      activeJobID.value = job.id;
      let latest = job;
      while (loading.value) {
        if (latest.status === "succeeded") {
          images.value = (latest.result?.data || []).map((item) => item.b64_json || "").filter(Boolean);
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
  };

  const rt = runtime.value;

  return (
    <div class="mx-auto max-w-6xl space-y-6 p-8">
      <div>
        <h1 class="text-2xl font-bold text-gray-900">{t("image.title")}</h1>
        <p class="mt-2 text-sm text-gray-500">{t("image.subtitle")}</p>
      </div>

      {rt && !rt.ready && (
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
                <option key={model.model || model.name} value={model.model || model.name}>
                  {model.display_name || model.label || model.model || model.name}
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
            />
          </label>
          <div class="grid grid-cols-2 gap-3">
            <Field label={t("image.size")} value={size.value} onInput={(v) => (size.value = v)} />
            <Field label={t("image.steps")} value={steps.value} onInput={(v) => (steps.value = v)} />
            <Field label={t("image.seed")} value={seed.value} onInput={(v) => (seed.value = v)} />
            <Field label={t("image.cfgScale")} value={cfgScale.value} onInput={(v) => (cfgScale.value = v)} />
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
          {images.value.length === 0 ? (
            <div class="flex h-full min-h-[480px] items-center justify-center rounded-xl border border-dashed border-gray-200 text-sm text-gray-400">
              {t("image.empty")}
            </div>
          ) : (
            <div class="grid gap-4 md:grid-cols-2">
              {images.value.map((img, i) => (
                <img key={i} src={`data:image/png;base64,${img}`} class="w-full rounded-xl border border-gray-200 object-contain" />
              ))}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function Field({ label, value, onInput }: { label: string; value: string; onInput: (value: string) => void }) {
  return (
    <label class="block">
      <span class="text-sm font-medium text-gray-700">{label}</span>
      <input class="mt-1 w-full rounded-lg border border-gray-200 px-3 py-2 text-sm" value={value} onInput={(e) => onInput((e.target as HTMLInputElement).value)} />
    </label>
  );
}


import { useEffect } from "preact/hooks";
import { signal } from "@preact/signals";
import { listImageGenerationJobs } from "../api/client";
import type { ImageGenerationJobResponse } from "../api/client";
import { locale, t } from "../i18n";

const jobs = signal<ImageGenerationJobResponse[]>([]);
const error = signal("");
const loading = signal(false);
const preview = signal<{ src: string; title: string } | null>(null);

function imageDataURL(image: string): string {
  if (/^(https?:|blob:)/i.test(image)) return image;
  return image.startsWith("data:") ? image : `data:image/png;base64,${image}`;
}

function jobImages(job: ImageGenerationJobResponse): string[] {
  return (job.result?.data || [])
    .map((item) => item.b64_json || item.url || "")
    .filter(Boolean);
}

function jobSize(job: ImageGenerationJobResponse): string {
  if (job.request.image || (job.request.images || []).length > 0) return t("image.sizeFollowsInput");
  return job.request.size || t("image.sizeFollowsInput");
}

function statusLabel(job: ImageGenerationJobResponse): string {
  if (job.status === "succeeded") return t("image.succeeded");
  if (job.status === "failed") return t("image.failed");
  if (job.status === "cancelled") return t("image.cancelled");
  if (job.status === "queued") return t("image.jobQueued");
  if (job.status === "running") return t("image.jobRunning");
  return job.status;
}

function historyImageClass(size: string): string {
  const [w, h] = size.split("x").map((part) => Number(part));
  if (!Number.isFinite(w) || !Number.isFinite(h) || w <= 0 || h <= 0) return "aspect-square";
  if (h > w) return "aspect-[4/5]";
  if (w > h) return "aspect-[16/9]";
  return "aspect-square";
}

function downloadImage(image: string, job: ImageGenerationJobResponse, index: number) {
  const a = document.createElement("a");
  a.href = imageDataURL(image);
  a.download = `csghub-image-${job.id}-${index + 1}.png`;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

async function refreshJobs() {
  loading.value = true;
  error.value = "";
  try {
    const list = await listImageGenerationJobs();
    jobs.value = (list.jobs || []).filter((job) => job.status !== "queued" && job.status !== "running");
  } catch (err: any) {
    error.value = err?.message || String(err);
  } finally {
    loading.value = false;
  }
}

export function ImageHistory() {
  void locale.value;

  useEffect(() => {
    refreshJobs();
  }, []);

  return (
    <div class="mx-auto max-w-6xl space-y-6 p-8">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <a href="/images" class="text-sm font-medium text-indigo-600 hover:text-indigo-700">{t("image.backToImages")}</a>
          <h1 class="mt-2 text-2xl font-bold text-gray-900">{t("image.history")}</h1>
          <p class="mt-2 text-sm text-gray-500">{t("image.historyHint")}</p>
        </div>
        <button
          type="button"
          onClick={() => refreshJobs()}
          disabled={loading.value}
          class="rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 hover:bg-gray-50 disabled:opacity-50"
        >
          {loading.value ? t("image.refreshingJobs") : t("image.refreshJobs")}
        </button>
      </div>

      {error.value && <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error.value}</div>}

      {jobs.value.length === 0 ? (
        <div class="flex min-h-[420px] items-center justify-center rounded-xl border border-dashed border-gray-200 bg-white px-6 text-center text-sm text-gray-400">
          {t("image.emptyHistory")}
        </div>
      ) : (
        <div class="grid gap-5 xl:grid-cols-2">
          {jobs.value.map((job) => {
            const images = jobImages(job);
            const size = jobSize(job);
            return (
              <article key={job.id} class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm">
                <div class="space-y-3 border-b border-gray-100 bg-gray-50/60 p-4">
                  <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                    <div class="min-w-0 space-y-1">
                      <div class="flex flex-wrap items-center gap-2">
                        <span class="rounded-full bg-white px-2.5 py-1 text-xs text-gray-600 ring-1 ring-gray-200">{statusLabel(job)}</span>
                        <span class="truncate text-xs text-gray-400">{job.request.model}</span>
                      </div>
                      <p class="line-clamp-2 text-sm font-semibold leading-6 text-gray-900">{job.request.prompt}</p>
                    </div>
                    <time class="shrink-0 text-left text-xs text-gray-400 sm:text-right" dateTime={job.created_at}>
                      {new Date(job.created_at).toLocaleString()}
                    </time>
                  </div>
                  <div class="flex flex-wrap gap-2 text-xs text-gray-600">
                    <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{size}</span>
                    <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.steps")}: {job.request.steps || "-"}</span>
                    <span class="rounded-full bg-white px-2.5 py-1 ring-1 ring-gray-200">{t("image.seed")}: {job.request.seed ?? t("image.random")}</span>
                  </div>
                  {job.error && <p class="rounded-lg border border-red-100 bg-red-50 px-3 py-2 text-xs text-red-700">{job.error}</p>}
                </div>
                {images.length > 0 && (
                  <div class={`p-4 ${images.length === 1 ? "grid gap-4" : "grid gap-4 md:grid-cols-2"}`}>
                    {images.map((img, i) => (
                      <div key={`${job.id}-${i}`} class="overflow-hidden rounded-xl border border-gray-200 bg-gray-50">
                        <button
                          type="button"
                          onClick={() => (preview.value = { src: imageDataURL(img), title: job.request.prompt })}
                          class={`block w-full overflow-hidden bg-gray-100 ${historyImageClass(size)}`}
                        >
                          <img src={imageDataURL(img)} alt={job.request.prompt} class="h-full w-full object-contain transition-opacity duration-200 hover:opacity-95" />
                        </button>
                        <div class="flex items-center justify-between gap-3 bg-white px-3 py-2">
                          <span class="truncate text-xs text-gray-400">{images.length > 1 ? `${i + 1}/${images.length}` : size}</span>
                          <button
                            type="button"
                            onClick={() => downloadImage(img, job, i)}
                            class="rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-50"
                          >
                            {t("image.download")}
                          </button>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}

      {preview.value && (
        <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-6" onClick={() => (preview.value = null)}>
          <div class="max-h-full max-w-5xl rounded-xl bg-white p-4 shadow-xl" onClick={(e) => e.stopPropagation()}>
            <div class="mb-3 flex items-center justify-between gap-4">
              <p class="truncate text-sm font-medium text-gray-700">{preview.value.title}</p>
              <button type="button" onClick={() => (preview.value = null)} class="rounded-lg border border-gray-200 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-50">
                {t("image.closePreview")}
              </button>
            </div>
            <img src={preview.value.src} class="max-h-[78vh] max-w-full rounded-lg object-contain" />
          </div>
        </div>
      )}
    </div>
  );
}

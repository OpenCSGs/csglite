import { useEffect, useState } from "preact/hooks";
import { getMarketplaceModelDetail } from "../api/client";
import type {
  ArtifactSource,
  MarketplaceModel,
  MarketplaceModelDetailResponse,
  MarketplaceTag,
} from "../api/client";
import type { DownloadTask } from "../downloads";
import { locale, t } from "../i18n";
import { LocalInferenceBadge } from "./LocalInferenceBadge";
import {
  localInferenceLabelKey,
  localInferenceModeFromSupport,
  localInferenceValueKey,
} from "../utils/localInference";

type MarketplaceModelDetailDialogProps = {
  modelPath: string;
  artifactSource?: ArtifactSource;
  revision?: string;
  isLocal?: boolean;
  pulling?: DownloadTask;
  onDownload?: (modelPath: string) => void;
  onClose: () => void;
};

function artifactSourceLabel(source?: ArtifactSource): string {
  switch (source) {
    case "huggingface":
      return t("mp.sourceHuggingFace");
    case "modelscope":
      return t("mp.sourceModelScope");
    default:
      return t("mp.sourceOpenCSG");
  }
}

export function MarketplaceModelDetailDialog({
  modelPath,
  artifactSource,
  revision,
  isLocal,
  pulling,
  onDownload,
  onClose,
}: MarketplaceModelDetailDialogProps) {
  void locale.value;

  const [detail, setDetail] = useState<MarketplaceModelDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    setDetail(null);

    getMarketplaceModelDetail(modelPath, { artifactSource, revision })
      .then((data) => {
        if (!cancelled) {
          setDetail(data);
        }
      })
      .catch((err: any) => {
        if (!cancelled) {
          setError(err?.message || t("mp.failedLoadDetail"));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [modelPath, artifactSource, revision]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };

    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, [onClose]);

  const model = detail?.details || null;
  const formatTags = model ? getModelFormatTags(model.tags || []) : [];
  const localInferenceMode = localInferenceModeFromSupport(detail?.local_inference);
  const taskTags = (model?.tags || []).filter((tag) => tag.category === "task");
  const runtimeTags = (model?.tags || []).filter((tag) => tag.category === "runtime_framework");
  const quantizations = detail?.quantizations || [];
  const downloaded = Boolean(isLocal || detail?.local_model?.downloaded);

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4 py-6"
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <div class="w-full max-w-4xl max-h-[88vh] overflow-hidden rounded-2xl bg-white shadow-2xl flex flex-col">
        <div class="flex items-start justify-between gap-4 px-6 py-5 border-b border-gray-100">
          <div class="min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <h2 class="text-xl font-bold text-gray-900 break-all">
                {model?.artifact_source === "modelscope" && model.nickname ? model.nickname : modelPath}
              </h2>
              {formatTags.map((tag) => (
                <span
                  key={`format:${tag.name}`}
                  class={`inline-flex items-center rounded-full px-2.5 py-1 text-xs font-medium ${formatBadgeTone(tag)}`}
                >
                  {formatBadgeLabel(tag)}
                </span>
              ))}
              {downloaded && (
                <span class="inline-flex items-center rounded-full bg-indigo-50 px-2.5 py-1 text-xs font-medium text-indigo-600">
                  {t("mp.downloaded")}
                </span>
              )}
              {!loading && model && localInferenceMode !== "none" && (
                <LocalInferenceBadge mode={localInferenceMode} prefix="mp" />
              )}
            </div>
            <p class="mt-1 text-sm text-gray-500">
              {model?.artifact_source === "modelscope" && model.nickname ? modelPath : t("mp.detailSubtitle")}
            </p>
          </div>
          <div class="flex items-center gap-3 flex-shrink-0">
            <DetailDownloadAction
              modelPath={modelPath}
              isLocal={downloaded}
              pulling={pulling}
              onDownload={onDownload}
            />
            <button
              onClick={onClose}
              class="inline-flex h-9 w-9 items-center justify-center rounded-full text-gray-500 hover:bg-gray-100 hover:text-gray-700"
              aria-label={t("dash.close")}
              title={t("dash.close")}
            >
              <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        </div>

        <div class="flex-1 overflow-auto px-6 py-5">
          {loading ? (
            <div class="rounded-xl border border-gray-200 px-6 py-16 text-center text-gray-400">
              {t("mp.loadingDetail")}
            </div>
          ) : error ? (
            <div class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 whitespace-pre-wrap">
              {error}
            </div>
          ) : model ? (
            <div class="space-y-6">
              <ProviderModelDetails
                model={model}
                artifactSource={model.artifact_source || artifactSource}
                revision={revision}
                formatTags={formatTags}
                localInferenceMode={localInferenceMode}
                taskTags={taskTags}
                runtimeTags={runtimeTags}
              />

              {quantizations.length > 0 && (
                <section class="rounded-xl border border-gray-200 bg-white p-5 space-y-3">
                  <div>
                    <h3 class="text-sm font-semibold text-gray-900">{t("mp.quantizations")}</h3>
                    <p class="mt-1 text-sm text-gray-500">{t("mp.quantizationsHint")}</p>
                  </div>
                  <div class="flex flex-wrap gap-2">
                    {quantizations.map((item) => (
                      <span
                        key={item.name}
                        title={item.example_path}
                        class="inline-flex items-center gap-2 rounded-full border border-blue-100 bg-blue-50 px-3 py-1.5 text-sm font-medium text-blue-700"
                      >
                        <span>{item.name}</span>
                        {item.file_count > 1 && <span class="text-xs text-blue-500">x{item.file_count}</span>}
                      </span>
                    ))}
                  </div>
                </section>
              )}
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function ProviderModelDetails({
  model,
  artifactSource,
  revision,
  formatTags,
  localInferenceMode,
  taskTags,
  runtimeTags,
}: {
  model: MarketplaceModel;
  artifactSource?: ArtifactSource;
  revision?: string;
  formatTags: MarketplaceTag[];
  localInferenceMode: ReturnType<typeof localInferenceModeFromSupport>;
  taskTags: MarketplaceTag[];
  runtimeTags: MarketplaceTag[];
}) {
  const source = model.artifact_source || artifactSource || "opencsg";
  const format = formatTags.length > 0 ? formatTags.map(formatBadgeLabel).join(" / ") : t("lib.notAvailable");
  const huggingFace = model.provider?.huggingface;
  const modelScope = model.provider?.modelscope;
  const task = huggingFace?.pipeline_tag || modelScope?.tasks?.join(" / ") ||
    taskTags.map(displayTagName).join(" / ") || t("lib.notAvailable");
  const runtime = huggingFace?.library_name || modelScope?.libraries?.join(" / ") ||
    runtimeTags.map(displayTagName).join(" / ") || t("lib.notAvailable");
  const commonTechnical = (
    <>
      <SummaryTile label={t("mp.modelParams")} value={formatModelParams(model.metadata?.model_params)} />
      <SummaryTile label={t("mp.repoSize")} value={formatRepoSize(model.repo_size)} />
      <SummaryTile label={t("mp.architecture")} value={model.metadata?.architecture || model.metadata?.class_name || t("lib.notAvailable")} />
      <SummaryTile label={t("mp.tensorType")} value={model.metadata?.tensor_type || t("lib.notAvailable")} />
    </>
  );

  if (source === "huggingface") {
    return (
      <>
        <section class="overflow-hidden rounded-xl border border-gray-200 bg-white">
          <div class="flex items-center gap-2 border-b border-gray-100 px-5 py-3 text-sm font-semibold text-gray-900">
            <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-bold text-gray-600">{t("mp.sourceHuggingFaceShort")}</span>
            {t("mp.sourceHuggingFace")}
          </div>
          <div class="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
            <SummaryTile label={t("mp.author")} value={huggingFace?.author || model.path.split("/")[0] || t("lib.notAvailable")} />
            <SummaryTile label={t("mp.tasks")} value={task} />
            <SummaryTile label={t("mp.library")} value={runtime} />
            <SummaryTile label={t("mp.format")} value={format} />
            <SummaryTile label={t("mp.downloads")} value={formatCount(model.downloads)} />
            <SummaryTile label={t("mp.likes")} value={formatCount(model.likes)} />
            <SummaryTile label={t("mp.commit")} value={model.revision || revision || t("mp.defaultRevision")} />
            <SummaryTile label={t("mp.languages")} value={huggingFace?.languages?.join(", ") || t("lib.notAvailable")} />
            <SummaryTile label={t("mp.baseModel")} value={huggingFace?.base_models?.join(" / ") || t("lib.notAvailable")} />
            {huggingFace?.gated && <SummaryTile label={t("mp.access")} value={t("mp.gatedAccess")} />}
          </div>
        </section>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {commonTechnical}
          <SummaryTile label={t("mp.updated")} value={formatDate(model.updated_at)} />
          <SummaryTile
            label={t(localInferenceLabelKey("mp"))}
            value={t(localInferenceValueKey(localInferenceMode, "mp"))}
            tone={localInferenceMode === "none" ? "danger" : "default"}
          />
        </div>
        <ModelDescription model={model} />
        {taskTags.length > 0 && <TagSection title={t("mp.tasks")} tags={taskTags} tone="bg-gray-100 text-gray-700" />}
      </>
    );
  }

  if (source === "modelscope") {
    return (
      <>
        <section class="overflow-hidden rounded-xl border border-gray-200 bg-white">
          <div class="flex items-center gap-2 border-b border-gray-100 px-5 py-3 text-sm font-semibold text-gray-900">
            <span class="rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-bold text-gray-600">{t("mp.sourceModelScopeShort")}</span>
            {t("mp.sourceModelScope")}
          </div>
          <div class="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
            <SummaryTile label={t("mp.modelId")} value={model.path} />
            <SummaryTile label={t("mp.tasks")} value={task} />
            <SummaryTile label={t("mp.library")} value={runtime} />
            <SummaryTile label={t("mp.modelType")} value={modelScope?.model_type || model.metadata?.model_type || t("lib.notAvailable")} />
            <SummaryTile label={t("mp.downloads")} value={formatCount(model.downloads)} />
            <SummaryTile label={t("mp.likes")} value={formatCount(model.likes)} />
            <SummaryTile label={t("lib.licenseLabel")} value={model.license || t("lib.notAvailable")} />
            {modelScope?.gated && <SummaryTile label={t("mp.access")} value={t("mp.gatedAccess")} />}
          </div>
        </section>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
          {commonTechnical}
          <SummaryTile label={t("mp.format")} value={format} />
          <SummaryTile label={t("mp.updated")} value={formatDate(model.updated_at)} />
          <SummaryTile
            label={t(localInferenceLabelKey("mp"))}
            value={t(localInferenceValueKey(localInferenceMode, "mp"))}
            tone={localInferenceMode === "none" ? "danger" : "default"}
          />
        </div>
        <ModelDescription model={model} />
        {runtimeTags.length > 0 && <TagSection title={t("mp.runtimeFrameworks")} tags={runtimeTags} tone="bg-gray-100 text-gray-700" />}
        {taskTags.length > 0 && <TagSection title={t("mp.tasks")} tags={taskTags} tone="bg-gray-100 text-gray-700" />}
      </>
    );
  }

  return (
    <>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <SummaryTile label={t("mp.format")} value={format} />
        <SummaryTile label={t("mp.artifactSource")} value={artifactSourceLabel(source)} />
        <SummaryTile label={t("mp.revision")} value={model.revision || revision || t("mp.defaultRevision")} />
        <SummaryTile
          label={t(localInferenceLabelKey("mp"))}
          value={t(localInferenceValueKey(localInferenceMode, "mp"))}
          tone={localInferenceMode === "none" ? "danger" : "default"}
        />
        {commonTechnical}
        <SummaryTile label={t("mp.downloads")} value={formatCount(model.downloads)} />
        <SummaryTile label={t("mp.updated")} value={formatDate(model.updated_at)} />
      </div>
      <ModelDescription model={model} />
      {runtimeTags.length > 0 && <TagSection title={t("mp.runtimeFrameworks")} tags={runtimeTags} tone="bg-indigo-50 text-indigo-700" />}
      {taskTags.length > 0 && <TagSection title={t("mp.tasks")} tags={taskTags} tone="bg-gray-100 text-gray-700" />}
    </>
  );
}

function ModelDescription({ model }: { model: MarketplaceModel }) {
  if (!model.description && !model.license) return null;
  return (
    <section class="rounded-xl border border-gray-200 bg-white p-5 space-y-3">
      {model.description && (
        <div>
          <h3 class="mb-1 text-sm font-semibold text-gray-900">{t("lib.description")}</h3>
          <p class="text-sm text-gray-600 whitespace-pre-wrap">{model.description}</p>
        </div>
      )}
      {model.license && (
        <div class="text-sm text-gray-600">
          <span class="mr-2 font-semibold text-gray-900">{t("lib.licenseLabel")}</span>
          <span>{model.license}</span>
        </div>
      )}
    </section>
  );
}

function DetailDownloadAction({
  modelPath,
  isLocal,
  pulling,
  onDownload,
}: {
  modelPath: string;
  isLocal?: boolean;
  pulling?: DownloadTask;
  onDownload?: (modelPath: string) => void;
}) {
  if ((isLocal || pulling?.status === "success") && !pulling?.status?.startsWith("downloading")) {
    return (
      <span class="inline-flex items-center gap-1.5 rounded-lg bg-indigo-50 px-3 py-2 text-sm font-medium text-indigo-600">
        <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
        </svg>
        {t("mp.downloaded")}
      </span>
    );
  }

  if (pulling) {
    if (pulling.status === "error") {
      return (
        <button
          type="button"
          onClick={() => onDownload?.(modelPath)}
          class="inline-flex items-center gap-1.5 rounded-lg border border-red-200 px-3 py-2 text-sm font-medium text-red-600 hover:bg-red-50"
          title={pulling.error || pulling.statusText}
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
          {t("mp.failed")}
        </button>
      );
    }

    return (
      <div class="min-w-[120px]">
        <div class="mb-1 text-xs font-medium text-indigo-600">
          {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
        </div>
        <div class="h-1.5 overflow-hidden rounded-full bg-gray-200">
          <div
            class="h-full rounded-full bg-indigo-500 transition-all duration-300"
            style={{ width: `${Math.max(pulling.percent, 3)}%` }}
          />
        </div>
      </div>
    );
  }

  return (
    <button
      type="button"
      onClick={() => onDownload?.(modelPath)}
      class="inline-flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-2 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-50"
    >
      <DownloadIcon /> {t("mp.download")}
    </button>
  );
}

function DownloadIcon() {
  return (
    <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 4v12m0 0l-4-4m4 4l4-4M4 20h16" />
    </svg>
  );
}

function TagSection({
  title,
  tags,
  tone,
}: {
  title: string;
  tags: MarketplaceTag[];
  tone: string;
}) {
  return (
    <section class="rounded-xl border border-gray-200 bg-white p-5 space-y-3">
      <h3 class="text-sm font-semibold text-gray-900">{title}</h3>
      <div class="flex flex-wrap gap-2">
        {tags.map((tag) => (
          <span key={`${tag.category}:${tag.name}`} class={`inline-flex items-center rounded-full px-3 py-1.5 text-sm font-medium ${tone}`}>
            {displayTagName(tag)}
          </span>
        ))}
      </div>
    </section>
  );
}

function SummaryTile({ label, value, tone = "default" }: { label: string; value: string; tone?: "default" | "danger" }) {
  const danger = tone === "danger";
  return (
    <div class={`rounded-xl border px-4 py-3 ${danger ? "border-red-200 bg-red-50" : "border-gray-200 bg-white"}`}>
      <div class={`text-xs font-medium uppercase tracking-wide ${danger ? "text-red-500" : "text-gray-400"}`}>{label}</div>
      <div class={`mt-1 text-sm font-semibold break-words ${danger ? "text-red-700" : "text-gray-900"}`}>{value}</div>
    </div>
  );
}

function getModelFormatTags(tags: MarketplaceTag[]): MarketplaceTag[] {
  const byName = new Map<string, MarketplaceTag>();
  for (const tag of tags) {
    if (tag.category !== "framework") continue;
    const name = tag.name.trim().toLowerCase();
    if (name === "gguf" || name === "safetensors") {
      if (!byName.has(name)) {
        byName.set(name, tag);
      }
    }
  }

  return ["gguf", "safetensors"]
    .map((name) => byName.get(name))
    .filter((tag): tag is MarketplaceTag => Boolean(tag));
}

function formatBadgeLabel(tag: MarketplaceTag): string {
  const showName = tag.show_name?.trim();
  if (showName) {
    return showName;
  }
  const name = tag.name.trim().toLowerCase();
  if (name === "safetensors") return "SafeTensors";
  if (name === "gguf") return "GGUF";
  return tag.name;
}

function formatBadgeTone(tag: MarketplaceTag): string {
  const name = tag.name.trim().toLowerCase();
  if (name === "gguf") return "bg-blue-50 text-blue-700";
  if (name === "safetensors") return "bg-emerald-50 text-emerald-700";
  return "bg-gray-100 text-gray-700";
}

function displayTagName(tag: MarketplaceTag): string {
  return tag.show_name?.trim() || tag.name;
}

function formatModelParams(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) {
    return t("lib.notAvailable");
  }
  if (value >= 1000) {
    return `${trimFloat(value / 1000)}T`;
  }
  return `${trimFloat(value)}B`;
}

function formatRepoSize(bytes?: number): string {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes <= 0) {
    return t("lib.notAvailable");
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** unitIndex;
  return `${value.toFixed(value >= 100 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function trimFloat(value: number): string {
  return value.toFixed(value >= 100 ? 0 : value >= 10 ? 1 : 2).replace(/\.?0+$/, "");
}

function formatCount(value?: number): string {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return t("lib.notAvailable");
  }
  if (value < 1000) {
    return String(Math.max(0, Math.floor(value)));
  }
  return `${(value / 1000).toFixed(1).replace(/\.0$/, "")}k`;
}

function formatDate(value?: string): string {
  if (!value) {
    return t("lib.notAvailable");
  }
  const language = locale.value === "zh" ? "zh-CN" : "en-US";
  return new Date(value).toLocaleDateString(language, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

export function getMarketplaceModelFormats(model: MarketplaceModel): MarketplaceTag[] {
  return getModelFormatTags(model.tags || []);
}

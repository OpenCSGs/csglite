import type { ComponentChildren } from "preact";
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
              {model ? artifactSourceLabel(model.artifact_source || artifactSource) : t("mp.detailSubtitle")}
              {model?.artifact_source === "modelscope" && model.nickname ? ` · ${modelPath}` : ""}
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
            <div class="px-6 py-16 text-center text-sm text-gray-400">
              {t("mp.loadingDetail")}
            </div>
          ) : error ? (
            <div class="rounded-lg bg-red-50 px-4 py-3 text-sm text-red-700 whitespace-pre-wrap">
              {error}
            </div>
          ) : model ? (
            <div class="space-y-8">
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
                <ChipGroup title={t("mp.quantizations")} hint={t("mp.quantizationsHint")}>
                  {quantizations.map((item) => (
                    <span
                      key={item.name}
                      title={item.example_path}
                      class="inline-flex items-center gap-2 rounded-full bg-blue-50 px-3 py-1 text-sm font-medium text-blue-700"
                    >
                      <span>{item.name}</span>
                      {item.file_count > 1 && <span class="text-xs text-blue-500">x{item.file_count}</span>}
                    </span>
                  ))}
                </ChipGroup>
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
  const format = formatTags.length > 0 ? formatTags.map(formatBadgeLabel).join(" / ") : "";
  const huggingFace = model.provider?.huggingface;
  const modelScope = model.provider?.modelscope;
  const unavailable = t("lib.notAvailable");
  const task = huggingFace?.pipeline_tag || modelScope?.tasks?.join(" / ") || taskTags.map(displayTagName).join(" / ");
  const runtime = huggingFace?.library_name || modelScope?.libraries?.join(" / ") || runtimeTags.map(displayTagName).join(" / ");
  const inferenceTone = localInferenceMode === "none" ? "danger" : "default";
  const technical: DetailFact[] = [
    fact(t("mp.modelParams"), formatModelParams(model.metadata?.model_params), unavailable),
    fact(t("mp.repoSize"), formatRepoSize(model.repo_size), unavailable),
    fact(t("mp.architecture"), model.metadata?.architecture || model.metadata?.class_name || "", unavailable),
    fact(t("mp.tensorType"), model.metadata?.tensor_type || "", unavailable),
  ];
  const inference = fact(
    t(localInferenceLabelKey("mp")),
    t(localInferenceValueKey(localInferenceMode, "mp")),
    "",
    inferenceTone,
  );

  let facts: DetailFact[] = [];
  if (source === "huggingface") {
    facts = [
      fact(t("mp.author"), huggingFace?.author || model.path.split("/")[0] || "", unavailable),
      fact(t("mp.tasks"), task, unavailable),
      fact(t("mp.library"), runtime, unavailable),
      fact(t("mp.format"), format, unavailable),
      fact(t("mp.downloads"), formatCount(model.downloads), unavailable),
      fact(t("mp.likes"), formatCount(model.likes), unavailable),
      fact(t("mp.commit"), model.revision || revision || t("mp.defaultRevision"), unavailable),
      fact(t("mp.languages"), huggingFace?.languages?.join(", ") || "", unavailable),
      fact(t("mp.baseModel"), huggingFace?.base_models?.join(" / ") || "", unavailable),
      ...(huggingFace?.gated ? [fact(t("mp.access"), t("mp.gatedAccess"), unavailable)] : []),
      ...technical,
      fact(t("mp.updated"), formatDate(model.updated_at), unavailable),
      inference,
    ];
  } else if (source === "modelscope") {
    facts = [
      fact(t("mp.modelId"), model.path, unavailable),
      fact(t("mp.tasks"), task, unavailable),
      fact(t("mp.library"), runtime, unavailable),
      fact(t("mp.modelType"), modelScope?.model_type || model.metadata?.model_type || "", unavailable),
      fact(t("mp.downloads"), formatCount(model.downloads), unavailable),
      fact(t("mp.likes"), formatCount(model.likes), unavailable),
      fact(t("lib.licenseLabel"), model.license || "", unavailable),
      ...(modelScope?.gated ? [fact(t("mp.access"), t("mp.gatedAccess"), unavailable)] : []),
      ...technical,
      fact(t("mp.format"), format, unavailable),
      fact(t("mp.updated"), formatDate(model.updated_at), unavailable),
      inference,
    ];
  } else {
    facts = [
      fact(t("mp.format"), format, unavailable),
      fact(t("mp.revision"), model.revision || revision || t("mp.defaultRevision"), unavailable),
      inference,
      ...technical,
      fact(t("mp.downloads"), formatCount(model.downloads), unavailable),
      fact(t("mp.updated"), formatDate(model.updated_at), unavailable),
    ];
  }

  return (
    <>
      <DetailFacts items={facts} />
      <ModelDescription model={model} />
      {source === "opencsg" && runtimeTags.length > 0 && (
        <ChipGroup title={t("mp.runtimeFrameworks")}>
          {runtimeTags.map((tag) => (
            <span key={`${tag.category}:${tag.name}`} class="inline-flex items-center rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-700">
              {displayTagName(tag)}
            </span>
          ))}
        </ChipGroup>
      )}
      {source === "opencsg" && taskTags.length > 0 && (
        <ChipGroup title={t("mp.tasks")}>
          {taskTags.map((tag) => (
            <span key={`${tag.category}:${tag.name}`} class="inline-flex items-center rounded-full bg-gray-100 px-3 py-1 text-sm text-gray-700">
              {displayTagName(tag)}
            </span>
          ))}
        </ChipGroup>
      )}
    </>
  );
}

type DetailFact = {
  label: string;
  value: string;
  tone?: "default" | "danger";
};

function fact(label: string, value: string, unavailable: string, tone: "default" | "danger" = "default"): DetailFact {
  const trimmed = value.trim();
  if (!trimmed || trimmed === unavailable) {
    return { label, value: "" };
  }
  return { label, value: trimmed, tone };
}

function DetailFacts({ items }: { items: DetailFact[] }) {
  const visible = items.filter((item) => item.value);
  if (visible.length === 0) return null;
  return (
    <dl class="grid grid-cols-1 gap-x-10 gap-y-5 sm:grid-cols-2 xl:grid-cols-3">
      {visible.map((item) => (
        <div key={item.label}>
          <dt class="text-xs text-gray-400">{item.label}</dt>
          <dd class={`mt-1 text-sm font-medium break-words ${item.tone === "danger" ? "text-red-600" : "text-gray-900"}`}>
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function ChipGroup({ title, hint, children }: { title: string; hint?: string; children: ComponentChildren }) {
  return (
    <div>
      <h3 class="text-xs text-gray-400">{title}</h3>
      {hint && <p class="mt-1 text-sm text-gray-500">{hint}</p>}
      <div class="mt-2 flex flex-wrap gap-2">{children}</div>
    </div>
  );
}

function ModelDescription({ model }: { model: MarketplaceModel }) {
  if (!model.description && !model.license) return null;
  return (
    <div class="space-y-2">
      {model.description && (
        <p class="text-sm leading-6 text-gray-600 whitespace-pre-wrap">{model.description}</p>
      )}
      {model.license && (
        <p class="text-sm text-gray-500">
          <span class="mr-2 text-gray-400">{t("lib.licenseLabel")}</span>
          {model.license}
        </p>
      )}
    </div>
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
          class="inline-flex items-center gap-1.5 rounded-lg bg-red-50 px-3 py-2 text-sm font-medium text-red-600 hover:bg-red-100"
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
      class="inline-flex items-center gap-1.5 rounded-lg bg-gray-900 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-gray-700"
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

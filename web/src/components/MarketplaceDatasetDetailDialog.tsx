import { useEffect, useState } from "preact/hooks";
import { getMarketplaceDatasetDetail, getMarketplaceDatasetExtras } from "../api/client";
import type {
  ArtifactSource,
  MarketplaceDataset,
  MarketplaceDatasetDetailResponse,
} from "../api/client";
import type { DownloadTask } from "../downloads";
import { datasetMarketplaceMetadata } from "../datasetMetadata";
import { locale, t } from "../i18n";
import { ArtifactOwnerAvatar } from "./ArtifactOwnerAvatar";

type Props = {
  datasetPath: string;
  artifactSource: ArtifactSource;
  revision?: string;
  isLocal?: boolean;
  pulling?: DownloadTask;
  onDownload: (dataset: MarketplaceDataset) => void;
  onClose: () => void;
};

export function MarketplaceDatasetDetailDialog(props: Props) {
  void locale.value;
  const [detail, setDetail] = useState<MarketplaceDatasetDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const extrasController = new AbortController();
    setLoading(true);
    setError("");
    getMarketplaceDatasetDetail(props.datasetPath, {
      artifactSource: props.artifactSource,
      revision: props.revision,
    }).then((response) => {
      if (cancelled) return;
      setDetail(response);
      void getMarketplaceDatasetExtras(props.datasetPath, {
        artifactSource: props.artifactSource,
        revision: props.revision,
        signal: extrasController.signal,
      }).then((extras) => {
        if (cancelled || !extras.available) return;
        setDetail((current) => current ? {
          ...current,
          details: {
            ...current.details,
            repo_size: extras.repo_size || current.details.repo_size,
            file_count: extras.file_count,
            revision: extras.revision || current.details.revision,
          },
        } : current);
      }).catch(() => {});
    }).catch((err: any) => {
      if (!cancelled) setError(err?.message || t("mp.failedLoadDatasetDetail"));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => {
      cancelled = true;
      extrasController.abort();
    };
  }, [props.datasetPath, props.artifactSource, props.revision]);

  useEffect(() => {
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") props.onClose();
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, [props.onClose]);

  const dataset = detail?.details;
  const downloaded = Boolean(props.isLocal || detail?.local_dataset.downloaded);
  const provider = dataset?.provider;
  const metadata = dataset ? datasetMarketplaceMetadata(dataset) : undefined;
  const languages = metadata?.languages || [];
  const tasks = metadata?.tasks || [];
  const title = provider?.huggingface?.pretty_name || provider?.modelscope?.display_name || dataset?.nickname || props.datasetPath;
  const revision = dataset?.revision || dataset?.default_branch || props.revision;
  const sourceURL = dataset?.repository?.http_clone_url;

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4 py-6" onClick={(event) => {
      if (event.target === event.currentTarget) props.onClose();
    }}>
      <section class="flex max-h-[88vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl bg-white shadow-2xl">
        <header class="flex items-start justify-between gap-4 border-b border-gray-100 px-6 py-5">
          <div class="flex min-w-0 items-start gap-3">
            <ArtifactOwnerAvatar source={props.artifactSource} path={props.datasetPath} />
            <div class="min-w-0">
              <h2 class="break-all text-xl font-bold text-gray-900">{title}</h2>
              <p class="mt-1 break-all text-sm text-gray-500">{title === props.datasetPath ? t("mp.datasetDetailSubtitle") : props.datasetPath}</p>
            </div>
          </div>
          <div class="flex flex-shrink-0 items-center gap-3">
            {downloaded ? (
              <span class="text-sm font-medium text-indigo-600">{t("mp.downloaded")}</span>
            ) : props.pulling ? (
              <span class="text-sm font-medium text-indigo-600">{props.pulling.percent > 0 ? `${props.pulling.percent}%` : t("mp.pulling")}</span>
            ) : dataset ? (
              <button onClick={() => props.onDownload(dataset)} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700">
                {t("mp.download")}
              </button>
            ) : null}
            {sourceURL && (
              <a href={sourceURL} target="_blank" rel="noreferrer" class="rounded-lg border border-gray-200 px-3 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50">
                {t("mp.openSourcePage")} ↗
              </a>
            )}
            <button onClick={props.onClose} aria-label={t("dash.close")} class="h-9 w-9 rounded-full text-gray-500 hover:bg-gray-100">×</button>
          </div>
        </header>
        <div class="flex-1 overflow-auto px-6 py-5">
          {loading ? (
            <div class="py-16 text-center text-gray-400">{t("mp.loadingDatasetDetail")}</div>
          ) : error ? (
            <div class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700">{error}</div>
          ) : dataset ? (
            <div class="space-y-5">
              {dataset.description && <p class="whitespace-pre-wrap text-sm leading-6 text-gray-600">{dataset.description}</p>}
              <div class="grid grid-cols-2 gap-3 sm:grid-cols-5">
                <Metadata label={t("mp.repoSize")} value={formatBytes(dataset.repo_size)} />
                <Metadata label={t("mp.files")} value={dataset.file_count ? String(dataset.file_count) : undefined} />
                <Metadata label={t("mp.downloads")} value={String(dataset.downloads || 0)} />
                <Metadata label={t("mp.likes")} value={String(dataset.likes || 0)} />
                <Metadata label={t("mp.updated")} value={formatDate(dataset.updated_at)} />
              </div>
              <div class="flex flex-wrap gap-2">
                {metadata?.type && <Pill value={metadata.type} tone="blue" />}
                {metadata?.sizeCategory && <Pill value={metadata.sizeCategory} />}
                {metadata?.formats.map((value) => <Pill key={`format:${value}`} value={value.toUpperCase()} tone="green" />)}
                {metadata?.modalities.map((value) => <Pill key={`modality:${value}`} value={value} />)}
              </div>
              <section class="rounded-xl border border-gray-200 p-5">
                <dl class="grid gap-4 text-sm sm:grid-cols-2">
                  <MetadataRow label={t("mp.datasetId")} value={dataset.path} />
                  <MetadataRow label={t("mp.artifactSource")} value={sourceLabel(props.artifactSource)} />
                  <MetadataRow label={t("ds.licenseLabel")} value={dataset.license} />
                  <MetadataRow label={t("mp.revision")} value={revision} />
                  <MetadataRow label={t("mp.author")} value={provider?.huggingface?.author || dataset.path.split("/")[0]} />
                  <MetadataRow label={t("mp.access")} value={
                    provider?.huggingface?.gated || provider?.modelscope?.gated ? t("mp.gatedAccess") : t("mp.publicAccess")
                  } />
                  <MetadataRow label={t("mp.created")} value={formatDate(dataset.created_at)} />
                  <MetadataRow label={t("mp.commit")} value={provider?.huggingface?.sha || dataset.revision} />
                </dl>
              </section>
              {languages.length > 0 && <TagSection title={t("mp.languages")} values={languages} />}
              {tasks.length > 0 && <TagSection title={t("mp.tasks")} values={tasks} />}
              {(metadata?.libraries.length || 0) > 0 && <TagSection title={t("mp.datasetLibraries")} values={metadata!.libraries} />}
              {(metadata?.topics.length || 0) > 0 && <TagSection title={t("mp.datasetTopics")} values={metadata!.topics.slice(0, 16)} />}
            </div>
          ) : null}
        </div>
      </section>
    </div>
  );
}

function Metadata({ label, value }: { label: string; value?: string }) {
  return <div class="rounded-xl border border-gray-200 p-3"><div class="text-xs text-gray-400">{label}</div><div class="mt-1 break-all text-sm font-semibold text-gray-900">{value || "—"}</div></div>;
}

function MetadataRow({ label, value }: { label: string; value?: string }) {
  return <div><dt class="text-xs font-medium text-gray-400">{label}</dt><dd class="mt-1 break-all text-gray-700">{value || "—"}</dd></div>;
}

function TagSection({ title, values }: { title: string; values: string[] }) {
  return <section><h3 class="mb-2 text-sm font-semibold text-gray-900">{title}</h3><div class="flex flex-wrap gap-2">{values.map((value) => <span key={value} class="rounded-full bg-gray-100 px-3 py-1 text-xs text-gray-700">{value}</span>)}</div></section>;
}

function Pill({ value, tone = "gray" }: { value: string; tone?: "gray" | "blue" | "green" }) {
  const colors = tone === "blue"
    ? "bg-blue-50 text-blue-700"
    : tone === "green"
      ? "bg-emerald-50 text-emerald-700"
      : "bg-gray-100 text-gray-700";
  return <span class={`rounded-full px-3 py-1 text-xs font-medium ${colors}`}>{value}</span>;
}

function sourceLabel(source: ArtifactSource): string {
  if (source === "huggingface") return t("mp.sourceHuggingFace");
  if (source === "modelscope") return t("mp.sourceModelScope");
  return t("mp.sourceOpenCSG");
}

function formatBytes(value?: number): string {
  if (!value) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index++;
  }
  return `${size.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function formatDate(value?: string): string {
  if (!value) return "—";
  return new Date(value).toLocaleDateString(locale.value === "zh" ? "zh-CN" : "en-US");
}

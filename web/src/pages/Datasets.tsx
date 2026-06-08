import { useEffect, useRef, useState } from "preact/hooks";
import { signal } from "@preact/signals";
import { getDatasetTags, searchDatasets, getDatasetFiles, deleteDataset, getDatasetManifest } from "../api/client";
import type { DatasetInfo, DatasetFileEntry, DatasetManifestResponse, DatasetDownloadFile } from "../api/client";
import { t, locale } from "../i18n";
import { DownloadTableCell } from "../components/DownloadProgressPanel";
import { getDownloadTask, getDownloadTasks, hasActiveDownload, clearDownloadTask, downloadCompletionVersion } from "../downloads";
import type { DownloadTask } from "../downloads";

type View = { kind: "list" } | { kind: "detail"; dataset: string; path: string };
type DatasetTableRow = {
  dataset: DatasetInfo;
  task?: DownloadTask;
  downloadOnly: boolean;
};

const allDatasets = signal<DatasetInfo[]>([]);
const searchQuery = signal("");
const sortField = signal<"name" | "size" | "modified_at">("name");
const sortAsc = signal(true);
const currentView = signal<View>({ kind: "list" });
const fileEntries = signal<DatasetFileEntry[]>([]);
const filesLoading = signal(false);
const datasetsLoading = signal(false);

function loadDatasets() {
  datasetsLoading.value = true;
  const query = searchQuery.value.trim();
  const promise = query ? searchDatasets(query, 100, 0) : getDatasetTags();
  promise
    .then((result) => {
      allDatasets.value = Array.isArray(result) ? result : result.datasets;
    })
    .catch(() => {})
    .finally(() => {
      datasetsLoading.value = false;
    });
}

function sortedDatasets(): DatasetInfo[] {
  const field = sortField.value;
  const asc = sortAsc.value;
  return [...allDatasets.value].sort((a, b) => {
    let cmp = 0;
    if (field === "name") cmp = a.name.localeCompare(b.name);
    else if (field === "size") cmp = a.size - b.size;
    else cmp = new Date(a.modified_at).getTime() - new Date(b.modified_at).getTime();
    return asc ? cmp : -cmp;
  });
}

function datasetRows(datasets: DatasetInfo[]): DatasetTableRow[] {
  const rows = datasets.map((dataset) => ({
    dataset,
    task: getDownloadTask("dataset", dataset.name),
    downloadOnly: false,
  }));
  const known = new Set(datasets.map((dataset) => dataset.name));
  for (const task of getDownloadTasks("dataset")) {
    if (known.has(task.name)) continue;
    rows.push({
      dataset: {
        name: task.name,
        dataset: task.name,
        size: task.totalBytes || task.completedBytes,
        files: 0,
        modified_at: task.updatedAt,
      },
      task,
      downloadOnly: true,
    });
  }
  return rows;
}

async function loadFiles(dataset: string, path: string) {
  filesLoading.value = true;
  try {
    const resp = await getDatasetFiles(dataset, path);
    const dirs = (resp.entries || []).filter((e) => e.is_dir);
    const files = (resp.entries || []).filter((e) => !e.is_dir);
    dirs.sort((a, b) => a.name.localeCompare(b.name));
    files.sort((a, b) => a.name.localeCompare(b.name));
    fileEntries.value = [...dirs, ...files];
  } catch {
    fileEntries.value = [];
  }
  filesLoading.value = false;
}

export function Datasets() {
  void locale.value;

  useEffect(() => {
    loadDatasets();
    return () => {
      currentView.value = { kind: "list" };
    };
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      loadDatasets();
    }, searchQuery.value.trim() ? 250 : 0);
    return () => clearTimeout(timer);
  }, [searchQuery.value]);

  if (currentView.value.kind === "detail") {
    return <DatasetDetail dataset={currentView.value.dataset} path={currentView.value.path} />;
  }

  return <DatasetList />;
}

function DatasetList() {
  useEffect(() => {
    if (downloadCompletionVersion.value > 0) loadDatasets();
  }, [downloadCompletionVersion.value]);

  const handleDelete = async (name: string) => {
    if (hasActiveDownload.value) return;
    if (!confirm(t("ds.deleteConfirm", name))) return;
    await deleteDataset(name);
    // 清除对应的下载任务记录
    const task = getDownloadTask("dataset", name);
    if (task) clearDownloadTask(task);
    allDatasets.value = allDatasets.value.filter((d) => d.name !== name);
  };

  const handleDetails = (name: string) => {
    if (hasActiveDownload.value) return;
    currentView.value = { kind: "detail", dataset: name, path: "" };
    loadFiles(name, "");
  };

  const toggleSort = (field: "name" | "size" | "modified_at") => {
    if (sortField.value === field) {
      sortAsc.value = !sortAsc.value;
    } else {
      sortField.value = field;
      sortAsc.value = true;
    }
  };

  const rows = datasetRows(sortedDatasets());
  const downloading = hasActiveDownload.value;

  return (
    <div class="p-8 max-w-5xl mx-auto">
      <div class="mb-1">
        <h1 class="text-2xl font-bold text-gray-900">{t("ds.title")}</h1>
        <p class="text-gray-500 text-sm mt-1">{t("ds.subtitle")}</p>
      </div>

      <div class="flex items-center gap-4 mt-6 mb-6">
        <div class="relative flex-1 min-w-[260px]">
          <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35m1.85-5.15a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            disabled={downloading}
            value={searchQuery.value}
            onInput={(e) => (searchQuery.value = (e.currentTarget as HTMLInputElement).value)}
            placeholder={t("ds.search")}
            class="w-full pl-10 pr-24 py-2.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-400"
          />
          <span class="absolute right-3 top-1/2 -translate-y-1/2 text-[11px] font-medium text-gray-400 bg-gray-50 px-2 py-0.5 rounded-full">
            {datasetsLoading.value ? t("ds.searching") : t("ds.results", rows.length)}
          </span>
        </div>
      </div>

      <div class="bg-white rounded-xl border border-gray-200 overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-100 text-left text-gray-500 bg-gray-50">
              <SortHeader label={t("ds.datasetName")} field="name" current={sortField.value} asc={sortAsc.value} onToggle={toggleSort} />
              <SortHeader label={t("ds.fileSize")} field="size" current={sortField.value} asc={sortAsc.value} onToggle={toggleSort} />
              <th class="px-4 py-3 font-medium">{t("downloads.progress")}</th>
              <SortHeader label={t("ds.dateTime")} field="modified_at" current={sortField.value} asc={sortAsc.value} onToggle={toggleSort} />
              <th class="px-4 py-3 font-medium text-right">{t("ds.operation")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={5} class="text-center py-12 text-gray-400">
                  {t("ds.noDatasets")}
                </td>
              </tr>
            ) : (
              rows.map(({ dataset: d, task, downloadOnly }) => (
                <tr key={d.name} class="border-b border-gray-50 hover:bg-gray-50/50">
                  <td class="px-4 py-3">
                    <button
                      onClick={() => handleDetails(d.name)}
                      disabled={downloading || downloadOnly}
                      class="font-medium text-indigo-600 hover:text-indigo-800 hover:underline break-all text-left disabled:text-gray-400 disabled:hover:text-gray-400 disabled:cursor-not-allowed"
                    >
                      {d.name}
                    </button>
                  </td>
                  <td class="px-4 py-3">
                    <span class="bg-indigo-50 text-indigo-700 px-2 py-0.5 rounded text-xs font-medium">
                      {fmtSize(d.size)}
                    </span>
                  </td>
                  <td class="px-4 py-3">
                    <DownloadTableCell task={task} onComplete={loadDatasets} />
                  </td>
                  <td class="px-4 py-3 text-gray-500">
                    {new Date(d.modified_at).toLocaleDateString("en-US", { day: "numeric", month: "long" })}
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex items-center justify-end gap-3">
                      <button disabled={downloading || downloadOnly} onClick={() => handleDelete(d.name)} class="text-gray-500 hover:text-red-600 text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed">
                        {t("ds.delete")}
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function DatasetDetail({ dataset, path }: { dataset: string; path: string }) {
  const [manifest, setManifest] = useState<DatasetManifestResponse | null>(null);
  const [detailError, setDetailError] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const copyResetRef = useRef<number | null>(null);

  useEffect(() => {
    loadFiles(dataset, path);
  }, [dataset, path]);

  useEffect(() => {
    setManifest(null);
    setDetailError("");
    getDatasetManifest(dataset)
      .then((data) => {
        setManifest(data);
      })
      .catch((err: any) => {
        setDetailError(err?.message || t("ds.failedLoadDetail"));
      });
  }, [dataset]);

  useEffect(() => {
    return () => {
      if (copyResetRef.current !== null) {
        window.clearTimeout(copyResetRef.current);
      }
    };
  }, []);

  const navigateTo = (subPath: string) => {
    currentView.value = { kind: "detail", dataset, path: subPath };
  };

  const goBack = () => {
    if (path) {
      const parts = path.split("/").filter(Boolean);
      parts.pop();
      navigateTo(parts.join("/"));
    } else {
      currentView.value = { kind: "list" };
    }
  };

  const breadcrumbs = buildBreadcrumbs(dataset, path);
  const manifestURL = buildDatasetManifestURL(dataset);
  const manifestCurl = buildCurlCommand(manifestURL);
  const exampleFile = manifest?.files?.[0];
  const exampleCurl = exampleFile ? buildDatasetFileCurlCommand(exampleFile) : "";
  const fileMetaMap = new Map((manifest?.files || []).map((file) => [file.path, file]));

  const handleCopy = async (key: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopiedKey(key);
      if (copyResetRef.current !== null) {
        window.clearTimeout(copyResetRef.current);
      }
      copyResetRef.current = window.setTimeout(() => {
        setCopiedKey("");
        copyResetRef.current = null;
      }, 1500);
    } catch {
      // Ignore clipboard failures.
    }
  };

  return (
    <div class="p-8 max-w-5xl mx-auto">
      <div class="flex items-center gap-3 mb-4">
        <button
          onClick={goBack}
          class="w-8 h-8 flex items-center justify-center rounded-full hover:bg-gray-200 transition-colors text-gray-600"
          aria-label={t("ds.back")}
          title={t("ds.back")}
        >
          <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M15 19l-7-7 7-7" />
          </svg>
        </button>
        <div class="min-w-0">
          <h1 class="text-xl font-bold text-gray-900 break-all">{dataset}</h1>
          <p class="text-sm text-gray-500 mt-1">{t("ds.detailSubtitle")}</p>
        </div>
      </div>

      {detailError && (
        <div class="mb-4 flex items-start gap-2 bg-red-50 border border-red-200 text-red-700 text-sm px-4 py-3 rounded-lg">
          <svg class="w-4 h-4 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span class="whitespace-pre-line flex-1">{detailError}</span>
        </div>
      )}

      {manifest && (
        <div class="space-y-6 mb-6">
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
            <SummaryTile label={t("ds.fileSize")} value={fmtSize(manifest.details.size)} />
            <SummaryTile label={t("ds.fileCount")} value={String(manifest.files.length)} />
            <SummaryTile label={t("ds.updated")} value={fmtDate(manifest.details.modified_at)} />
            <SummaryTile label={t("ds.licenseLabel")} value={manifest.details.license || t("ds.notAvailable")} />
          </div>

          {(manifest.details.description || manifest.details.license) && (
            <section class="bg-white rounded-xl border border-gray-200 p-5 space-y-3">
              {manifest.details.description && (
                <div>
                  <h2 class="text-sm font-semibold text-gray-900 mb-1">{t("ds.description")}</h2>
                  <p class="text-sm text-gray-600 whitespace-pre-wrap">{manifest.details.description}</p>
                </div>
              )}
              {manifest.details.license && (
                <div class="text-sm text-gray-600">
                  <span class="font-semibold text-gray-900 mr-2">{t("ds.licenseLabel")}</span>
                  <span>{manifest.details.license}</span>
                </div>
              )}
            </section>
          )}

          <section class="bg-white rounded-xl border border-gray-200 p-5 space-y-4">
            <div>
              <h2 class="text-lg font-semibold text-gray-900">{t("ds.downloadMethods")}</h2>
              <p class="text-sm text-gray-500 mt-1">{t("ds.downloadHint")}</p>
            </div>

            <div>
              <div class="flex items-center justify-between gap-3 flex-wrap mb-2">
                <span class="text-sm font-medium text-gray-700">{t("ds.manifestUrl")}</span>
                <button
                  onClick={() => void handleCopy("manifest-url", manifestURL)}
                  class={`text-xs transition-colors flex items-center gap-1 ${
                    copiedKey === "manifest-url" ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                  }`}
                >
                  {copiedKey === "manifest-url" ? t("dash.copied") : t("ds.copyUrl")}
                </button>
              </div>
              <div class="rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm text-gray-700 font-mono break-all">
                {manifestURL}
              </div>
            </div>

            <div class="grid grid-cols-1 xl:grid-cols-2 gap-4">
              <CodeBlock
                title={t("ds.manifestCurl")}
                code={manifestCurl}
                copied={copiedKey === "manifest-curl"}
                onCopy={() => void handleCopy("manifest-curl", manifestCurl)}
              />
              {exampleFile && (
                <CodeBlock
                  title={t("ds.fileCurl")}
                  code={exampleCurl}
                  copied={copiedKey === "example-curl"}
                  onCopy={() => void handleCopy("example-curl", exampleCurl)}
                />
              )}
            </div>
          </section>
        </div>
      )}

      {path && (
        <div class="flex items-center gap-1 mb-4 text-sm text-gray-500">
          {breadcrumbs.map((bc, i) => (
            <span key={i} class="flex items-center gap-1">
              {i > 0 && <span class="text-gray-300 mx-1">/</span>}
              {bc.clickable ? (
                <button
                  onClick={() => navigateTo(bc.path)}
                  class="text-indigo-600 hover:text-indigo-800 hover:underline"
                >
                  {bc.label}
                </button>
              ) : (
                <span class="text-gray-700 font-medium">{bc.label}</span>
              )}
            </span>
          ))}
        </div>
      )}

      <div class="bg-white rounded-xl border border-gray-200 overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-100 text-left text-gray-500 bg-gray-50">
              <th class="px-4 py-3 font-medium">{t("ds.name")}</th>
              <th class="px-4 py-3 font-medium text-right">{t("ds.size")}</th>
              <th class="px-4 py-3 font-medium text-right">{t("ds.dateModified")}</th>
              <th class="px-4 py-3 font-medium text-right">{t("ds.operation")}</th>
            </tr>
          </thead>
          <tbody>
            {filesLoading.value ? (
              <tr>
                <td colSpan={4} class="text-center py-12 text-gray-400">
                  {t("ds.loadingFiles")}
                </td>
              </tr>
            ) : fileEntries.value.length === 0 ? (
              <tr>
                <td colSpan={4} class="text-center py-12 text-gray-400">
                  {t("ds.noFiles")}
                </td>
              </tr>
            ) : (
              fileEntries.value.map((f) => {
                const relPath = path ? `${path}/${f.name}` : f.name;
                const fileMeta = fileMetaMap.get(relPath);
                const fileURL = fileMeta ? absoluteURL(fileMeta.download_url) : buildDatasetFileURL(dataset, relPath);
                const fileCurl = fileMeta ? buildDatasetFileCurlCommand(fileMeta) : buildDatasetFileCurlCommand({
                  path: relPath,
                  size: f.size,
                  download_url: fileURL,
                });
                return (
                  <tr key={f.name} class="border-b border-gray-50 hover:bg-gray-50/50">
                    <td class="px-4 py-3">
                      <div class="flex items-start gap-2">
                        <div class="pt-0.5">{f.is_dir ? <FolderIcon /> : <FileIcon />}</div>
                        <div class="min-w-0">
                          {f.is_dir ? (
                            <button
                              onClick={() => navigateTo(relPath)}
                              class="text-indigo-600 hover:text-indigo-800 hover:underline font-medium break-all text-left"
                            >
                              {f.name}
                            </button>
                          ) : (
                            <div class="text-gray-900 break-all">{f.name}</div>
                          )}
                          {!f.is_dir && fileMeta?.sha256 && (
                            <div class="mt-1 font-mono text-[11px] text-gray-500 break-all">
                              {t("ds.sha256")}: {fileMeta.sha256}
                            </div>
                          )}
                        </div>
                      </div>
                    </td>
                    <td class="px-4 py-3 text-right text-gray-500">
                      {f.is_dir ? "—" : fmtSizeDetailed(f.size)}
                    </td>
                    <td class="px-4 py-3 text-right text-gray-500">{fmtRelativeTime(f.modified_at)}</td>
                    <td class="px-4 py-3">
                      <div class="flex items-center justify-end gap-3 flex-wrap">
                        {!f.is_dir && (
                          <>
                            <button
                              onClick={() => void handleCopy(`url:${relPath}`, fileURL)}
                              class={`text-sm transition-colors ${
                                copiedKey === `url:${relPath}` ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                              }`}
                            >
                              {copiedKey === `url:${relPath}` ? t("dash.copied") : t("ds.copyUrl")}
                            </button>
                            <button
                              onClick={() => void handleCopy(`curl:${relPath}`, fileCurl)}
                              class={`text-sm transition-colors ${
                                copiedKey === `curl:${relPath}` ? "text-green-600" : "text-gray-500 hover:text-indigo-600"
                              }`}
                            >
                              {copiedKey === `curl:${relPath}` ? t("dash.copied") : t("ds.copyCurl")}
                            </button>
                          </>
                        )}
                      </div>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function SortHeader({
  label,
  field,
  current,
  asc,
  onToggle,
}: {
  label: string;
  field: string;
  current: string;
  asc: boolean;
  onToggle: (f: "name" | "size" | "modified_at") => void;
}) {
  const active = current === field;
  return (
    <th
      class="px-4 py-3 font-medium cursor-pointer select-none hover:text-gray-700"
      onClick={() => onToggle(field as "name" | "size" | "modified_at")}
    >
      <span class="flex items-center gap-1">
        {label}
        <span class={`text-xs ${active ? "text-indigo-600" : "text-gray-300"}`}>
          {active ? (asc ? "\u25B2" : "\u25BC") : "\u21C5"}
        </span>
      </span>
    </th>
  );
}

function CodeBlock({
  title,
  code,
  copied,
  onCopy,
}: {
  title: string;
  code: string;
  copied: boolean;
  onCopy: () => void;
}) {
  return (
    <div>
      <div class="flex items-center justify-between mb-2">
        <span class="text-sm font-medium text-gray-700">{title}</span>
        <button
          onClick={onCopy}
          class={`text-xs transition-colors flex items-center gap-1 ${copied ? "text-green-600" : "text-gray-500 hover:text-indigo-600"}`}
        >
          {copied ? t("dash.copied") : t("ds.copyCommand")}
        </button>
      </div>
      <pre class="bg-gray-900 text-gray-100 rounded-lg p-4 text-xs leading-5 overflow-x-auto font-mono whitespace-pre-wrap break-all">
        {code}
      </pre>
    </div>
  );
}

function SummaryTile({ label, value }: { label: string; value: string }) {
  return (
    <div class="rounded-xl border border-gray-200 bg-white px-4 py-3">
      <div class="text-xs font-medium uppercase tracking-wide text-gray-400">{label}</div>
      <div class="mt-1 text-sm font-semibold text-gray-900 break-words">{value}</div>
    </div>
  );
}

function FolderIcon() {
  return (
    <svg class="w-4 h-4 text-indigo-500 flex-shrink-0" fill="currentColor" viewBox="0 0 20 20">
      <path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z" />
    </svg>
  );
}

function FileIcon() {
  return (
    <svg class="w-4 h-4 text-gray-400 flex-shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
      <path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
    </svg>
  );
}

function buildBreadcrumbs(dataset: string, path: string) {
  const parts = path.split("/").filter(Boolean);
  const name = dataset.split("/").pop() || dataset;
  const crumbs: { label: string; path: string; clickable: boolean }[] = [
    { label: name, path: "", clickable: parts.length > 0 },
  ];
  let accumulated = "";
  for (let i = 0; i < parts.length; i++) {
    accumulated = accumulated ? `${accumulated}/${parts[i]}` : parts[i];
    crumbs.push({
      label: parts[i],
      path: accumulated,
      clickable: i < parts.length - 1,
    });
  }
  return crumbs;
}

function buildDatasetManifestURL(dataset: string): string {
  const parts = splitDatasetID(dataset);
  if (!parts) {
    return absoluteURL(`/api/datasets/${encodeURIComponent(dataset)}/manifest`);
  }
  return absoluteURL(`/api/datasets/${encodeURIComponent(parts.namespace)}/${encodeURIComponent(parts.name)}/manifest`);
}

function buildDatasetFileURL(dataset: string, relPath: string): string {
  const parts = splitDatasetID(dataset);
  if (!parts) {
    return absoluteURL(`/api/datasets/${encodeURIComponent(dataset)}/files/${encodeURIComponent(relPath)}`);
  }
  const segments = relPath.split("/").filter(Boolean).map((segment) => encodeURIComponent(segment));
  return absoluteURL(`/api/datasets/${encodeURIComponent(parts.namespace)}/${encodeURIComponent(parts.name)}/files/${segments.join("/")}`);
}

function buildCurlCommand(url: string): string {
  return `curl ${shellQuote(url)}`;
}

function buildDatasetFileCurlCommand(file: Pick<DatasetDownloadFile, "path" | "download_url" | "size">): string {
  const fileURL = absoluteURL(file.download_url);
  const targetPath = shellQuote(file.path);
  return `mkdir -p "$(dirname -- ${targetPath})" && curl -L -C - ${shellQuote(fileURL)} -o ${targetPath}`;
}

function absoluteURL(path: string): string {
  if (typeof window === "undefined") {
    return path;
  }
  return new URL(path, window.location.origin).toString();
}

function splitDatasetID(dataset: string): { namespace: string; name: string } | null {
  const slash = dataset.indexOf("/");
  if (slash <= 0 || slash === dataset.length - 1) {
    return null;
  }
  return {
    namespace: dataset.slice(0, slash),
    name: dataset.slice(slash + 1),
  };
}

function shellQuote(value: string): string {
  return "'" + value.replace(/'/g, `'\"'\"'`) + "'";
}

function fmtSize(bytes: number): string {
  if (bytes === 0) return "0 B";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(1)}GB`;
  const mb = bytes / (1024 * 1024);
  if (mb >= 1) return `${mb.toFixed(1)}MB`;
  const kb = bytes / 1024;
  return `${kb.toFixed(0)}KB`;
}

function fmtSizeDetailed(bytes: number): string {
  if (bytes === 0) return "—";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(0)} GB`;
  const mb = bytes / (1024 * 1024);
  if (mb >= 1) return `${mb.toFixed(mb >= 100 ? 0 : 1)} MB`;
  const kb = bytes / 1024;
  if (kb >= 1) return `${kb.toFixed(kb >= 100 ? 0 : 1)} kB`;
  return `${bytes} Bytes`;
}

function fmtRelativeTime(dateStr: string): string {
  const date = new Date(dateStr);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMin = Math.floor(diffMs / 60000);
  if (diffMin < 1) return t("ds.lessThanMinute");
  if (diffMin < 60) return t("ds.minutesAgo", diffMin);
  const diffHours = Math.floor(diffMin / 60);
  if (diffHours < 24) return t("ds.hoursAgo", diffHours);
  const diffDays = Math.floor(diffHours / 24);
  return t("ds.daysAgo", diffDays);
}

function fmtDate(dateStr: string): string {
  const language = locale.value === "zh" ? "zh-CN" : "en-US";
  return new Date(dateStr).toLocaleString(language, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

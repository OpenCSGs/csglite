import { useEffect, useRef } from "preact/hooks";
import { computed, signal } from "@preact/signals";
import { deleteModel, getPs, loadModel, searchLocalModels, uploadLocalModel } from "../api/client";
import type { LoadModelOptions, LocalModelUploadFile, ModelInfo, RunningModel } from "../api/client";
import { locale, t } from "../i18n";
import { DownloadTableCell } from "../components/DownloadProgressPanel";
import { getDownloadTask, getDownloadTasks, hasActiveDownload, clearDownloadTask, pauseDownload, startDownload } from "../downloads";
import type { DownloadTask } from "../downloads";

type FormatFilter = "all" | "gguf" | "safetensors";
type RunModelParams = {
  numCtx: string;
  numParallel: string;
  nGpuLayers: string;
  cacheTypeK: string;
  cacheTypeV: string;
  dtype: string;
  keepAlive: string;
};
type ModelTableRow = {
  model: ModelInfo;
  task?: DownloadTask;
  downloadOnly: boolean;
};
type UploadMode = "archive" | "directory" | "files";

const RUN_PARAMS_STORAGE_KEY = "csghub-lite-run-params";
const CACHE_TYPE_OPTIONS = ["f32", "f16", "bf16", "q8_0", "q4_0", "q4_1", "iq4_nl", "q5_0", "q5_1"];
const DTYPE_OPTIONS = ["f32", "f16", "bf16", "q8_0", "tq1_0", "tq2_0", "auto"];

const allModels = signal<ModelInfo[]>([]);
const runningModels = signal<RunningModel[]>([]);
const searchQuery = signal("");
const formatFilter = signal<FormatFilter>("all");
const sortField = signal<"name" | "size" | "modified_at">("name");
const sortAsc = signal(true);
const modelsLoading = signal(false);
const loadingRun = signal<string>("");
const loadProgress = signal<string>("");
const libraryError = signal<string>("");
const runDialogModel = signal<ModelInfo | null>(null);
const runDialogError = signal<string>("");
const runParams = signal<RunModelParams>(loadSavedRunParams());
const uploadDialogOpen = signal(false);
const uploadModelID = signal("");
const uploadMode = signal<UploadMode>("files");
const uploadFiles = signal<LocalModelUploadFile[]>([]);
const uploadOverwrite = signal(false);
const uploadProgress = signal(0);
const uploadError = signal("");
const uploadBusy = signal(false);

let loadModelsRequestID = 0;

function defaultRunParams(): RunModelParams {
  return {
    numCtx: "",
    numParallel: "",
    nGpuLayers: "",
    cacheTypeK: "",
    cacheTypeV: "",
    dtype: "",
    keepAlive: "",
  };
}

function normalizeRunParams(raw: any): RunModelParams {
  const defaults = defaultRunParams();
  return {
    numCtx: String(raw?.numCtx ?? raw?.num_ctx ?? defaults.numCtx),
    numParallel: String(raw?.numParallel ?? raw?.num_parallel ?? defaults.numParallel),
    nGpuLayers: String(raw?.nGpuLayers ?? raw?.n_gpu_layers ?? defaults.nGpuLayers),
    cacheTypeK: String(raw?.cacheTypeK ?? raw?.cache_type_k ?? defaults.cacheTypeK),
    cacheTypeV: String(raw?.cacheTypeV ?? raw?.cache_type_v ?? defaults.cacheTypeV),
    dtype: String(raw?.dtype ?? defaults.dtype),
    keepAlive: String(raw?.keepAlive ?? raw?.keep_alive ?? defaults.keepAlive),
  };
}

function loadSavedRunParams(): RunModelParams {
  try {
    return normalizeRunParams(JSON.parse(localStorage.getItem(RUN_PARAMS_STORAGE_KEY) || "{}"));
  } catch {
    return defaultRunParams();
  }
}

function saveRunParams(params: RunModelParams) {
  try {
    localStorage.setItem(RUN_PARAMS_STORAGE_KEY, JSON.stringify(params));
  } catch {
    /* ignore localStorage failures */
  }
}

function optionalText(value: string): string | undefined {
  const trimmed = value.trim();
  return trimmed ? trimmed : undefined;
}

function optionalInt(value: string, label: string, min: number): number | undefined {
  const trimmed = value.trim();
  if (!trimmed) return undefined;
  const parsed = Number(trimmed);
  if (!Number.isInteger(parsed)) {
    throw new Error(t("lib.runParamInteger", label));
  }
  if (parsed < min) {
    throw new Error(t("lib.runParamMin", label, min));
  }
  return parsed;
}

function buildLoadOptions(params: RunModelParams): LoadModelOptions {
  return {
    num_ctx: optionalInt(params.numCtx, t("lib.runParamNumCtx"), 1024),
    num_parallel: optionalInt(params.numParallel, t("lib.runParamNumParallel"), 1),
    n_gpu_layers: optionalInt(params.nGpuLayers, t("lib.runParamNGPULayers"), 0),
    cache_type_k: optionalText(params.cacheTypeK),
    cache_type_v: optionalText(params.cacheTypeV),
    dtype: optionalText(params.dtype),
    keep_alive: optionalText(params.keepAlive),
  };
}

function isImageGenerationModel(model: Pick<ModelInfo, "pipeline_tag">): boolean {
	return model.pipeline_tag === "text-to-image";
}

function isEmbeddingModel(model: Pick<ModelInfo, "pipeline_tag">): boolean {
	const tag = model.pipeline_tag || "";
	return tag === "feature-extraction" || tag === "sentence-similarity" || tag === "text-embedding" || tag === "embedding";
}

function isASRModel(model: Pick<ModelInfo, "pipeline_tag">): boolean {
	const tag = model.pipeline_tag || "";
	return tag === "automatic-speech-recognition";
}

function buildLoadOptionsForModel(model: ModelInfo, params: RunModelParams): LoadModelOptions {
	if (isImageGenerationModel(model) || isASRModel(model)) {
		return {
			keep_alive: optionalText(params.keepAlive),
		};
	}
	if (isEmbeddingModel(model)) {
		return {
			num_ctx: optionalInt(params.numCtx, t("lib.runParamNumCtx"), 1024),
			n_gpu_layers: optionalInt(params.nGpuLayers, t("lib.runParamNGPULayers"), 0),
			dtype: optionalText(params.dtype),
			keep_alive: optionalText(params.keepAlive),
		};
	}
	return buildLoadOptions(params);
}

function loadingLabelForModel(model: ModelInfo): string {
	if (loadProgress.value) return loadProgress.value;
	if (isImageGenerationModel(model)) return t("lib.loadingImageRuntime");
	if (isASRModel(model)) return t("lib.loadingASRRuntime");
	if (model.format !== "gguf") return t("lib.converting");
	return t("lib.loadingModel");
}

const filtered = computed(() => {
  const list = allModels.value;
  const field = sortField.value;
  const asc = sortAsc.value;
  return [...list].sort((a, b) => {
    let cmp = 0;
    if (field === "name") cmp = a.name.localeCompare(b.name);
    else if (field === "size") cmp = a.size - b.size;
    else cmp = new Date(a.modified_at).getTime() - new Date(b.modified_at).getTime();
    return asc ? cmp : -cmp;
  });
});

function isCloudModel(model: Pick<ModelInfo, "source">): boolean {
  return model.source === "cloud";
}

function modelDetailHref(modelID: string): string {
  return `/library/detail/${encodeURIComponent(modelID)}`;
}

function modelRows(models: ModelInfo[]): ModelTableRow[] {
  const rows = models.map((model) => ({
    model,
    task: getDownloadTask("model", model.name),
    downloadOnly: false,
  }));
  const known = new Set(models.map((model) => model.name));
  for (const task of getDownloadTasks("model")) {
    if (known.has(task.name)) continue;
    rows.push({
      model: {
        name: task.name,
        model: task.name,
        size: task.totalBytes || task.completedBytes,
        format: "",
        modified_at: task.updatedAt,
      },
      task,
      downloadOnly: true,
    });
  }
  return rows;
}

function uploadPathForFile(file: File): string {
  return ((file as any).webkitRelativePath || file.name || "").replace(/\\/g, "/");
}

function isArchivePath(path: string): boolean {
  const lower = path.toLowerCase();
  return lower.endsWith(".zip") || lower.endsWith(".tar") || lower.endsWith(".tar.gz") || lower.endsWith(".tgz");
}

function deriveUploadModelID(path: string): string {
  const base = (path.split("/").filter(Boolean).pop() || "uploaded-model")
    .replace(/\.(tar\.gz|tgz|zip|tar|gguf|safetensors|bin)$/i, "")
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/^[-._]+|[-._]+$/g, "");
  return `local/${base || "uploaded-model"}`;
}

function deriveUploadModelIDFromSelection(files: LocalModelUploadFile[], mode: UploadMode): string {
  if (mode === "directory") {
    const roots = files
      .map((item) => (item.path || item.file.name || "").replace(/\\/g, "/").split("/").filter(Boolean)[0])
      .filter(Boolean);
    if (roots.length > 0 && roots.every((root) => root === roots[0])) {
      return deriveUploadModelID(roots[0]);
    }
  }
  return deriveUploadModelID(files[0]?.path || files[0]?.file.name || "");
}

function setUploadSelection(files: LocalModelUploadFile[], mode: UploadMode) {
  uploadFiles.value = files;
  uploadMode.value = mode;
  uploadProgress.value = 0;
  uploadError.value = "";
  if (!uploadModelID.value.trim() && files.length > 0) {
    uploadModelID.value = deriveUploadModelIDFromSelection(files, mode);
  }
}

function filesFromInput(list: FileList | null): LocalModelUploadFile[] {
  return Array.from(list || []).map((file) => ({
    file,
    path: uploadPathForFile(file),
  }));
}

async function filesFromDrop(event: DragEvent): Promise<LocalModelUploadFile[]> {
  const items = Array.from(event.dataTransfer?.items || []);
  const entries = items
    .map((item: any) => (typeof item.webkitGetAsEntry === "function" ? item.webkitGetAsEntry() : null))
    .filter(Boolean);
  if (entries.length === 0) {
    return filesFromInput(event.dataTransfer?.files || null);
  }
  const result: LocalModelUploadFile[] = [];
  for (const entry of entries) {
    result.push(...(await filesFromEntry(entry, "")));
  }
  return result;
}

function filesFromEntry(entry: any, parentPath: string): Promise<LocalModelUploadFile[]> {
  const entryPath = parentPath ? `${parentPath}/${entry.name}` : entry.name;
  if (entry.isFile) {
    return new Promise((resolve, reject) => {
      entry.file(
        (file: File) => resolve([{ file, path: entryPath }]),
        (err: any) => reject(err)
      );
    });
  }
  if (!entry.isDirectory) return Promise.resolve([]);
  const reader = entry.createReader();
  const entries: any[] = [];
  return new Promise((resolve, reject) => {
    const readBatch = () => {
      reader.readEntries(
        async (batch: any[]) => {
          if (!batch.length) {
            try {
              const files: LocalModelUploadFile[] = [];
              for (const child of entries) {
                files.push(...(await filesFromEntry(child, entryPath)));
              }
              resolve(files);
            } catch (err) {
              reject(err);
            }
            return;
          }
          entries.push(...batch);
          readBatch();
        },
        (err: any) => reject(err)
      );
    };
    readBatch();
  });
}

async function loadModels() {
  const requestID = ++loadModelsRequestID;
  modelsLoading.value = true;
  libraryError.value = "";

  try {
    const models: ModelInfo[] = [];
    let offset = 0;
    const limit = 100;

    for (;;) {
      const resp = await searchLocalModels({
        q: searchQuery.value,
        format: formatFilter.value === "all" ? undefined : formatFilter.value,
        limit,
        offset,
      });

      models.push(...(resp.models || []).filter((model) => !isCloudModel(model)));

      if (!resp.has_more || !resp.models?.length) {
        break;
      }
      offset += resp.models.length;
    }

    if (requestID !== loadModelsRequestID) return;
    allModels.value = models;
  } catch (e: any) {
    if (requestID !== loadModelsRequestID) return;
    allModels.value = [];
    libraryError.value = e?.message || t("lib.failedLoadModels");
  } finally {
    if (requestID === loadModelsRequestID) {
      modelsLoading.value = false;
    }
  }
}

function loadRunningModels() {
  getPs().then((models) => (runningModels.value = models)).catch(() => {});
}

export function Library() {
  void locale.value;
  const fileInputRef = useRef<HTMLInputElement>(null);
  const folderInputRef = useRef<HTMLInputElement>(null);
  const completedDownloadsKey = getDownloadTasks("model")
    .filter((task) => task.status === "success" && task.completedAt)
    .map((task) => `${task.name}:${task.completedAt}`)
    .join("|");

  useEffect(() => {
    loadRunningModels();
  }, []);

  useEffect(() => {
    if (completedDownloadsKey) void loadModels();
  }, [completedDownloadsKey]);

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      void loadModels();
    }, searchQuery.value.trim() ? 250 : 0);
    return () => window.clearTimeout(timeout);
  }, [searchQuery.value, formatFilter.value]);

  const handleDelete = async (name: string) => {
    if (hasActiveDownload.value) return;
    if (!confirm(t("lib.deleteConfirm", name))) return;
    await deleteModel(name);
    // 清除对应的下载任务记录
    const task = getDownloadTask("model", name);
    if (task) clearDownloadTask(task);
    await loadModels();
    loadRunningModels();
  };

  const handleRun = async (name: string, options: LoadModelOptions) => {
    loadingRun.value = name;
    loadProgress.value = "";
    libraryError.value = "";
    try {
      await loadModel(name, (p) => {
        if (p.step && p.total && p.total > 0 && p.current) {
          const pct = Math.round((p.current / p.total) * 100);
          loadProgress.value = `${p.step} (${p.current}/${p.total}) ${pct}%`;
        } else if (p.step) {
          loadProgress.value = p.step;
        } else if (p.status) {
          loadProgress.value = p.status;
        }
      }, options);
      await loadModels();
      loadRunningModels();
    } catch (e: any) {
      libraryError.value = e?.message || t("lib.failedLoad");
    }
    loadingRun.value = "";
    loadProgress.value = "";
  };

  const openUploadDialog = () => {
    if (hasActiveDownload.value || uploadBusy.value) return;
    uploadDialogOpen.value = true;
    uploadError.value = "";
    uploadProgress.value = 0;
  };

  const closeUploadDialog = () => {
    if (uploadBusy.value) return;
    uploadDialogOpen.value = false;
    uploadFiles.value = [];
    uploadError.value = "";
    uploadProgress.value = 0;
  };

  const selectUploadFiles = (files: LocalModelUploadFile[], fallbackMode: UploadMode) => {
    const mode = files.length === 1 && isArchivePath(files[0].path || files[0].file.name) ? "archive" : fallbackMode;
    setUploadSelection(files, mode);
  };

  const submitUpload = async () => {
    if (uploadBusy.value || uploadFiles.value.length === 0) return;
    uploadBusy.value = true;
    uploadError.value = "";
    uploadProgress.value = 0;
    try {
      await uploadLocalModel(
        {
          model: uploadModelID.value.trim(),
          mode: uploadMode.value,
          overwrite: uploadOverwrite.value,
          files: uploadFiles.value,
        },
        (percent) => (uploadProgress.value = percent)
      );
      uploadDialogOpen.value = false;
      uploadFiles.value = [];
      uploadProgress.value = 0;
      await loadModels();
    } catch (e: any) {
      uploadError.value = e?.message || t("lib.uploadFailed");
    } finally {
      uploadBusy.value = false;
    }
  };

  const openRunDialog = (model: ModelInfo) => {
    if (hasActiveDownload.value) return;
    runParams.value = loadSavedRunParams();
    runDialogError.value = "";
    libraryError.value = "";
    runDialogModel.value = model;
  };

  const closeRunDialog = () => {
    if (loadingRun.value) return;
    runDialogModel.value = null;
    runDialogError.value = "";
  };

  const updateRunParam = (field: keyof RunModelParams, value: string) => {
    runParams.value = { ...runParams.value, [field]: value };
    runDialogError.value = "";
  };

  const submitRunDialog = async () => {
    const model = runDialogModel.value;
    if (!model) return;
    let options: LoadModelOptions;
    try {
      options = buildLoadOptionsForModel(model, runParams.value);
    } catch (e: any) {
      runDialogError.value = e?.message || t("lib.runParamInvalid");
      return;
    }
    saveRunParams(runParams.value);
    runDialogModel.value = null;
    runDialogError.value = "";
    await handleRun(model.name, options);
  };

  const toggleSort = (field: "name" | "size" | "modified_at") => {
    if (sortField.value === field) {
      sortAsc.value = !sortAsc.value;
    } else {
      sortField.value = field;
      sortAsc.value = true;
    }
  };

  const runningStatus = (name: string) => runningModels.value.find((m) => m.name === name)?.status || "";
  const hasActiveFilters = searchQuery.value.trim().length > 0 || formatFilter.value !== "all";
  const downloading = hasActiveDownload.value;
  const uploadDisabled = downloading || uploadBusy.value;
  const rows = modelRows(filtered.value);

  return (
    <div class="p-8 max-w-5xl mx-auto">
      <div class="flex items-center justify-between mb-1">
        <div>
          <h1 class="text-2xl font-bold text-gray-900">{t("lib.title")}</h1>
          <p class="text-gray-500 text-sm mt-1">{t("lib.subtitle")}</p>
        </div>
        <button
          onClick={openUploadDialog}
          disabled={uploadDisabled}
          class="inline-flex items-center justify-center px-4 py-2 text-sm rounded-lg bg-indigo-600 text-white hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t("lib.upload")}
        </button>
      </div>

      {libraryError.value && (
        <div class="mt-4 flex items-start gap-2 bg-red-50 border border-red-200 text-red-700 text-sm px-4 py-3 rounded-lg">
          <svg class="w-4 h-4 flex-shrink-0 mt-0.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span class="whitespace-pre-line flex-1">{libraryError.value}</span>
          <button onClick={() => (libraryError.value = "")} class="ml-auto text-red-400 hover:text-red-600 flex-shrink-0">&#x2715;</button>
        </div>
      )}

      <div class="flex items-center gap-4 mt-6 mb-6 flex-wrap">
        <div class="relative flex-1 min-w-[260px]">
          <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35m1.85-5.15a7 7 0 11-14 0 7 7 0 0114 0z" />
          </svg>
          <input
            type="text"
            disabled={uploadDisabled}
            value={searchQuery.value}
            onInput={(e) => (searchQuery.value = (e.currentTarget as HTMLInputElement).value)}
            placeholder={t("lib.search")}
            class="w-full pl-10 pr-24 py-2.5 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent disabled:bg-gray-100 disabled:text-gray-400"
          />
          <span class="absolute right-3 top-1/2 -translate-y-1/2 text-[11px] font-medium text-gray-400 bg-gray-50 px-2 py-0.5 rounded-full">
            {modelsLoading.value ? t("lib.searching") : t("lib.results", rows.length)}
          </span>
        </div>
        <div class="flex bg-gray-100 rounded-lg p-0.5">
          {(["all", "gguf", "safetensors"] as FormatFilter[]).map((f) => (
            <button
              key={f}
              onClick={() => (formatFilter.value = f)}
              disabled={uploadDisabled}
              class={`px-4 py-1.5 text-sm font-medium rounded-md capitalize transition-colors ${
                formatFilter.value === f
                  ? "bg-white text-gray-900 shadow-sm"
                  : "text-gray-500 hover:text-gray-700"
              }`}
            >
              {f === "all" ? t("lib.all") : f === "gguf" ? "GGUF" : "SafeTensors"}
            </button>
          ))}
        </div>
      </div>

      <div class="bg-white rounded-xl border border-gray-200 overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-gray-100 text-left text-gray-500 bg-gray-50">
              <SortHeader label={t("lib.modelName")} field="name" current={sortField.value} asc={sortAsc.value} onToggle={toggleSort} />
              <th class="px-4 py-3 font-medium">{t("lib.format")}</th>
              <SortHeader label={t("lib.fileSize")} field="size" current={sortField.value} asc={sortAsc.value} onToggle={toggleSort} />
              <th class="px-4 py-3 font-medium">{t("downloads.progress")}</th>
              <SortHeader label={t("lib.dateTime")} field="modified_at" current={sortField.value} asc={sortAsc.value} onToggle={toggleSort} />
              <th class="px-4 py-3 font-medium text-right">{t("lib.operation")}</th>
            </tr>
          </thead>
          <tbody>
            {modelsLoading.value ? (
              <tr>
                <td colSpan={6} class="text-center py-12 text-gray-400">
                  {t("lib.searching")}
                </td>
              </tr>
            ) : rows.length === 0 ? (
              <tr>
                <td colSpan={6} class="text-center py-12 text-gray-400">
                  {hasActiveFilters ? t("lib.noSearchResults") : t("lib.noModels")}
                </td>
              </tr>
            ) : (
              rows.map(({ model: m, task, downloadOnly }) => (
                <tr key={m.name} class="border-b border-gray-50 hover:bg-gray-50/50">
                  <td class="px-4 py-3">
                    <a href={downloading || downloadOnly ? undefined : modelDetailHref(m.model)} class={`font-medium break-all ${downloading || downloadOnly ? "text-gray-400 cursor-not-allowed" : "text-indigo-600 hover:text-indigo-800 hover:underline"}`}>
                      {m.name}
                    </a>
                  </td>
                  <td class="px-4 py-3">
                    <span
                      class={`inline-block px-2 py-0.5 rounded text-xs font-medium ${
                        m.format === "gguf" ? "bg-blue-50 text-blue-700" : "bg-purple-50 text-purple-700"
                      }`}
                    >
                      {m.format?.toUpperCase() || (downloadOnly ? t("downloads.downloading") : "—")}
                    </span>
                  </td>
                  <td class="px-4 py-3">
                    <span class="bg-indigo-50 text-indigo-700 px-2 py-0.5 rounded text-xs font-medium">
                      {fmtSize(m.size)}
                    </span>
                  </td>
                  <td class="px-4 py-3">
                    <DownloadTableCell task={task} onComplete={() => void loadModels()} />
                  </td>
                  <td class="px-4 py-3 text-gray-500">
                    {new Date(m.modified_at).toLocaleDateString("en-US", { day: "numeric", month: "long" })}
                  </td>
                  <td class="px-4 py-3">
                    <div class="flex items-center justify-end gap-3 flex-wrap">
                      <button
                        disabled={task?.status === "downloading"}
                        onClick={() => {
                          if (task && task.status !== "downloading") {
                            clearDownloadTask(task);
                          }
                          handleDelete(m.name);
                        }}
                        class="text-gray-500 hover:text-red-600 text-sm transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                      >
                        {t("lib.delete")}
                      </button>
                      {task?.status === "downloading" ? (
                        <button
                          onClick={() => pauseDownload(task.kind, task.name)}
                          class="inline-flex items-center justify-center w-16 px-3 py-1 text-xs rounded bg-indigo-600 text-white hover:bg-indigo-700 transition-colors font-medium"
                        >
                          {t("downloads.pause")}
                        </button>
                      ) : task?.status === "paused" || task?.status === "error" ? (
                        <button
                          onClick={() => startDownload(task.kind, task.name, () => void loadModels())}
                          disabled={hasActiveDownload.value && getDownloadTask("model", task.name)?.status !== "paused"}
                          class="inline-flex items-center justify-center w-16 px-3 py-1 text-xs rounded bg-indigo-600 text-white hover:bg-indigo-700 disabled:opacity-50 transition-colors font-medium"
                        >
                          {t("downloads.resume")}
                        </button>
                      ) : downloadOnly ? (
                        <span class="inline-flex items-center justify-center w-16 px-3 py-1 text-xs rounded bg-gray-50 text-gray-400 font-medium">
                          —
                        </span>
                      ) : runningStatus(m.name) === "running" ? (
                        <span class="inline-flex items-center justify-center w-16 px-3 py-1 text-xs rounded bg-green-50 text-green-700 font-medium">
                          {t("lib.running")}
                        </span>
                      ) : runningStatus(m.name) === "loading" ? (
                        <div class="flex items-center gap-2">
                          <span class="text-xs text-gray-500 max-w-[200px] truncate">
                            {loadingLabelForModel(m)}
                          </span>
                          <span class="inline-block w-3 h-3 border-2 border-indigo-600 border-t-transparent rounded-full animate-spin" />
                        </div>
                      ) : loadingRun.value === m.name ? (
                        <div class="flex items-center gap-2">
                          <span class="text-xs text-gray-500 max-w-[200px] truncate">
                            {loadingLabelForModel(m)}
                          </span>
                          <span class="inline-block w-3 h-3 border-2 border-indigo-600 border-t-transparent rounded-full animate-spin" />
                        </div>
                      ) : (
                        <button
                          onClick={() => openRunDialog(m)}
                          disabled={!!loadingRun.value || hasActiveDownload.value}
                          class="inline-flex items-center justify-center w-16 px-3 py-1 text-xs rounded bg-indigo-600 text-white hover:bg-indigo-700 disabled:opacity-50 transition-colors font-medium"
                        >
                          {t("lib.run")}
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      {runDialogModel.value && (
        <RunParamsDialog
          model={runDialogModel.value}
          params={runParams.value}
          error={runDialogError.value}
          disabled={!!loadingRun.value}
          onChange={updateRunParam}
          onCancel={closeRunDialog}
          onSubmit={submitRunDialog}
        />
      )}
      {uploadDialogOpen.value && (
        <UploadModelDialog
          modelID={uploadModelID.value}
          files={uploadFiles.value}
          mode={uploadMode.value}
          overwrite={uploadOverwrite.value}
          progress={uploadProgress.value}
          error={uploadError.value}
          busy={uploadBusy.value}
          fileInputRef={fileInputRef}
          folderInputRef={folderInputRef}
          onModelIDChange={(value) => (uploadModelID.value = value)}
          onOverwriteChange={(value) => (uploadOverwrite.value = value)}
          onPickFiles={() => fileInputRef.current?.click()}
          onPickFolder={() => folderInputRef.current?.click()}
          onFilesSelected={selectUploadFiles}
          onDropFiles={(files) => selectUploadFiles(files, "directory")}
          onCancel={closeUploadDialog}
          onSubmit={submitUpload}
        />
      )}
    </div>
  );
}

function UploadModelDialog({
  modelID,
  files,
  mode,
  overwrite,
  progress,
  error,
  busy,
  fileInputRef,
  folderInputRef,
  onModelIDChange,
  onOverwriteChange,
  onPickFiles,
  onPickFolder,
  onFilesSelected,
  onDropFiles,
  onCancel,
  onSubmit,
}: {
  modelID: string;
  files: LocalModelUploadFile[];
  mode: UploadMode;
  overwrite: boolean;
  progress: number;
  error: string;
  busy: boolean;
  fileInputRef: any;
  folderInputRef: any;
  onModelIDChange: (value: string) => void;
  onOverwriteChange: (value: boolean) => void;
  onPickFiles: () => void;
  onPickFolder: () => void;
  onFilesSelected: (files: LocalModelUploadFile[], mode: UploadMode) => void;
  onDropFiles: (files: LocalModelUploadFile[]) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const totalBytes = files.reduce((sum, item) => sum + item.file.size, 0);
  const modeLabel = mode === "archive" ? t("lib.uploadModeArchive") : mode === "directory" ? t("lib.uploadModeDirectory") : t("lib.uploadModeFiles");
  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4">
      <form
        class="w-full max-w-2xl bg-white rounded-2xl shadow-xl border border-gray-200 overflow-hidden"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
      >
        <div class="px-6 py-5 border-b border-gray-100">
          <h2 class="text-lg font-semibold text-gray-900">{t("lib.uploadTitle")}</h2>
          <p class="text-sm text-gray-500 mt-1">{t("lib.uploadDesc")}</p>
        </div>

        <div class="px-6 py-5 space-y-4">
          <div>
            <label class="block text-sm font-medium text-gray-700 mb-1">{t("lib.uploadModelID")}</label>
            <input
              type="text"
              value={modelID}
              disabled={busy}
              onInput={(e) => onModelIDChange((e.currentTarget as HTMLInputElement).value)}
              placeholder="local/my-model"
              class="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent disabled:bg-gray-100"
            />
            <p class="text-xs text-gray-400 mt-1">{t("lib.uploadModelIDHint")}</p>
          </div>

          <div
            class="border-2 border-dashed border-gray-200 rounded-xl px-5 py-8 text-center bg-gray-50"
            onDragOver={(e) => {
              e.preventDefault();
              if (e.dataTransfer) e.dataTransfer.dropEffect = "copy";
            }}
            onDrop={(e) => {
              e.preventDefault();
              if (busy) return;
              filesFromDrop(e).then(onDropFiles).catch((err: any) => (uploadError.value = err?.message || t("lib.uploadReadFailed")));
            }}
          >
            <p class="text-sm font-medium text-gray-700">{t("lib.uploadDropTitle")}</p>
            <p class="text-xs text-gray-500 mt-1">{t("lib.uploadDropDesc")}</p>
            <div class="mt-4 flex justify-center gap-3 flex-wrap">
              <button type="button" disabled={busy} onClick={onPickFiles} class="px-4 py-2 text-sm rounded-lg border border-gray-200 bg-white text-gray-700 hover:bg-gray-50 disabled:opacity-50">
                {t("lib.uploadPickFiles")}
              </button>
              <button type="button" disabled={busy} onClick={onPickFolder} class="px-4 py-2 text-sm rounded-lg border border-gray-200 bg-white text-gray-700 hover:bg-gray-50 disabled:opacity-50">
                {t("lib.uploadPickFolder")}
              </button>
            </div>
            <input
              ref={fileInputRef}
              type="file"
              multiple
              class="hidden"
              accept=".zip,.tar,.gz,.tgz,.gguf,.safetensors,.bin"
              onChange={(e) => onFilesSelected(filesFromInput((e.currentTarget as HTMLInputElement).files), "files")}
            />
            <input
              ref={folderInputRef}
              type="file"
              multiple
              class="hidden"
              {...({ webkitdirectory: "true" } as any)}
              onChange={(e) => onFilesSelected(filesFromInput((e.currentTarget as HTMLInputElement).files), "directory")}
            />
          </div>

          <div class="rounded-lg border border-gray-100 bg-white px-4 py-3 text-sm">
            <div class="flex items-center justify-between gap-3">
              <span class="font-medium text-gray-700">{files.length ? t("lib.uploadSelected", files.length, fmtSize(totalBytes)) : t("lib.uploadNoFiles")}</span>
              {files.length > 0 && <span class="text-xs text-gray-400">{modeLabel}</span>}
            </div>
            {files.length > 0 && (
              <div class="mt-2 max-h-24 overflow-auto text-xs text-gray-500 space-y-1">
                {files.slice(0, 8).map((item) => (
                  <div key={item.path} class="truncate">
                    {item.path}
                  </div>
                ))}
                {files.length > 8 && <div>{t("lib.uploadMoreFiles", files.length - 8)}</div>}
              </div>
            )}
          </div>

          <label class="flex items-center gap-2 text-sm text-gray-600">
            <input type="checkbox" checked={overwrite} disabled={busy} onChange={(e) => onOverwriteChange((e.currentTarget as HTMLInputElement).checked)} />
            {t("lib.uploadOverwrite")}
          </label>

          {busy && (
            <div>
              <div class="h-2 rounded-full bg-gray-100 overflow-hidden">
                <div class="h-full bg-indigo-600 transition-all" style={{ width: `${Math.max(2, progress)}%` }} />
              </div>
              <p class="text-xs text-gray-500 mt-1">{t("lib.uploadProgress", progress)}</p>
            </div>
          )}

          {error && <div class="text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2">{error}</div>}
        </div>

        <div class="px-6 py-4 bg-gray-50 border-t border-gray-100 flex justify-end gap-3">
          <button type="button" onClick={onCancel} disabled={busy} class="px-4 py-2 text-sm rounded-lg border border-gray-200 text-gray-600 hover:bg-white disabled:opacity-50">
            {t("lib.runParamCancel")}
          </button>
          <button type="submit" disabled={busy || files.length === 0 || !modelID.trim()} class="px-4 py-2 text-sm rounded-lg bg-indigo-600 text-white hover:bg-indigo-700 disabled:opacity-50">
            {busy ? t("lib.uploading") : t("lib.uploadSubmit")}
          </button>
        </div>
      </form>
    </div>
  );
}

function RunParamsDialog({
  model,
  params,
  error,
  disabled,
  onChange,
  onCancel,
  onSubmit,
}: {
  model: ModelInfo;
  params: RunModelParams;
  error: string;
  disabled: boolean;
  onChange: (field: keyof RunModelParams, value: string) => void;
  onCancel: () => void;
  onSubmit: () => void;
}) {
  const imageGenerationModel = isImageGenerationModel(model);
  const embeddingModel = isEmbeddingModel(model);
  const asrModel = isASRModel(model);
  const runtimeManagedModel = imageGenerationModel || asrModel;

  return (
    <div class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 px-4">
      <form
        class="w-full max-w-2xl bg-white rounded-2xl shadow-xl border border-gray-200 overflow-hidden"
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
      >
        <div class="px-6 py-5 border-b border-gray-100">
          <h2 class="text-lg font-semibold text-gray-900">{t("lib.runParamsTitle")}</h2>
          <p class="text-sm text-gray-500 mt-1">
            {imageGenerationModel
              ? t("lib.runParamsDescImage", model.name)
              : asrModel
                ? t("lib.runParamsDescASR", model.name)
              : embeddingModel
                ? t("lib.runParamsDescEmbedding", model.name)
                : t("lib.runParamsDesc", model.name)}
          </p>
        </div>

        <div class="px-6 py-5 grid grid-cols-1 md:grid-cols-2 gap-4">
          {runtimeManagedModel ? (
            <div class="md:col-span-2 rounded-lg border border-indigo-100 bg-indigo-50 px-3 py-2 text-sm text-indigo-800">
              {imageGenerationModel ? t("lib.runParamImageRuntimeHint") : t("lib.runParamASRRuntimeHint")}
            </div>
          ) : (
            <>
              <RunNumberField
                label={t("lib.runParamNumCtx")}
                value={params.numCtx}
                min={1024}
                placeholder="131072"
                hint={t("lib.runParamNumCtxHint")}
                onInput={(value) => onChange("numCtx", value)}
              />
              {!embeddingModel && (
                <RunNumberField
                  label={t("lib.runParamNumParallel")}
                  value={params.numParallel}
                  min={1}
                  placeholder="1"
                  hint={t("lib.runParamNumParallelHint")}
                  onInput={(value) => onChange("numParallel", value)}
                />
              )}
              <RunNumberField
                label={t("lib.runParamNGPULayers")}
                value={params.nGpuLayers}
                min={0}
                placeholder="40"
                hint={t("lib.runParamNGPULayersHint")}
                onInput={(value) => onChange("nGpuLayers", value)}
              />
              <RunSelectField
                label={t("lib.runParamDType")}
                value={params.dtype}
                options={DTYPE_OPTIONS}
                hint={t("lib.runParamDTypeHint")}
                onInput={(value) => onChange("dtype", value)}
              />
              {!embeddingModel && (
                <>
                  <RunSelectField
                    label={t("lib.runParamCacheTypeK")}
                    value={params.cacheTypeK}
                    options={CACHE_TYPE_OPTIONS}
                    hint={t("lib.runParamCacheTypeHint")}
                    onInput={(value) => onChange("cacheTypeK", value)}
                  />
                  <RunSelectField
                    label={t("lib.runParamCacheTypeV")}
                    value={params.cacheTypeV}
                    options={CACHE_TYPE_OPTIONS}
                    hint={t("lib.runParamCacheTypeHint")}
                    onInput={(value) => onChange("cacheTypeV", value)}
                  />
                </>
              )}
            </>
          )}
          <div class="md:col-span-2">
            <label class="block text-sm font-medium text-gray-700 mb-1">{t("lib.runParamKeepAlive")}</label>
            <input
              type="text"
              value={params.keepAlive}
              onInput={(e) => onChange("keepAlive", (e.currentTarget as HTMLInputElement).value)}
              placeholder="5m, 1h, -1"
              class="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
            />
            <p class="text-xs text-gray-400 mt-1">{t("lib.runParamKeepAliveHint")}</p>
          </div>
        </div>

        {error && <div class="mx-6 mb-4 text-sm text-red-700 bg-red-50 border border-red-200 rounded-lg px-3 py-2">{error}</div>}

        <div class="px-6 py-4 bg-gray-50 border-t border-gray-100 flex justify-end gap-3">
          <button
            type="button"
            onClick={onCancel}
            disabled={disabled}
            class="px-4 py-2 text-sm rounded-lg border border-gray-200 text-gray-600 hover:bg-white disabled:opacity-50"
          >
            {t("lib.runParamCancel")}
          </button>
          <button
            type="submit"
            disabled={disabled}
            class="px-4 py-2 text-sm rounded-lg bg-indigo-600 text-white hover:bg-indigo-700 disabled:opacity-50"
          >
            {t("lib.runParamSubmit")}
          </button>
        </div>
      </form>
    </div>
  );
}

function RunNumberField({
  label,
  value,
  min,
  placeholder,
  hint,
  onInput,
}: {
  label: string;
  value: string;
  min: number;
  placeholder: string;
  hint: string;
  onInput: (value: string) => void;
}) {
  return (
    <div>
      <label class="block text-sm font-medium text-gray-700 mb-1">{label}</label>
      <input
        type="number"
        min={min}
        step={1}
        value={value}
        onInput={(e) => onInput((e.currentTarget as HTMLInputElement).value)}
        placeholder={placeholder}
        class="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
      />
      <p class="text-xs text-gray-400 mt-1">{hint}</p>
    </div>
  );
}

function RunSelectField({
  label,
  value,
  options,
  hint,
  onInput,
}: {
  label: string;
  value: string;
  options: string[];
  hint: string;
  onInput: (value: string) => void;
}) {
  return (
    <div>
      <label class="block text-sm font-medium text-gray-700 mb-1">{label}</label>
      <select
        value={value}
        onInput={(e) => onInput((e.currentTarget as HTMLSelectElement).value)}
        class="w-full px-3 py-2 border border-gray-200 rounded-lg text-sm bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
      >
        <option value="">{t("lib.runParamDefault")}</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
      <p class="text-xs text-gray-400 mt-1">{hint}</p>
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

function fmtSize(bytes: number): string {
  if (bytes === 0) return "0 B";
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return `${gb.toFixed(1)}GB`;
  const mb = bytes / (1024 * 1024);
  return `${mb.toFixed(0)}MB`;
}

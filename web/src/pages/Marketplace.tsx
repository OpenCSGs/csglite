import { useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { signal, computed } from "@preact/signals";
import {
  getMarketplaceModels,
  getMarketplaceModelExtras,
  getMarketplaceDatasets,
  getMarketplaceModelDetail,
  getTags,
  getDatasetTags,
  getSettings,
  saveSettings,
} from "../api/client";
import type { ArtifactSource, MarketplaceModel, MarketplaceDataset, MarketplaceModelQuantization, MarketplaceTag } from "../api/client";
import { t, locale } from "../i18n";
import { startDownload, getDownloadTask } from "../downloads";
import { normalizeMarketplaceModelSource } from "../modelSources";
import { datasetMarketplaceMetadata } from "../datasetMetadata";
import {
  isProviderMarketplaceSource,
  providerDatasetCardInfo,
  providerModelCardInfo,
} from "../marketplaceCardInfo";
import { ArtifactOwnerAvatar } from "../components/ArtifactOwnerAvatar";
import { MarketplaceDatasetDetailDialog } from "../components/MarketplaceDatasetDetailDialog";
import {
  MarketplaceModelDetailDialog,
  getMarketplaceModelFormats,
} from "../components/MarketplaceModelDetailDialog";

type Tab = "models" | "datasets";
type ViewMode = "grid" | "list";
type ModelFrameworkFilter = "" | "gguf" | "safetensors";
type ModelTaskFilter = "" | "text-generation" | "feature-extraction" | "sentence-similarity" | "automatic-speech-recognition" | "text-to-image" | "image-to-image";
type FilterOption<T extends string> = {
  value: T;
  label: string;
};
type GGUFQuantSelection = {
  modelPath: string;
  quantizations: MarketplaceModelQuantization[];
  artifactSource: ArtifactSource;
};
const modelParamsMinLimit = 0;
const modelParamsMaxLimit = 1000;
const modelParamsSliderMinLimit = 1;
const modelParamsSliderMaxLimit = 100;
const modelParamsQuickRanges = [
  { key: "under-1", label: "mp.modelSizeUnder1B", min: modelParamsMinLimit, max: 0.99999 },
  { key: "2", label: "mp.modelSizeB", min: 2, max: 2 },
  { key: "3", label: "mp.modelSizeB", min: 3, max: 3 },
  { key: "6", label: "mp.modelSizeB", min: 6, max: 6 },
  { key: "12", label: "mp.modelSizeB", min: 12, max: 12 },
  { key: "32", label: "mp.modelSizeB", min: 32, max: 32 },
  { key: "over-100", label: "mp.modelSizeOver100B", min: modelParamsSliderMaxLimit, max: modelParamsMaxLimit },
];
const modelFrameworkOptions: FilterOption<ModelFrameworkFilter>[] = [
  { value: "", label: "mp.allModelTypes" },
  { value: "gguf", label: "mp.modelTypeGGUF" },
  { value: "safetensors", label: "mp.modelTypeSafeTensors" },
];
const modelTaskOptions: FilterOption<ModelTaskFilter>[] = [
  { value: "", label: "mp.allTaskTypes" },
  { value: "text-generation", label: "mp.taskTextGeneration" },
  { value: "feature-extraction", label: "mp.taskFeatureExtraction" },
  { value: "sentence-similarity", label: "mp.taskSentenceSimilarity" },
  { value: "automatic-speech-recognition", label: "mp.taskASR" },
  { value: "text-to-image", label: "mp.taskTextToImage" },
  { value: "image-to-image", label: "mp.taskImageToImage" },
];
const datasetTaskOptions: FilterOption<string>[] = [
  { value: "", label: "mp.allDatasetTasks" },
  { value: "text-classification", label: "mp.datasetTaskTextClassification" },
  { value: "question-answering", label: "mp.datasetTaskQuestionAnswering" },
  { value: "text-generation", label: "mp.taskTextGeneration" },
  { value: "image-classification", label: "mp.datasetTaskImageClassification" },
  { value: "automatic-speech-recognition", label: "mp.taskASR" },
];
const datasetLanguageOptions: FilterOption<string>[] = [
  { value: "", label: "mp.allDatasetLanguages" },
  { value: "en", label: "mp.datasetLanguageEnglish" },
  { value: "zh", label: "mp.datasetLanguageChinese" },
  { value: "multilingual", label: "mp.datasetLanguageMultilingual" },
];
const datasetLicenseOptions: FilterOption<string>[] = [
  { value: "", label: "mp.allDatasetLicenses" },
  { value: "apache-2.0", label: "mp.datasetLicenseApache" },
  { value: "mit", label: "mp.datasetLicenseMIT" },
  { value: "cc-by-4.0", label: "mp.datasetLicenseCCBY" },
  { value: "cc0-1.0", label: "mp.datasetLicenseCC0" },
];
const activeTab = signal<Tab>("models");
const artifactSource = signal<ArtifactSource>("opencsg");
const datasetArtifactSource = signal<ArtifactSource>("opencsg");
const artifactSourceReady = signal(false);
const searchQuery = signal("");
const sortBy = signal("trending");
const frameworkFilter = signal<ModelFrameworkFilter>("");
const taskFilter = signal<ModelTaskFilter>("");
const datasetTaskFilter = signal("");
const datasetLanguageFilter = signal("");
const datasetLicenseFilter = signal("");
const modelParamsMin = signal(modelParamsMinLimit);
const modelParamsMax = signal(modelParamsMaxLimit);
const viewMode = signal<ViewMode>("grid");
const page = signal(1);
const perPage = 16;

const models = signal<MarketplaceModel[]>([]);
const datasets = signal<MarketplaceDataset[]>([]);
const total = signal(0);
const hasMore = signal(false);
const totalExact = signal(true);
const loading = signal(false);

const localModelNames = signal<Set<string>>(new Set());
const localDatasetNames = signal<Set<string>>(new Set());
let loadDataRequestID = 0;

function localModelKey(source: ArtifactSource, name: string): string {
  return `${source}:${name}`;
}

function localDatasetKey(source: ArtifactSource, name: string): string {
  return `${source}:${name}`;
}

function marketplaceDatasetRevision(dataset: MarketplaceDataset, source: ArtifactSource): string | undefined {
  return source === "opencsg" ? undefined : dataset.revision || dataset.default_branch || undefined;
}

function loadLocalModels() {
  getTags().then((m) => {
    localModelNames.value = new Set(
      m.filter((x) => x.source === "local")
        .map((x) => localModelKey(x.artifact_source || "opencsg", x.repository || x.name)),
    );
  }).catch(() => {});
}

function loadLocalDatasets() {
  getDatasetTags().then((d) => {
    localDatasetNames.value = new Set(d.map((x) => localDatasetKey(x.artifact_source || "opencsg", x.repository || x.name)));
  }).catch(() => {});
}

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / perPage)));

async function loadData() {
  if (!artifactSourceReady.value) return;
  const requestID = ++loadDataRequestID;
  loading.value = true;
  try {
    if (activeTab.value === "models") {
      const supportsModelParamsFilter = artifactSource.value === "opencsg";
      const modelParamsRangeActive = supportsModelParamsFilter &&
        (modelParamsMin.value > modelParamsMinLimit || modelParamsMax.value < modelParamsMaxLimit);
      const res = await getMarketplaceModels({
        search: searchQuery.value,
        sort: sortBy.value,
        framework: frameworkFilter.value || undefined,
        task: taskFilter.value || undefined,
        modelParamsMin: supportsModelParamsFilter && modelParamsMin.value > modelParamsMinLimit ? String(modelParamsMin.value) : undefined,
        modelParamsMax: modelParamsRangeActive ? formatModelParamsMax(modelParamsMax.value) : undefined,
        page: page.value,
        per: perPage,
        artifactSource: artifactSource.value,
      });
      if (requestID !== loadDataRequestID) return;
      models.value = res.data || [];
      total.value = res.total;
      hasMore.value = res.has_more ?? false;
      totalExact.value = res.total_exact ?? true;
      const repoIDs = models.value
        .map((model) => model.repository_id || 0)
        .filter((id) => id > 0);
      if (repoIDs.length > 0) {
        void getMarketplaceModelExtras(repoIDs).then((extras) => {
          if (requestID !== loadDataRequestID) return;
          const sizes = new Map(extras.map((extra) => [extra.repo_id, extra.size]));
          models.value = models.value.map((model) => ({
            ...model,
            repo_size: sizes.get(model.repository_id || 0) ?? model.repo_size,
          }));
        }).catch(() => {});
      }
    } else {
      const res = await getMarketplaceDatasets({
        search: searchQuery.value,
        sort: sortBy.value,
        task: datasetArtifactSource.value === "opencsg" ? undefined : datasetTaskFilter.value || undefined,
        language: datasetArtifactSource.value === "opencsg" ? undefined : datasetLanguageFilter.value || undefined,
        license: datasetArtifactSource.value === "opencsg" ? undefined : datasetLicenseFilter.value || undefined,
        page: page.value,
        per: perPage,
        artifactSource: datasetArtifactSource.value,
      });
      if (requestID !== loadDataRequestID) return;
      datasets.value = res.data || [];
      total.value = res.total;
      hasMore.value = res.has_more ?? false;
      totalExact.value = res.total_exact ?? true;
    }
  } catch {
    /* ignore */
  } finally {
    if (requestID === loadDataRequestID) {
      loading.value = false;
    }
  }
}

export function Marketplace() {
  void locale.value;
  const [selectedModelPath, setSelectedModelPath] = useState("");
  const [selectedDatasetPath, setSelectedDatasetPath] = useState("");
  const [ggufSelection, setGGUFSelection] = useState<GGUFQuantSelection | null>(null);
  const [datasetDownloadConfirmation, setDatasetDownloadConfirmation] = useState<MarketplaceDataset | null>(null);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const filtersRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    loadLocalModels();
    loadLocalDatasets();
    let cancelled = false;
    getSettings()
      .then((settings) => {
        if (cancelled) return;
        artifactSource.value = normalizeMarketplaceModelSource(settings.marketplace_model_source);
        datasetArtifactSource.value = normalizeMarketplaceModelSource(settings.marketplace_dataset_source);
      })
      .catch(() => {
        if (!cancelled) artifactSource.value = "opencsg";
      })
      .finally(() => {
        if (!cancelled) artifactSourceReady.value = true;
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    page.value = 1;
    loadData();
  }, [artifactSourceReady.value, activeTab.value, artifactSource.value, datasetArtifactSource.value, sortBy.value, frameworkFilter.value, taskFilter.value, datasetTaskFilter.value, datasetLanguageFilter.value, datasetLicenseFilter.value, modelParamsMin.value, modelParamsMax.value]);

  useEffect(() => {
    setSelectedModelPath("");
    setSelectedDatasetPath("");
    setGGUFSelection(null);
    setDatasetDownloadConfirmation(null);
    if (activeTab.value !== "models") {
      setFiltersOpen(false);
    }
  }, [activeTab.value, artifactSource.value, datasetArtifactSource.value]);

  useEffect(() => {
    if (!filtersOpen) return;

    const handlePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && filtersRef.current?.contains(target)) {
        return;
      }
      setFiltersOpen(false);
    };

    document.addEventListener("pointerdown", handlePointerDown);
    return () => document.removeEventListener("pointerdown", handlePointerDown);
  }, [filtersOpen]);

  const handleSearch = (e: Event) => {
    e.preventDefault();
    page.value = 1;
    loadData();
  };

  const selectArtifactSource = (source: ArtifactSource) => {
    const target = activeTab.value === "models" ? artifactSource : datasetArtifactSource;
    const previous = target.value;
    if (source === previous) return;
    target.value = source;
    page.value = 1;
    if (activeTab.value === "models" && source !== "opencsg") {
      modelParamsMin.value = modelParamsMinLimit;
      modelParamsMax.value = modelParamsMaxLimit;
    }
    const patch = activeTab.value === "models"
      ? { marketplace_model_source: source }
      : { marketplace_dataset_source: source };
    void saveSettings(patch).catch(() => {
      if (target.value === source) {
        target.value = previous;
        page.value = 1;
      }
    });
  };

  const beginModelDownload = (modelPath: string, quants?: string[], source = artifactSource.value) => {
    startDownload("model", modelPath, () => {
      loadLocalModels();
    }, { quants, artifactSource: source });
  };

  const handleDownload = (modelPath: string) => {
    const source = artifactSource.value;
    getMarketplaceModelDetail(modelPath, { artifactSource: source }).then((detail) => {
      const quants = detail.quantizations || [];
      if (quants.length > 1) {
        setGGUFSelection({ modelPath, quantizations: quants, artifactSource: source });
        return;
      }
      beginModelDownload(modelPath, quants.length === 1 ? [quants[0].name] : undefined, source);
    }).catch(() => {
      beginModelDownload(modelPath, undefined, source);
    });
  };

  const handleConfirmGGUFDownload = (quants: string[]) => {
    if (!ggufSelection) return;
    const modelPath = ggufSelection.modelPath;
    setGGUFSelection(null);
    beginModelDownload(modelPath, quants, ggufSelection.artifactSource);
  };

  const handleDatasetDownload = (dataset: MarketplaceDataset) => {
    setDatasetDownloadConfirmation(dataset);
  };

  const confirmDatasetDownload = () => {
    const dataset = datasetDownloadConfirmation;
    if (!dataset) return;
    setDatasetDownloadConfirmation(null);
    const source = dataset.artifact_source || datasetArtifactSource.value;
    const revision = marketplaceDatasetRevision(dataset, source);
    const datasetPath = dataset.path;
    startDownload("dataset", datasetPath, () => {
      loadLocalDatasets();
    }, { artifactSource: source, revision });
  };
  const hasModelFilters = frameworkFilter.value !== ""
    || taskFilter.value !== ""
    || (artifactSource.value === "opencsg" &&
      (modelParamsMin.value !== modelParamsMinLimit || modelParamsMax.value !== modelParamsMaxLimit));
  const clearModelFilters = () => {
    frameworkFilter.value = "";
    taskFilter.value = "";
    modelParamsMin.value = modelParamsMinLimit;
    modelParamsMax.value = modelParamsMaxLimit;
  };
  const selectedDataset = datasets.value.find((dataset) => dataset.path === selectedDatasetPath);

  return (
    <div class="page-shell">
      <div class="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 class="text-2xl font-bold text-gray-900">{t("mp.title")}</h1>
          <p class="mt-1 text-sm text-gray-500">{t("mp.subtitle")}</p>
        </div>
        <div class="inline-flex w-fit items-center gap-2 rounded-xl border border-gray-200 bg-white p-1.5 shadow-sm">
            <span class="pl-2 text-xs font-medium text-gray-500">{t("mp.artifactSource")}</span>
            <div class="flex rounded-lg bg-gray-100 p-0.5" role="group" aria-label={t("mp.artifactSource")}>
              {([
                ["opencsg", t("mp.sourceOpenCSG")],
                ["huggingface", t("mp.sourceHuggingFace")],
                ["modelscope", t("mp.sourceModelScope")],
              ] as Array<[ArtifactSource, string]>).map(([source, label]) => (
                <button
                  key={source}
                  type="button"
                  aria-pressed={(activeTab.value === "models" ? artifactSource.value : datasetArtifactSource.value) === source}
                  disabled={!artifactSourceReady.value}
                  onClick={() => selectArtifactSource(source)}
                  class={`rounded-md px-3 py-1.5 text-xs font-semibold transition-all ${
                    (activeTab.value === "models" ? artifactSource.value : datasetArtifactSource.value) === source
                      ? marketplaceSourceActiveClass(source)
                      : "text-gray-500 hover:bg-white hover:text-gray-800"
                  }`}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
      </div>

      {/* Tabs + Search + View Toggle */}
      <div class="flex items-center gap-3 mb-6 flex-wrap">
        <div class="flex bg-gray-100 rounded-lg p-0.5">
          <TabButton label={t("mp.models")} active={activeTab.value === "models"} onClick={() => (activeTab.value = "models")} />
          <TabButton label={t("mp.datasets")} active={activeTab.value === "datasets"} onClick={() => (activeTab.value = "datasets")} />
        </div>
        <form onSubmit={handleSearch} class="flex-1 min-w-[200px]">
          <div class="relative">
            <svg class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z" />
            </svg>
            <input
              type="text"
              placeholder={t("mp.search")}
              class="w-full pl-10 pr-4 py-2 border border-gray-200 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500 focus:border-transparent"
              value={searchQuery.value}
              onInput={(e) => (searchQuery.value = (e.target as HTMLInputElement).value)}
            />
          </div>
        </form>
        <div class="relative">
          <select
            class="appearance-none border border-gray-200 rounded-lg pl-8 pr-3 py-2 text-sm text-gray-600 focus:outline-none focus:ring-2 focus:ring-indigo-500"
            value={sortBy.value}
            onChange={(e) => (sortBy.value = (e.target as HTMLSelectElement).value)}
          >
            <option value="trending">{t("mp.trending")}</option>
            <option value="recently_update">{t("mp.recentlyUpdated")}</option>
            <option value="most_download">{t("mp.mostDownloads")}</option>
            <option value="most_favorite">{t("mp.mostLikes")}</option>
          </select>
          <svg class="absolute left-2.5 top-1/2 -translate-y-1/2 w-4 h-4 text-gray-400 pointer-events-none" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3 4h13M3 8h9m-9 4h6m4 0l4-4m0 0l4 4m-4-4v12" />
          </svg>
        </div>
        {activeTab.value === "models" && (
          <div ref={filtersRef} class="relative">
            <button
              type="button"
              onClick={() => setFiltersOpen((open) => !open)}
              class={`inline-flex items-center gap-1.5 px-3 py-2 text-sm border rounded-lg transition-colors ${
                filtersOpen || hasModelFilters
                  ? "bg-indigo-50 text-indigo-700 border-indigo-200"
                  : "bg-white text-gray-600 border-gray-200 hover:bg-gray-50"
              }`}
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" />
              </svg>
              {t("mp.filter")}
              {hasModelFilters && <span class="w-1.5 h-1.5 rounded-full bg-indigo-500" />}
            </button>
            {filtersOpen && (
              <div class="absolute right-0 z-20 mt-2 w-96 max-w-[calc(100vw-2rem)] rounded-2xl border border-gray-200 bg-white p-4 shadow-xl">
                <div class="flex items-center justify-between gap-3 mb-4">
                  <div>
                    <div class="text-sm font-semibold text-gray-900">{t("mp.filter")}</div>
                    <div class="text-xs text-gray-500 mt-0.5">
                      {hasModelFilters ? t("mp.activeFilters") : t("mp.noActiveFilters")}
                    </div>
                  </div>
                  {hasModelFilters && (
                    <button
                      type="button"
                      onClick={clearModelFilters}
                      class="px-2.5 py-1.5 text-xs font-medium text-gray-500 hover:text-gray-900 hover:bg-gray-50 border border-gray-200 rounded-full transition-colors"
                    >
                      {t("mp.clearFilters")}
                    </button>
                  )}
                </div>
                <div class="space-y-5">
                  <FilterPillGroup
                    label={t("mp.modelType")}
                    value={frameworkFilter.value}
                    options={modelFrameworkOptions.map((option) => ({ ...option, label: t(option.label) }))}
                    onChange={(value) => (frameworkFilter.value = value)}
                  />
                  <FilterPillGroup
                    label={t("mp.taskType")}
                    value={taskFilter.value}
                    options={modelTaskOptions.map((option) => ({ ...option, label: t(option.label) }))}
                    onChange={(value) => (taskFilter.value = value)}
                  />
                  {artifactSource.value === "opencsg" && (
                    <ModelParamsRangeSlider
                      min={modelParamsMin.value}
                      max={modelParamsMax.value}
                      onMinChange={(value) => {
                        modelParamsMin.value = Math.min(value, modelParamsMax.value);
                      }}
                      onMaxChange={(value) => {
                        modelParamsMax.value = Math.max(value, modelParamsMin.value);
                      }}
                      onRangeChange={(min, max) => {
                        modelParamsMin.value = min;
                        modelParamsMax.value = max;
                      }}
                    />
                  )}
                </div>
              </div>
            )}
          </div>
        )}
        {activeTab.value === "datasets" && datasetArtifactSource.value !== "opencsg" && (
          <div class="flex items-center gap-2">
            <DatasetFilterSelect label={t("mp.taskType")} value={datasetTaskFilter.value} options={datasetTaskOptions.map((option) => ({ ...option, label: t(option.label) }))} onChange={(value) => (datasetTaskFilter.value = value)} />
            <DatasetFilterSelect label={t("mp.languages")} value={datasetLanguageFilter.value} options={datasetLanguageOptions.map((option) => ({ ...option, label: t(option.label) }))} onChange={(value) => (datasetLanguageFilter.value = value)} />
            <DatasetFilterSelect label={t("ds.licenseLabel")} value={datasetLicenseFilter.value} options={datasetLicenseOptions.map((option) => ({ ...option, label: t(option.label) }))} onChange={(value) => (datasetLicenseFilter.value = value)} />
          </div>
        )}
        <div class="flex border border-gray-200 rounded-lg overflow-hidden">
          <button
            onClick={() => (viewMode.value = "grid")}
            class={`p-2 ${viewMode.value === "grid" ? "bg-indigo-50 text-indigo-600" : "text-gray-400 hover:bg-gray-50"}`}
          >
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 16 16">
              <path d="M1 2.5A1.5 1.5 0 012.5 1h3A1.5 1.5 0 017 2.5v3A1.5 1.5 0 015.5 7h-3A1.5 1.5 0 011 5.5v-3zm8 0A1.5 1.5 0 0110.5 1h3A1.5 1.5 0 0115 2.5v3A1.5 1.5 0 0113.5 7h-3A1.5 1.5 0 019 5.5v-3zm-8 8A1.5 1.5 0 012.5 9h3A1.5 1.5 0 017 10.5v3A1.5 1.5 0 015.5 15h-3A1.5 1.5 0 011 13.5v-3zm8 0A1.5 1.5 0 0110.5 9h3a1.5 1.5 0 011.5 1.5v3a1.5 1.5 0 01-1.5 1.5h-3A1.5 1.5 0 019 13.5v-3z" />
            </svg>
          </button>
          <button
            onClick={() => (viewMode.value = "list")}
            class={`p-2 ${viewMode.value === "list" ? "bg-indigo-50 text-indigo-600" : "text-gray-400 hover:bg-gray-50"}`}
          >
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 16 16">
              <path fill-rule="evenodd" d="M2.5 12a.5.5 0 01.5-.5h10a.5.5 0 010 1H3a.5.5 0 01-.5-.5zm0-4a.5.5 0 01.5-.5h10a.5.5 0 010 1H3a.5.5 0 01-.5-.5zm0-4a.5.5 0 01.5-.5h10a.5.5 0 010 1H3a.5.5 0 01-.5-.5z" />
            </svg>
          </button>
        </div>
      </div>

      {/* Content */}
      {loading.value ? (
        <div class="text-center py-16 text-gray-400">{t("mp.loading")}</div>
      ) : activeTab.value === "models" ? (
        viewMode.value === "grid" ? (
          <div class="grid grid-cols-2 gap-4 2xl:grid-cols-3">
            {models.value.map((m) => (
              <ModelGridCard
                key={m.path}
                model={m}
                pulling={getDownloadTask("model", m.path, { artifactSource: artifactSource.value })}
                isLocal={localModelNames.value.has(localModelKey(artifactSource.value, m.path))}
                onDownload={handleDownload}
                onOpenDetail={setSelectedModelPath}
              />
            ))}
            {models.value.length === 0 && <p class="col-span-2 text-center py-16 text-gray-400">{t("mp.noModels")}</p>}
          </div>
        ) : (
          <div class={isProviderMarketplaceSource(artifactSource.value) ? "space-y-3" : "space-y-0 divide-y divide-gray-100"}>
            {models.value.map((m) => (
              <ModelCard
                key={m.path}
                model={m}
                pulling={getDownloadTask("model", m.path, { artifactSource: artifactSource.value })}
                isLocal={localModelNames.value.has(localModelKey(artifactSource.value, m.path))}
                onDownload={handleDownload}
                onOpenDetail={setSelectedModelPath}
              />
            ))}
            {models.value.length === 0 && <p class="text-center py-16 text-gray-400">{t("mp.noModels")}</p>}
          </div>
        )
      ) : viewMode.value === "grid" ? (
        <div class="grid grid-cols-2 gap-4 2xl:grid-cols-3">
          {datasets.value.map((d) => (
            <DatasetGridCard
              key={`${datasetArtifactSource.value}:${d.path}`}
              dataset={d}
              pulling={getDownloadTask("dataset", d.path, { artifactSource: datasetArtifactSource.value, revision: marketplaceDatasetRevision(d, datasetArtifactSource.value) })}
              isLocal={localDatasetNames.value.has(localDatasetKey(datasetArtifactSource.value, d.path))}
              onDownload={handleDatasetDownload}
              onOpenDetail={() => setSelectedDatasetPath(d.path)}
            />
          ))}
          {datasets.value.length === 0 && <p class="col-span-2 text-center py-16 text-gray-400">{t("mp.noDatasets")}</p>}
        </div>
      ) : (
        <div class={isProviderMarketplaceSource(datasetArtifactSource.value) ? "space-y-3" : "space-y-0 divide-y divide-gray-100"}>
          {datasets.value.map((d) => (
            <DatasetCard
              key={`${datasetArtifactSource.value}:${d.path}`}
              dataset={d}
              pulling={getDownloadTask("dataset", d.path, { artifactSource: datasetArtifactSource.value, revision: marketplaceDatasetRevision(d, datasetArtifactSource.value) })}
              isLocal={localDatasetNames.value.has(localDatasetKey(datasetArtifactSource.value, d.path))}
              onDownload={handleDatasetDownload}
              onOpenDetail={() => setSelectedDatasetPath(d.path)}
            />
          ))}
          {datasets.value.length === 0 && <p class="text-center py-16 text-gray-400">{t("mp.noDatasets")}</p>}
        </div>
      )}

      {/* Pagination */}
      {(totalExact.value ? totalPages.value > 1 : page.value > 1 || hasMore.value) && (
        <div class="flex items-center justify-center gap-2 mt-8">
          <button
            disabled={page.value <= 1}
            onClick={() => { page.value--; loadData(); }}
            class="px-3 py-1.5 text-sm border border-gray-200 rounded-lg disabled:opacity-40 hover:bg-gray-50"
          >
            {t("mp.prev")}
          </button>
          <span class="text-sm text-gray-500">
            {totalExact.value ? t("mp.page", page.value, totalPages.value) : t("mp.pageCurrent", page.value)}
          </span>
          <button
            disabled={totalExact.value ? page.value >= totalPages.value : !hasMore.value}
            onClick={() => { page.value++; loadData(); }}
            class="px-3 py-1.5 text-sm border border-gray-200 rounded-lg disabled:opacity-40 hover:bg-gray-50"
          >
            {t("mp.next")}
          </button>
        </div>
      )}

      {selectedModelPath && (
        <MarketplaceModelDetailDialog
          modelPath={selectedModelPath}
          artifactSource={artifactSource.value}
          isLocal={localModelNames.value.has(localModelKey(artifactSource.value, selectedModelPath))}
          pulling={getDownloadTask("model", selectedModelPath, { artifactSource: artifactSource.value })}
          onDownload={handleDownload}
          onClose={() => setSelectedModelPath("")}
        />
      )}

      {selectedDatasetPath && (
        <MarketplaceDatasetDetailDialog
          datasetPath={selectedDatasetPath}
          artifactSource={datasetArtifactSource.value}
          revision={selectedDataset ? marketplaceDatasetRevision(selectedDataset, datasetArtifactSource.value) : undefined}
          isLocal={localDatasetNames.value.has(localDatasetKey(datasetArtifactSource.value, selectedDatasetPath))}
          pulling={getDownloadTask("dataset", selectedDatasetPath, {
            artifactSource: datasetArtifactSource.value,
            revision: selectedDataset ? marketplaceDatasetRevision(selectedDataset, datasetArtifactSource.value) : undefined,
          })}
          onDownload={handleDatasetDownload}
          onClose={() => setSelectedDatasetPath("")}
        />
      )}

      {ggufSelection && (
        <GGUFQuantSelectionDialog
          selection={ggufSelection}
          onConfirm={handleConfirmGGUFDownload}
          onClose={() => setGGUFSelection(null)}
        />
      )}

      {datasetDownloadConfirmation && (
        <DatasetDownloadConfirmationDialog
          dataset={datasetDownloadConfirmation}
          onConfirm={confirmDatasetDownload}
          onClose={() => setDatasetDownloadConfirmation(null)}
        />
      )}

    </div>
  );
}

function TabButton({ label, active, onClick }: { label: string; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      class={`px-4 py-1.5 text-sm font-medium rounded-md transition-colors ${
        active ? "bg-white text-gray-900 shadow-sm" : "text-gray-500 hover:text-gray-700"
      }`}
    >
      {label}
    </button>
  );
}

function DatasetDownloadConfirmationDialog({
  dataset,
  onConfirm,
  onClose,
}: {
  dataset: MarketplaceDataset;
  onConfirm: () => void;
  onClose: () => void;
}) {
  void locale.value;
  const knownSize = formatRepoSize(dataset.repo_size || 0);
  const source = dataset.artifact_source || datasetArtifactSource.value;
  const partialPath = source === "opencsg"
    ? `<dataset_dir>/${dataset.path}`
    : `<dataset_dir>/.registries/${source}/${dataset.path}`;
  return (
    <div class="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 px-4 py-6" onClick={(event) => {
      if (event.target === event.currentTarget) onClose();
    }}>
      <section class="w-full max-w-lg rounded-2xl bg-white p-6 shadow-2xl">
        <h2 class="text-lg font-semibold text-gray-900">{t("mp.datasetSnapshotConfirmTitle")}</h2>
        <p class="mt-2 text-sm leading-6 text-gray-600">{t("mp.datasetSnapshotConfirmDescription")}</p>
        <dl class="mt-4 rounded-xl border border-gray-200 bg-gray-50 p-4 text-sm">
          <div class="flex gap-4">
            <dt class="w-24 flex-shrink-0 text-gray-400">{t("mp.datasetId")}</dt>
            <dd class="break-all font-medium text-gray-800">{dataset.path}</dd>
          </div>
          <div class="mt-2 flex gap-4">
            <dt class="w-24 flex-shrink-0 text-gray-400">{t("mp.repoSize")}</dt>
            <dd class="font-medium text-gray-800">{knownSize || t("mp.unknownSize")}</dd>
          </div>
          <div class="mt-2 flex gap-4">
            <dt class="w-24 flex-shrink-0 text-gray-400">{t("mp.partialStoragePath")}</dt>
            <dd class="break-all font-mono text-xs text-gray-700">{partialPath}</dd>
          </div>
        </dl>
        <p class="mt-4 rounded-lg bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800">
          {t("mp.datasetSnapshotPartialHint")}
        </p>
        <div class="mt-6 flex justify-end gap-3">
          <button type="button" onClick={onClose} class="rounded-lg border border-gray-200 px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50">
            {t("ds.cancel")}
          </button>
          <button type="button" onClick={onConfirm} class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700">
            {t("mp.downloadFullSnapshot")}
          </button>
        </div>
      </section>
    </div>
  );
}

function GGUFQuantSelectionDialog({
  selection,
  onConfirm,
  onClose,
}: {
  selection: GGUFQuantSelection;
  onConfirm: (quants: string[]) => void;
  onClose: () => void;
}) {
  void locale.value;
  const defaultQuant = selection.quantizations[0]?.name || "";
  const [selected, setSelected] = useState<Set<string>>(() => new Set(defaultQuant ? [defaultQuant] : []));
  const selectedList = selection.quantizations.map((item) => item.name).filter((name) => selected.has(name));

  const toggleQuant = (name: string) => {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(name)) {
        next.delete(name);
      } else {
        next.add(name);
      }
      return next;
    });
  };

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4 py-6"
      onClick={(event) => {
        if (event.target === event.currentTarget) {
          onClose();
        }
      }}
    >
      <div class="w-full max-w-lg rounded-2xl bg-white shadow-2xl">
        <div class="flex items-start justify-between gap-4 border-b border-gray-100 px-6 py-5">
          <div class="min-w-0">
            <h2 class="text-lg font-bold text-gray-900">{t("mp.selectGGUFQuantizations")}</h2>
            <p class="mt-1 break-all text-sm text-gray-500">{selection.modelPath}</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            class="inline-flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full text-gray-500 hover:bg-gray-100 hover:text-gray-700"
            aria-label={t("dash.close")}
          >
            x
          </button>
        </div>
        <div class="px-6 py-5">
          <p class="mb-4 text-sm text-gray-500">{t("mp.selectGGUFQuantizationsHint")}</p>
          <div class="max-h-72 space-y-2 overflow-auto pr-1">
            {selection.quantizations.map((item) => {
              const checked = selected.has(item.name);
              return (
                <label
                  key={item.name}
                  class={`flex cursor-pointer items-center justify-between gap-3 rounded-xl border px-4 py-3 transition-colors ${
                    checked
                      ? "border-indigo-200 bg-indigo-50"
                      : "border-gray-200 bg-white hover:border-gray-300"
                  }`}
                >
                  <div class="flex min-w-0 items-center gap-3">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => toggleQuant(item.name)}
                      class="h-4 w-4 rounded border-gray-300 text-indigo-600 focus:ring-indigo-500"
                    />
                    <div class="min-w-0">
                      <div class="font-medium text-gray-900">{item.name}</div>
                      <div class="truncate text-xs text-gray-500" title={item.example_path}>
                        {item.file_count > 1
                          ? t("mp.quantizationFiles", item.file_count)
                          : t("mp.quantizationFile")}
                      </div>
                    </div>
                  </div>
                  {item.name === defaultQuant && (
                    <span class="flex-shrink-0 rounded-full bg-white px-2 py-0.5 text-xs font-medium text-indigo-600">
                      {t("mp.defaultQuantization")}
                    </span>
                  )}
                </label>
              );
            })}
          </div>
        </div>
        <div class="flex items-center justify-end gap-3 border-t border-gray-100 px-6 py-4">
          <button
            type="button"
            onClick={onClose}
            class="rounded-lg border border-gray-200 px-4 py-2 text-sm font-medium text-gray-600 hover:bg-gray-50"
          >
            {t("settings.cancel")}
          </button>
          <button
            type="button"
            disabled={selectedList.length === 0}
            onClick={() => onConfirm(selectedList)}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {t("mp.downloadSelectedQuantizations", selectedList.length)}
          </button>
        </div>
      </div>
    </div>
  );
}

function FilterPillGroup<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: FilterOption<T>[];
  onChange: (value: T) => void;
}) {
  return (
    <div class="flex items-center gap-2 flex-wrap">
      <span class="w-16 text-xs text-gray-500">{label}</span>
      <div class="flex items-center gap-1.5 flex-wrap">
        {options.map((option) => {
          const active = option.value === value;
          return (
            <button
              key={option.value || "all"}
              type="button"
              onClick={() => onChange(option.value)}
              class={`px-2.5 py-1 text-xs font-medium rounded-full border transition-colors ${
                active
                  ? "bg-indigo-50 text-indigo-700 border-indigo-200 shadow-sm"
                  : "bg-white text-gray-600 border-gray-200 hover:border-gray-300 hover:text-gray-900"
              }`}
            >
              {option.label}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function ModelParamsRangeSlider({
  min,
  max,
  onMinChange,
  onMaxChange,
  onRangeChange,
}: {
  min: number;
  max: number;
  onMinChange: (value: number) => void;
  onMaxChange: (value: number) => void;
  onRangeChange: (min: number, max: number) => void;
}) {
  const sliderMin = clampSliderModelParams(min);
  const sliderMax = clampSliderModelParams(max);
  const minPercent = ((sliderMin - modelParamsSliderMinLimit) / (modelParamsSliderMaxLimit - modelParamsSliderMinLimit)) * 100;
  const maxPercent = ((sliderMax - modelParamsSliderMinLimit) / (modelParamsSliderMaxLimit - modelParamsSliderMinLimit)) * 100;
  const rangeLabel = modelParamsRangeLabel(min, max);

  return (
    <div>
      <style>{`
        .marketplace-range-input {
          pointer-events: none;
        }
        .marketplace-range-input::-webkit-slider-runnable-track {
          background: transparent;
        }
        .marketplace-range-input::-webkit-slider-thumb {
          pointer-events: auto;
        }
        .marketplace-range-input::-moz-range-track {
          background: transparent;
        }
        .marketplace-range-input::-moz-range-thumb {
          pointer-events: auto;
        }
      `}</style>
      <div class="flex items-center justify-between gap-3 mb-3">
        <div>
          <div class="text-xs font-medium text-gray-500">{t("mp.modelSizeRange")}</div>
          <div class="text-sm font-semibold text-gray-900 mt-0.5">{rangeLabel}</div>
        </div>
        <div class="text-xs text-gray-400">{t("mp.modelSizeSliderHint", modelParamsSliderMinLimit, modelParamsSliderMaxLimit)}</div>
      </div>
      <div class="relative h-8">
        <div class="absolute left-0 right-0 top-1/2 h-1 -translate-y-1/2 rounded-full bg-gray-200" />
        <div
          class="absolute top-1/2 h-1 -translate-y-1/2 rounded-full bg-indigo-500"
          style={{ left: `${minPercent}%`, right: `${100 - maxPercent}%` }}
        />
        <input
          type="range"
          min={modelParamsSliderMinLimit}
          max={modelParamsSliderMaxLimit}
          step={1}
          value={sliderMin}
          onInput={(e) => onMinChange(Number((e.target as HTMLInputElement).value))}
          aria-label={t("mp.minimum")}
          class="marketplace-range-input absolute inset-x-0 top-1/2 z-20 w-full -translate-y-1/2 appearance-none bg-transparent accent-indigo-600"
        />
        <input
          type="range"
          min={modelParamsSliderMinLimit}
          max={modelParamsSliderMaxLimit}
          step={1}
          value={sliderMax}
          onInput={(e) => onMaxChange(Number((e.target as HTMLInputElement).value))}
          aria-label={t("mp.maximum")}
          class="marketplace-range-input absolute inset-x-0 top-1/2 z-30 w-full -translate-y-1/2 appearance-none bg-transparent accent-indigo-600"
        />
      </div>
      <div class="flex items-center justify-between gap-3 mt-2">
        <label class="flex items-center gap-2 text-xs text-gray-500">
          {t("mp.minimum")}
          <input
            type="number"
            min={modelParamsSliderMinLimit}
            max={modelParamsSliderMaxLimit}
            value={sliderMin}
            onInput={(e) => onMinChange(clampSliderModelParams(Number((e.target as HTMLInputElement).value)))}
            class="w-20 px-2 py-1 border border-gray-200 rounded-lg text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </label>
        <label class="flex items-center gap-2 text-xs text-gray-500">
          {t("mp.maximum")}
          <input
            type="number"
            min={modelParamsSliderMinLimit}
            max={modelParamsSliderMaxLimit}
            value={sliderMax}
            onInput={(e) => onMaxChange(clampSliderModelParams(Number((e.target as HTMLInputElement).value)))}
            class="w-20 px-2 py-1 border border-gray-200 rounded-lg text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-indigo-500"
          />
        </label>
      </div>
      <div class="mt-3">
        <div class="text-xs font-medium text-gray-500 mb-2">{t("mp.quickSizes")}</div>
        <div class="flex flex-wrap gap-1.5">
          {modelParamsQuickRanges.map((range) => {
            const active = min === range.min && max === range.max;
            return (
              <button
                key={range.key}
                type="button"
                onClick={() => onRangeChange(range.min, range.max)}
                class={`px-2.5 py-1 text-xs font-medium rounded-full border transition-colors ${
                  active
                    ? "bg-indigo-50 text-indigo-700 border-indigo-200 shadow-sm"
                    : "bg-white text-gray-600 border-gray-200 hover:border-gray-300 hover:text-gray-900"
                }`}
              >
                {range.label === "mp.modelSizeB" ? t(range.label, range.min) : t(range.label)}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function clampSliderModelParams(value: number): number {
  if (!Number.isFinite(value)) {
    return modelParamsSliderMinLimit;
  }
  return Math.max(modelParamsSliderMinLimit, Math.min(modelParamsSliderMaxLimit, Math.round(value)));
}

function modelParamsRangeLabel(min: number, max: number): string {
  if (min === modelParamsMinLimit && max === modelParamsMaxLimit) {
    return t("mp.modelSizeAny");
  }
  if (min === modelParamsMinLimit && max === 0.99999) {
    return t("mp.modelSizeUnder1B");
  }
  if (min === modelParamsSliderMaxLimit && max === modelParamsMaxLimit) {
    return t("mp.modelSizeOver100B");
  }
  if (min === max && Number.isInteger(min)) {
    return t("mp.modelSizeB", min);
  }
  return t("mp.modelSizeRangeValue", min, max);
}

function formatModelParamsMax(value: number): string {
  if (value >= modelParamsMaxLimit) {
    return String(modelParamsMaxLimit);
  }
  return Number.isInteger(value) ? `${value}.99999` : String(value);
}

function marketplaceSourceActiveClass(source: ArtifactSource): string {
  if (source === "huggingface") return "bg-[#FFD21E] text-gray-900 shadow-sm";
  if (source === "opencsg") return "bg-[#169F95] text-white shadow-sm";
  return "bg-[#624AFF] text-white shadow-sm";
}

function ModelCard({
  model,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  model: MarketplaceModel;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
  onOpenDetail: (path: string) => void;
}) {
  void locale.value;
  if (isProviderMarketplaceSource(model.artifact_source)) {
    return (
      <ProviderModelGridCard
        model={model}
        pulling={pulling}
        isLocal={isLocal}
        onDownload={onDownload}
        onOpenDetail={onOpenDetail}
      />
    );
  }
  const presentation = modelSourcePresentation(model);
  const tags = providerCardTags(model, 3);

  return (
    <div class="flex items-center justify-between py-4">
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          <ModelSourceMark source={presentation.source} modelPath={model.path} />
          <button
            onClick={() => onOpenDetail(model.path)}
            class={`font-medium text-gray-900 transition-colors text-left break-all ${presentation.hoverText}`}
            title={t("mp.viewDetails")}
          >
            {presentation.title}
          </button>
          <ModelFormatBadges model={model} />
          {isLocal && (
            <span class="px-1.5 py-0.5 text-xs bg-indigo-50 text-indigo-600 rounded font-medium">{t("mp.downloaded")}</span>
          )}
        </div>
        {model.description && (
          <p class="text-sm text-gray-500 mt-1 line-clamp-1">{model.description}</p>
        )}
        <div class="flex items-center gap-3 mt-2 text-xs text-gray-400">
          {tags.map((tg) => (
            <span key={`${tg.category}:${tg.name}`} class={`px-2 py-0.5 rounded ${presentation.tagTone}`}>
              {tg.show_name || tg.name}
            </span>
          ))}
          <span>&middot;</span>
          <span>{new Date(model.updated_at).toLocaleDateString()}</span>
          <span>&middot;</span>
          <span class="flex items-center gap-1">
            <DownloadIcon /> {formatDownloadCount(model.downloads)}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {model.likes}
          </span>
          {typeof model.repo_size === "number" && model.repo_size > 0 && (
            <span>{t("mp.repoSizeValue", formatRepoSize(model.repo_size))}</span>
          )}
        </div>
      </div>
      <div class="ml-4 flex-shrink-0 w-28 flex items-center justify-end">
        <ArtifactDownloadAction
          pulling={pulling}
          isLocal={isLocal}
          tone="indigo"
          compact={false}
          onDownload={() => onDownload(model.path)}
        />
      </div>
    </div>
  );
}

function ModelGridCard({
  model,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  model: MarketplaceModel;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
  onOpenDetail: (path: string) => void;
}) {
  void locale.value;
  if (isProviderMarketplaceSource(model.artifact_source)) {
    return (
      <ProviderModelGridCard
        model={model}
        pulling={pulling}
        isLocal={isLocal}
        onDownload={onDownload}
        onOpenDetail={onOpenDetail}
      />
    );
  }
  const presentation = modelSourcePresentation(model);
  const tags = providerCardTags(model, 2);

  return (
    <div class={`rounded-xl border bg-white p-5 flex flex-col justify-between ${presentation.border}`}>
      <div>
        <div class="flex items-center gap-2 mb-2">
          <ModelSourceMark source={presentation.source} modelPath={model.path} />
          <button
            onClick={() => onOpenDetail(model.path)}
            class={`min-w-0 flex-1 font-medium text-gray-900 text-sm truncate text-left transition-colors ${presentation.hoverText}`}
            title={t("mp.viewDetails")}
          >
            {presentation.title}
          </button>
          <ModelFormatBadges model={model} />
        </div>
        <p class="text-sm text-gray-500 line-clamp-2 mb-3 min-h-[2.5rem]">
          {model.description || ""}
        </p>
        <div class="flex items-center gap-2 flex-wrap text-xs text-gray-400">
          {tags.map((tg) => (
            <span key={`${tg.category}:${tg.name}`} class={`px-2 py-0.5 rounded ${presentation.tagTone}`}>
              {tg.show_name || tg.name}
            </span>
          ))}
          <span class="flex items-center gap-1">
            <DownloadIcon /> {formatDownloadCount(model.downloads)}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {model.likes}
          </span>
          {typeof model.repo_size === "number" && model.repo_size > 0 && (
            <span>{t("mp.repoSizeValue", formatRepoSize(model.repo_size))}</span>
          )}
        </div>
      </div>
      <div class="flex items-center justify-between mt-4 pt-3 border-t border-gray-100 text-xs text-gray-400">
        <span class="flex items-center gap-1">
          <ClockIcon />
          {t("mp.updatedAt", new Date(model.updated_at).toLocaleDateString())}
        </span>
        <ArtifactDownloadAction
          pulling={pulling}
          isLocal={isLocal}
          tone="indigo"
          compact
          onDownload={() => onDownload(model.path)}
        />
      </div>
    </div>
  );
}

function modelSourcePresentation(model: MarketplaceModel): {
  source: ArtifactSource;
  title: string;
  border: string;
  hoverText: string;
  tagTone: string;
} {
  return {
    source: model.artifact_source || "opencsg",
    title: model.path,
    border: "border-gray-200 hover:border-gray-300",
    hoverText: "hover:text-indigo-600",
    tagTone: "bg-gray-100 text-gray-600",
  };
}

function providerCardTags(model: MarketplaceModel, limit: number): MarketplaceTag[] {
  const ordered: MarketplaceTag[] = [];
  for (const category of ["task", "license"]) {
    for (const tag of model.tags || []) {
      if (tag.category === category) ordered.push(tag);
    }
  }
  return ordered.slice(0, limit);
}

function ModelSourceMark({ source, modelPath }: { source: ArtifactSource; modelPath: string }) {
  return <ArtifactOwnerAvatar source={source} path={modelPath} />;
}

function ProviderModelGridCard({
  model,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  model: MarketplaceModel;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (path: string) => void;
  onOpenDetail: (path: string) => void;
}) {
  void locale.value;
  const info = providerModelCardInfo(model);
  const displayTitle = info.source === "huggingface" ? info.path : info.title;
  const metadata = [
    ...getMarketplaceModelFormats(model).map(formatModelFormatLabel),
    info.task,
    info.params,
    info.library,
    info.license,
    info.architecture || info.modelType,
  ].filter(Boolean);
  return (
    <article class="flex min-h-[116px] flex-col justify-between rounded-xl border border-gray-200 bg-white p-4 transition-all hover:border-gray-300 hover:shadow-sm">
      <div>
        <ProviderOfficialHeader
          title={displayTitle}
          subtitle={info.source === "modelscope" && displayTitle !== info.path ? info.path : ""}
          gated={info.gated}
          privateArtifact={info.private}
          onOpen={() => onOpenDetail(model.path)}
          action={
            <ArtifactDownloadAction
              pulling={pulling}
              isLocal={isLocal}
              tone={info.source === "huggingface" ? "yellow" : "violet"}
              compact
              onDownload={() => onDownload(model.path)}
            />
          }
        />
        <div class="mt-2 flex min-h-5 items-center gap-2">
          <ProviderOfficialMetadata values={metadata} />
        </div>
      </div>
      <ProviderOfficialFooter
        owner={info.author}
        downloads={model.downloads}
        likes={model.likes}
        size={model.repo_size}
        updatedAt={model.updated_at}
      />
    </article>
  );
}

function DatasetGridCard({
  dataset,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  dataset: MarketplaceDataset;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (dataset: MarketplaceDataset) => void;
  onOpenDetail: () => void;
}) {
  void locale.value;
  if (isProviderMarketplaceSource(dataset.artifact_source)) {
    return (
      <ProviderDatasetGridCard
        dataset={dataset}
        pulling={pulling}
        isLocal={isLocal}
        onDownload={onDownload}
        onOpenDetail={onOpenDetail}
      />
    );
  }
  const metadata = datasetMarketplaceMetadata(dataset);
  const primaryTags = [
    ...metadata.tasks.slice(0, 1),
    ...(dataset.license ? [dataset.license] : []),
  ].slice(0, 2);
  const technicalTags = [
    ...metadata.formats.map((value) => value.toUpperCase()),
    ...metadata.modalities,
    ...metadata.topics,
  ].slice(0, 3);

  return (
    <div class="flex h-full min-h-[270px] flex-col rounded-xl border border-gray-200 bg-white p-5 transition-shadow hover:shadow-sm">
      <div class="flex-1">
        <div class="mb-2 flex min-h-[58px] items-start gap-2">
          <ArtifactOwnerAvatar source={dataset.artifact_source || "opencsg"} path={dataset.path} />
          <div class="min-w-0 flex-1">
            <button onClick={onOpenDetail} class="block w-full truncate text-left text-sm font-semibold text-gray-900 hover:text-indigo-600">{dataset.path}</button>
            <p class="mt-0.5 h-4 truncate text-xs text-gray-500">
              {dataset.nickname && dataset.nickname !== dataset.name ? dataset.nickname : "\u00a0"}
            </p>
            <div class="mt-1 flex min-h-5 flex-wrap items-center gap-1.5">
              {metadata.type && <DatasetBadge value={metadata.type} tone="blue" />}
              {metadata.sizeCategory && <DatasetBadge value={metadata.sizeCategory} />}
              {dataset.private && <DatasetBadge value={t("mp.privateAccess")} tone="amber" />}
            </div>
          </div>
        </div>
        <p class="mb-3 min-h-10 line-clamp-2 text-sm text-gray-500">
          {dataset.description || "\u00a0"}
        </p>
        <div class="flex min-h-6 items-center gap-1.5 overflow-hidden">
          {primaryTags.map((value) => <DatasetBadge key={value} value={value} tone="purple" />)}
          {technicalTags.map((value) => <DatasetBadge key={value} value={value} />)}
        </div>
      </div>
      <div class="mt-4 flex min-h-5 flex-wrap items-center gap-3 text-xs text-gray-400">
          <span class="flex items-center gap-1">
            <DownloadIcon /> {formatDownloadCount(dataset.downloads)}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {dataset.likes}
          </span>
          {Boolean(dataset.repo_size) && <span>{formatRepoSize(dataset.repo_size || 0)}</span>}
          {metadata.languages.length > 0 && (
            <span title={metadata.languages.join(", ")}>{metadata.languages.slice(0, 2).join(" · ")}</span>
          )}
      </div>
      <div class="mt-3 flex items-center justify-between border-t border-gray-100 pt-3 text-xs text-gray-400">
        <span class="flex items-center gap-1">
          <ClockIcon />
          {t("mp.updatedAt", new Date(dataset.updated_at).toLocaleDateString())}
        </span>
        <ArtifactDownloadAction
          pulling={pulling}
          isLocal={isLocal}
          tone="purple"
          compact
          onDownload={() => onDownload(dataset)}
        />
      </div>
    </div>
  );
}

function DatasetCard({
  dataset,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  dataset: MarketplaceDataset;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (dataset: MarketplaceDataset) => void;
  onOpenDetail: () => void;
}) {
  void locale.value;
  if (isProviderMarketplaceSource(dataset.artifact_source)) {
    return (
      <ProviderDatasetGridCard
        dataset={dataset}
        pulling={pulling}
        isLocal={isLocal}
        onDownload={onDownload}
        onOpenDetail={onOpenDetail}
      />
    );
  }
  const metadata = datasetMarketplaceMetadata(dataset);
  const badges = [
    ...(metadata.type ? [metadata.type] : []),
    ...metadata.tasks.slice(0, 1),
    ...metadata.formats.map((value) => value.toUpperCase()).slice(0, 1),
    ...metadata.topics.slice(0, 1),
    ...(dataset.license ? [dataset.license] : []),
  ].slice(0, 4);

  return (
    <div class="flex items-center justify-between py-4">
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2">
          <ArtifactOwnerAvatar source={dataset.artifact_source || "opencsg"} path={dataset.path} />
          <button onClick={onOpenDetail} class="font-medium text-gray-900 hover:text-indigo-600">{dataset.path}</button>
          {badges.map((value) => <DatasetBadge key={value} value={value} />)}
          {isLocal && (
            <span class="px-1.5 py-0.5 text-xs bg-purple-50 text-purple-600 rounded font-medium">{t("mp.downloaded")}</span>
          )}
        </div>
        {dataset.description && (
          <p class="text-sm text-gray-500 mt-1 line-clamp-1">{dataset.description}</p>
        )}
        <div class="flex items-center gap-3 mt-2 text-xs text-gray-400">
          {metadata.sizeCategory && <span>{metadata.sizeCategory}</span>}
          {metadata.languages.length > 0 && <span>{metadata.languages.slice(0, 2).join(" · ")}</span>}
          {Boolean(dataset.repo_size) && <span>{formatRepoSize(dataset.repo_size || 0)}</span>}
          <span>&middot;</span>
          <span>{new Date(dataset.updated_at).toLocaleDateString()}</span>
          <span>&middot;</span>
          <span class="flex items-center gap-1">
            <DownloadIcon /> {formatDownloadCount(dataset.downloads)}
          </span>
          <span class="flex items-center gap-1">
            <StarIcon /> {dataset.likes}
          </span>
        </div>
      </div>
      <div class="ml-4 flex-shrink-0 w-28 flex items-center justify-end">
        <ArtifactDownloadAction
          pulling={pulling}
          isLocal={isLocal}
          tone="purple"
          compact={false}
          onDownload={() => onDownload(dataset)}
        />
      </div>
    </div>
  );
}

function ProviderDatasetGridCard({
  dataset,
  pulling,
  isLocal,
  onDownload,
  onOpenDetail,
}: {
  dataset: MarketplaceDataset;
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  onDownload: (dataset: MarketplaceDataset) => void;
  onOpenDetail: () => void;
}) {
  void locale.value;
  const info = providerDatasetCardInfo(dataset);
  const displayTitle = info.source === "huggingface" ? info.path : info.title;
  const metadata = [
    info.type,
    ...info.tasks,
    ...info.formats.map((value) => value.toUpperCase()),
    info.sizeCategory,
    info.license,
  ].filter(Boolean);
  return (
    <article class="flex min-h-[116px] flex-col justify-between rounded-xl border border-gray-200 bg-white p-4 transition-all hover:border-gray-300 hover:shadow-sm">
      <div>
        <ProviderOfficialHeader
          title={displayTitle}
          subtitle={info.source === "modelscope" && displayTitle !== info.path ? info.path : ""}
          gated={info.gated}
          privateArtifact={info.private}
          onOpen={onOpenDetail}
          action={
            <ArtifactDownloadAction
              pulling={pulling}
              isLocal={isLocal}
              tone={info.source === "huggingface" ? "yellow" : "violet"}
              compact
              onDownload={() => onDownload(dataset)}
            />
          }
        />
        <div class="mt-2 flex min-h-5 items-center gap-2">
          <ProviderOfficialMetadata values={metadata} />
        </div>
      </div>
      <ProviderOfficialFooter
        owner={info.author}
        downloads={dataset.downloads}
        likes={dataset.likes}
        size={dataset.repo_size}
        files={dataset.file_count}
        updatedAt={dataset.updated_at}
      />
    </article>
  );
}

function ProviderOfficialHeader({
  title,
  subtitle,
  gated,
  privateArtifact,
  onOpen,
  action,
}: {
  title: string;
  subtitle?: string;
  gated: boolean;
  privateArtifact: boolean;
  onOpen: () => void;
  action: ComponentChildren;
}) {
  return (
    <div class="flex min-w-0 items-start justify-between gap-3">
      <div class="flex min-w-0 items-center gap-2">
        <button
          onClick={onOpen}
          class="truncate text-left text-sm font-semibold text-gray-800 transition-colors hover:text-violet-600"
          title={title}
        >
          {title}
        </button>
        {subtitle && <span class="hidden max-w-36 truncate text-xs text-gray-400 xl:inline" title={subtitle}>{subtitle}</span>}
        {gated && <ProviderOfficialBadge label={t("mp.gated")} />}
        {privateArtifact && <ProviderOfficialBadge label={t("mp.privateAccess")} />}
      </div>
      <div class="flex-shrink-0">{action}</div>
    </div>
  );
}

function ProviderOfficialBadge({ label }: { label: string }) {
  return <span class="flex-shrink-0 rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-500">{label}</span>;
}

function ProviderOfficialMetadata({ values }: { values: string[] }) {
  const [expanded, setExpanded] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const uniqueValues = [...new Set(values.map((value) => value.trim()).filter(Boolean))];
  const visibleValues = uniqueValues.slice(0, 3);
  const hiddenValues = uniqueValues.slice(3);

  useEffect(() => {
    if (!expanded) return;
    const closeOnOutsideClick = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setExpanded(false);
    };
    document.addEventListener("mousedown", closeOnOutsideClick);
    return () => document.removeEventListener("mousedown", closeOnOutsideClick);
  }, [expanded]);

  return (
    <div ref={menuRef} class="relative flex min-w-0 items-center gap-2">
      {visibleValues.map((value, index) => (
        <span key={`${value}:${index}`} class="flex max-w-32 flex-shrink-0 items-center gap-1 truncate text-[11px] text-gray-500" title={value}>
          <span class={`h-2 w-2 flex-shrink-0 rounded-sm ${index % 2 === 0 ? "bg-violet-400" : "bg-orange-300"}`} />
          <span class="truncate">{value}</span>
        </span>
      ))}
      {hiddenValues.length > 0 && (
        <button
          type="button"
          onClick={() => setExpanded((value) => !value)}
          class="flex h-5 w-6 flex-shrink-0 items-center justify-center rounded bg-gray-100 text-xs font-semibold text-gray-500 hover:bg-gray-200"
          title={t("mp.showMoreMetadata")}
          aria-label={t("mp.showMoreMetadata")}
          aria-expanded={expanded}
        >
          &hellip;
        </button>
      )}
      {expanded && hiddenValues.length > 0 && (
        <div class="absolute left-0 top-7 z-30 flex w-max max-w-80 flex-wrap gap-x-3 gap-y-2 rounded-lg border border-gray-200 bg-white p-3 shadow-lg">
          {hiddenValues.map((value, index) => (
            <span key={`${value}:more:${index}`} class="flex max-w-44 items-center gap-1.5 text-xs text-gray-600" title={value}>
              <span class={`h-2 w-2 flex-shrink-0 rounded-sm ${index % 2 === 0 ? "bg-violet-400" : "bg-orange-300"}`} />
              <span class="truncate">{value}</span>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function ProviderOfficialFooter({
  owner,
  downloads,
  likes,
  size,
  files,
  updatedAt,
}: {
  owner: string;
  downloads: number;
  likes: number;
  size?: number;
  files?: number;
  updatedAt: string;
}) {
  return (
    <div class="mt-3 flex min-w-0 items-center gap-3 overflow-hidden text-[11px] text-gray-400">
      {owner && (
        <span class="flex min-w-0 items-center gap-1.5">
          <span class="h-3.5 w-3.5 flex-shrink-0 rounded-full bg-violet-100" />
          <span class="max-w-28 truncate">{owner}</span>
        </span>
      )}
      <span class="flex flex-shrink-0 items-center gap-1"><ClockIcon />{new Date(updatedAt).toLocaleDateString()}</span>
      {typeof size === "number" && size > 0 && <span class="flex-shrink-0">{formatRepoSize(size)}</span>}
      {typeof files === "number" && files > 0 && <span class="flex-shrink-0">{t("mp.filesCount", files)}</span>}
      <span class="flex flex-shrink-0 items-center gap-1"><DownloadIcon />{formatDownloadCount(downloads)}</span>
      <span class="flex flex-shrink-0 items-center gap-1"><StarIcon />{likes}</span>
    </div>
  );
}

function ArtifactDownloadAction({
  pulling,
  isLocal,
  tone,
  compact,
  onDownload,
}: {
  pulling?: { status: string; percent: number; error?: string };
  isLocal?: boolean;
  tone: ArtifactActionTone;
  compact: boolean;
  onDownload: () => void;
}) {
  const color = {
    yellow: "text-[#D4A400]",
    violet: "text-violet-600",
    purple: "text-purple-600",
    indigo: "text-indigo-600",
  }[tone];
  const hoverColor = {
    yellow: "hover:text-[#B88A00]",
    violet: "hover:text-violet-700",
    purple: "hover:text-purple-700",
    indigo: "hover:text-indigo-700",
  }[tone];
  const bar = {
    yellow: "bg-[#FFD21E]",
    violet: "bg-violet-500",
    purple: "bg-purple-500",
    indigo: "bg-indigo-500",
  }[tone];
  if ((isLocal || pulling?.status === "success") && !pulling?.status?.startsWith("downloading")) {
    return (
      <span class={`inline-flex items-center gap-1 font-medium ${color} ${compact ? "text-xs" : "px-4 py-1.5 text-sm"}`}>
        <CheckIcon />
        {t("mp.downloaded")}
      </span>
    );
  }
  if (pulling) {
    if (pulling.status === "success") {
      return <span class={`font-medium ${color}`}>{t("mp.done")}</span>;
    }
    if (pulling.status.startsWith("error")) {
      return <span class="font-medium text-red-500" title={pulling.status}>{t("mp.failed")}</span>;
    }
    if (compact) {
      return (
        <span class={`font-medium ${color}`}>
          {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
        </span>
      );
    }
    return (
      <div>
        <div class={`mb-1 text-right text-xs font-medium ${color}`}>
          {pulling.percent > 0 ? `${pulling.percent}%` : t("mp.pulling")}
        </div>
        <div class="h-1.5 w-full overflow-hidden rounded-full bg-gray-200">
          <div class={`h-full rounded-full transition-all duration-300 ${bar}`} style={{ width: `${Math.max(pulling.percent, 3)}%` }} />
        </div>
      </div>
    );
  }
  if (compact) {
    return (
      <button onClick={onDownload} class={`flex items-center gap-1 font-medium transition-colors ${color} ${hoverColor}`}>
        <DownloadIcon /> {t("mp.download")}
      </button>
    );
  }
  return (
    <button
      onClick={onDownload}
      class="flex w-full items-center justify-center gap-1.5 rounded-lg border border-gray-200 px-4 py-1.5 text-sm text-gray-700 transition-colors hover:bg-gray-50"
    >
      <DownloadIcon /> {t("mp.download")}
    </button>
  );
}

type ArtifactActionTone = "yellow" | "violet" | "indigo" | "purple";

function ClockIcon() {
  return (
    <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
    </svg>
  );
}

function DatasetBadge({
  value,
  tone = "gray",
}: {
  value: string;
  tone?: "gray" | "blue" | "purple" | "amber";
}) {
  const tones = {
    gray: "bg-gray-100 text-gray-600",
    blue: "bg-blue-50 text-blue-700",
    purple: "bg-purple-50 text-purple-700",
    amber: "bg-amber-50 text-amber-700",
  };
  return (
    <span class={`inline-flex max-w-32 truncate rounded px-2 py-0.5 text-[11px] font-medium ${tones[tone]}`} title={value}>
      {value}
    </span>
  );
}

function DatasetFilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: string;
  options: FilterOption<string>[];
  onChange: (value: string) => void;
}) {
  return (
    <label class="sr-only">
      {label}
      <select
        aria-label={label}
        value={value}
        onChange={(event) => onChange(event.currentTarget.value)}
        class="not-sr-only rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-600 focus:outline-none focus:ring-2 focus:ring-indigo-500"
      >
        {options.map((option) => (
          <option key={option.value || "all"} value={option.value}>{option.label}</option>
        ))}
      </select>
    </label>
  );
}

function DownloadIcon() {
  return (
    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
    </svg>
  );
}

function StarIcon() {
  return (
    <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
      <path stroke-linecap="round" stroke-linejoin="round" d="M11.049 2.927c.3-.921 1.603-.921 1.902 0l1.519 4.674a1 1 0 00.95.69h4.915c.969 0 1.371 1.24.588 1.81l-3.976 2.888a1 1 0 00-.363 1.118l1.518 4.674c.3.922-.755 1.688-1.538 1.118l-3.976-2.888a1 1 0 00-1.176 0l-3.976 2.888c-.783.57-1.838-.197-1.538-1.118l1.518-4.674a1 1 0 00-.363-1.118l-3.976-2.888c-.784-.57-.38-1.81.588-1.81h4.914a1 1 0 00.951-.69l1.519-4.674z" />
    </svg>
  );
}

function ModelFormatBadges({ model }: { model: MarketplaceModel }) {
  const formatTags = getMarketplaceModelFormats(model);
  if (formatTags.length === 0) {
    return null;
  }

  return (
    <>
      {formatTags.map((formatTag) => {
        const formatName = formatTag.name.trim().toLowerCase();
        const label = formatModelFormatLabel(formatTag);
        const tone = formatName === "gguf"
          ? "bg-blue-50 text-blue-700"
          : formatName === "safetensors"
            ? "bg-emerald-50 text-emerald-700"
            : "bg-gray-100 text-gray-700";

        return (
          <span key={`format:${formatTag.name}`} class={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ${tone}`}>
            {label}
          </span>
        );
      })}
    </>
  );
}

function formatModelFormatLabel(formatTag: MarketplaceTag): string {
  const formatName = formatTag.name.trim().toLowerCase();
  return formatTag.show_name?.trim()
    || (formatName === "safetensors" ? "SafeTensors" : formatName === "gguf" ? "GGUF" : formatTag.name);
}

function formatRepoSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return "";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** unitIndex;
  return `${value.toFixed(value >= 100 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function formatDownloadCount(value: number): string {
  if (!Number.isFinite(value) || value < 0) {
    return "0";
  }
  if (value < 1000) {
    return String(Math.floor(value));
  }
  return `${(value / 1000).toFixed(1).replace(/\.0$/, "")}k`;
}

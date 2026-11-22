import type { ComponentChildren } from "preact";
import { useEffect, useRef, useState } from "preact/hooks";
import { createClusterDeployment, createProviderPool, getMarketplaceModelDetail } from "../api/client";
import type { ArtifactSource, MarketplaceModel, MarketplaceModelDetailResponse, ClusterNodeView, ClusterDeployment, ClusterDeploymentSpec, ClusterView, ProviderPool } from "../api/client";
import { t } from "../i18n";
import { canPublishModelInstance, modelInstanceRows, modelInstanceCategory, modelPublication } from "../clusterModels";
import type { ModelInstanceRow } from "../clusterModels";
import { ContextWindowField } from "./ContextWindowField";
import { DeploymentModelPicker } from "./DeploymentModelPicker";
import { deploymentModelKind, deploymentName, deploymentRevision, deploymentTargets, isLlamaDeployment } from "../deploymentModels";
import type { DeploymentModelKind } from "../deploymentModels";
import { formatMemory as memorySize } from "../clusterMemory";
import { automaticDeploymentTarget, deploymentNodeMemory, deploymentQuantOptions, recommendDeploymentQuant } from "../deploymentRecommendation";

const inputClass = "w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm focus:ring-2 focus:ring-indigo-500";
const buttonClass = "rounded-lg border border-gray-200 px-3 py-2 text-sm hover:bg-gray-50 disabled:opacity-40";
const primaryClass = "rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white hover:bg-indigo-700 disabled:opacity-40";

function nodeMemoryLabel(node: ClusterNodeView, cpuOnly: boolean): string {
  const memory = deploymentNodeMemory(node, cpuOnly);
  const capacity = memory.total > 0 ? memorySize(memory.total) : t("deployments.memoryUnknown");
  return `${t(`deployments.memory.${memory.kind}`)} ${capacity} · ${memory.free === null ? t("deployments.memoryFreeUnknown") : t("deployments.memoryFree", memorySize(memory.free))}`;
}

interface DeploymentControls {
  create: () => void;
  publish: (row: ModelInstanceRow) => void;
  busy: boolean;
  hasTargets: boolean;
}

// The unified model page owns inventory and deployment polling. These controls
// only own the create/publish dialogs, so a deployment is rendered once.
export function ClusterDeploymentControls({ view, deployments, publications, stale, onChanged, children }: {
  view: ClusterView;
  deployments: ClusterDeployment[];
  publications: ProviderPool[] | null;
  stale: boolean;
  onChanged: () => Promise<unknown>;
  children: (controls: DeploymentControls) => ComponentChildren;
}) {
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);
  const [modelKind, setModelKind] = useState<DeploymentModelKind>("unknown");
  const [detectedKind, setDetectedKind] = useState<DeploymentModelKind>("unknown");
  const [detecting, setDetecting] = useState(false);
  const [detectionFailed, setDetectionFailed] = useState(false);
  const [detail, setDetail] = useState<MarketplaceModelDetailResponse | null>(null);
  const selectedModel = useRef<MarketplaceModel | null>(null);
  const [selection, setSelection] = useState(0);
  const [draft, setDraft] = useState<ClusterDeploymentSpec | null>(null);
  const [publishing, setPublishing] = useState<ModelInstanceRow | null>(null);
  const [serviceID, setServiceID] = useState("");
  const [formError, setFormError] = useState("");
  const editor = useRef<HTMLDialogElement>(null);
  const publisher = useRef<HTMLDialogElement>(null);
  useEffect(() => { if (draft) editor.current?.showModal(); else editor.current?.close(); }, [!!draft]);
  useEffect(() => { if (publishing) publisher.current?.showModal(); else publisher.current?.close(); }, [!!publishing]);
  useEffect(() => {
    let current = true;
    const repository = draft?.repository.trim() || "";
    const source = draft?.artifact_source as ArtifactSource;
    if (!/^[^/\s]+\/[^/\s]+$/.test(repository)) { setDetecting(false); return; }
    setDetecting(true); setDetectionFailed(false);
    const timer = setTimeout(() => {
      void getMarketplaceModelDetail(repository, { artifactSource: source }).then(result => {
        if (!current) return;
        setDetail(result);
        const kind = deploymentModelKind(result.details, result.local_inference);
        // List metadata is still useful if the detail response omits task tags.
        const detected = kind === "unknown" && selectedModel.current ? deploymentModelKind(selectedModel.current) : kind;
        setDetectedKind(detected); setModelKind(detected);
        const revisionModel = result.details.default_branch ? result.details : selectedModel.current || result.details;
        setDraft(value => value && ({ ...value, revision: deploymentRevision(revisionModel, source) }));
      }).catch(() => { if (current) setDetectionFailed(true); })
        .finally(() => { if (current) setDetecting(false); });
    }, 300);
    return () => { current = false; clearTimeout(timer); };
  }, [draft?.repository, draft?.artifact_source, selection]);
  const targets = deploymentTargets(view);
  const cpuOnly = draft?.n_gpu_layers === 0;
  const keepAlive = draft?.keep_alive?.trim();
  const needsKeepAlive = !!keepAlive && keepAlive !== "-1";
  const autoTarget = automaticDeploymentTarget(view, cpuOnly, needsKeepAlive);
  const target = draft?.node_uuid === "auto" ? autoTarget : targets.find(node => node.uuid === draft?.node_uuid);
  const keepAliveUnsupported = needsKeepAlive && (draft?.node_uuid === "auto"
    ? !targets.some(node => node.status?.deployment_keep_alive) : !!target && !target.status?.deployment_keep_alive);
  const quantOptions = deploymentQuantOptions(detail?.details || selectedModel.current, detail);
  const recommendation = recommendDeploymentQuant(quantOptions, deploymentNodeMemory(target, cpuOnly), draft?.num_ctx || 4096, draft?.num_parallel || 1);
  const quantNoFit = !draft?.quant && recommendation.state === "no-fit";
  const instances = modelInstanceRows(view.members, [], deployments);
  const currentPublishing = instances.find(row => row.key === publishing?.key);
  const canPublish = !stale && !!currentPublishing && canPublishModelInstance(currentPublishing, view)
    && modelPublication(currentPublishing, view, publications).state === "unpublished";
  const create = () => {
    setFormError(""); setModelKind("unknown"); setDetectedKind("unknown"); setDetecting(false); setDetectionFailed(false); setDetail(null); selectedModel.current = null;
    setDraft({ name: "", node_uuid: "auto", repository: "", artifact_source: "opencsg", revision: "main", quant: "", kind: "chat", num_ctx: 4096, num_parallel: 1, n_gpu_layers: -1 });
  };
  const patch = (value: Partial<ClusterDeploymentSpec>) => setDraft(current => current && ({ ...current, ...value }));
  const selectModel = (model: MarketplaceModel) => {
    setDetail(null);
    selectedModel.current = model;
    setSelection(value => value + 1);
    const kind = deploymentModelKind(model);
    setModelKind(kind); setDetectedKind(kind);
    setDetecting(true);
    setDraft(current => current && ({ ...current, repository: model.path, name: deploymentName(model.path, deployments.map(d => d.name)),
      revision: deploymentRevision(model, current.artifact_source as ArtifactSource), quant: "", ...(isLlamaDeployment(kind) ? { kind } : {}) }));
  };
  const save = async (event: Event) => {
    event.preventDefault(); if (stale || busy || !draft || detecting || keepAliveUnsupported || !isLlamaDeployment(modelKind)) return;
    if (!target) { setFormError(t("deployments.chooseNode")); return; }
    if (quantNoFit) { setFormError(t("deployments.quantNoFit")); return; }
    setBusy(true); setFormError("");
    try { await createClusterDeployment({ ...draft, keep_alive: keepAlive || undefined, quant: draft.quant || recommendation.quant || "",
      minimum_memory_bytes: !draft.quant && recommendation.required ? Math.ceil(recommendation.required) : undefined,
      repository: draft.repository.trim(), kind: modelKind }); setDraft(null); await onChanged(); }
    catch (e) { setFormError((e as Error).message); }
    finally { setBusy(false); }
  };
  const publish = async (event: Event) => {
    event.preventDefault(); if (busy || !currentPublishing || !canPublish) return;
    setBusy(true); setFormError("");
    try {
      await createProviderPool({ name: serviceID.trim(), model: serviceID.trim(), enabled: true, policy: "fixed", category: modelInstanceCategory(currentPublishing),
        members: [{ id: crypto.randomUUID(), source: `node:${currentPublishing.node.uuid}`, model: currentPublishing.model.id, priority: 0, weight: 100 }] });
      setPublishing(null); setNotice(t("deployments.published")); await onChanged();
    } catch (e) { setFormError((e as Error).message); }
    finally { setBusy(false); }
  };
  return <>
    {notice && <p role="status" class="mb-4 rounded-lg bg-green-50 p-3 text-sm text-green-800">{notice} <a class="underline" href="/ai-gateway">{t("deployments.gateway")}</a></p>}
    {children({ create, publish: row => { setPublishing(row); setServiceID(row.model.repo || row.model.id); setFormError(""); }, busy, hasTargets: targets.length > 0 })}
    <dialog ref={editor} class="m-auto max-h-[90dvh] w-[min(620px,94vw)] max-w-none overflow-y-auto rounded-2xl p-0 shadow-xl backdrop:bg-black/30" onCancel={e => { if (busy) e.preventDefault(); else setDraft(null); }}>
      {draft && <form onSubmit={save} class="space-y-5 p-6">
        <h2 class="text-lg font-semibold">{t("deployments.create")}</h2>
        <p class="text-sm text-gray-500">{t("deployments.createHint")}</p>
        <fieldset disabled={busy} class="grid grid-cols-2 gap-4">
          <label class="col-span-2 space-y-1 text-sm">{t("deployments.source")}<select id="deployment-source" class={inputClass} value={draft.artifact_source} onChange={e => {
            const source = e.currentTarget.value as ArtifactSource;
            patch({ artifact_source: source, repository: "", name: "", revision: deploymentRevision(undefined, source), quant: "" });
            selectedModel.current = null; setDetail(null); setModelKind("unknown"); setDetectedKind("unknown"); setDetecting(false); setDetectionFailed(false);
          }}><option value="opencsg">OpenCSG</option><option value="huggingface">Hugging Face</option><option value="modelscope">ModelScope</option></select></label>
          <DeploymentModelPicker value={draft.repository} source={draft.artifact_source as ArtifactSource} disabled={busy}
            onInput={repository => {
              if (repository === draft.repository) return;
              patch({ repository, name: deploymentName(repository.trim(), deployments.map(d => d.name)), quant: "", revision: deploymentRevision(undefined, draft.artifact_source as ArtifactSource) });
              selectedModel.current = null; setDetail(null); setModelKind("unknown"); setDetectedKind("unknown"); setDetecting(/^[^/\s]+\/[^/\s]+$/.test(repository.trim())); setDetectionFailed(false);
            }} onSelect={selectModel} />
          {detecting ? <p role="status" class="col-span-2 text-sm text-gray-500">{t("deployments.detecting")}</p>
            : /^[^/\s]+\/[^/\s]+$/.test(draft.repository.trim()) && <>
              {detectedKind === "unknown" && <label class="col-span-2 space-y-1 text-sm" for="deployment-kind">{t("deployments.kind")}
                <select id="deployment-kind" class={inputClass} value={modelKind} onChange={e => setModelKind(e.currentTarget.value as DeploymentModelKind)}>
                  {["unknown", "chat", "embedding", "image", "asr", "tts"].map(kind => <option key={kind} value={kind}>{t(`deployments.${kind}`)}</option>)}
                </select>
                <p class="text-xs text-gray-500">{t(detectionFailed ? "deployments.detectionFailed" : "deployments.unknownHint")}</p>
              </label>}
              {modelKind !== "unknown" && <p data-deployment-runtime class="col-span-2 rounded-lg bg-gray-50 px-3 py-2 text-sm text-gray-600">
                {t(`deployments.${modelKind}`)} · {t(`deployments.runtime.${modelKind}`)} · {t("deployments.latest")}
              </p>}
              {modelKind !== "unknown" && !isLlamaDeployment(modelKind) && <p role="status" class="col-span-2 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">{t("deployments.unsupportedKind")}</p>}
            </>}
          <label class="col-span-2 space-y-1 text-sm" for="deployment-node">{t("deployments.node")}
            <select id="deployment-node" class={inputClass} required value={draft.node_uuid} onChange={e => patch({ node_uuid: e.currentTarget.value })}>
              <option value="auto">{t("deployments.autoNode")}</option>
              {targets.map(n => <option value={n.uuid} key={n.uuid}>{n.name}{n.local ? ` · ${t("deployments.thisNode")}` : ""} · {nodeMemoryLabel(n, cpuOnly)}</option>)}
            </select>
            <p class="text-xs text-gray-500">{draft.node_uuid === "auto"
              ? autoTarget ? t("deployments.autoNodeSelected", autoTarget.name, nodeMemoryLabel(autoTarget, cpuOnly)) : t("deployments.autoNodeUnavailable")
              : target && nodeMemoryLabel(target, cpuOnly)}</p>
          </label>
          {draft.repository.trim() && isLlamaDeployment(modelKind) && <div class="col-span-2 space-y-2">
            <label class="block space-y-1 text-sm" for="deployment-quant">{t("deployments.quant")}
              <select id="deployment-quant" class={inputClass} disabled={detecting} value={draft.quant} onChange={e => patch({ quant: e.currentTarget.value })}>
                <option value="">{recommendation.quant ? t("deployments.quantAutoRecommended", recommendation.quant.toUpperCase()) : t("deployments.quantAuto")}</option>
                {quantOptions.map(option => <option key={option.quant} value={option.quant}>{option.quant.toUpperCase()}{option.size ? ` · ${option.estimated ? "≈ " : ""}${memorySize(option.size)}` : ""}</option>)}
              </select>
            </label>
            {!detecting && <p role="status" class={`text-xs ${quantNoFit ? "text-amber-700" : "text-gray-500"}`}>
              {recommendation.state === "recommended" ? t("deployments.quantRecommendation", recommendation.quant!.toUpperCase(), memorySize(recommendation.required!), memorySize(recommendation.available!))
                : t(recommendation.state === "no-fit" ? "deployments.quantNoFit" : "deployments.quantUnknown")}
            </p>}
            <p class="text-xs text-gray-400">{t("deployments.quantEstimateHint")}</p>
          </div>}
          {draft.repository.trim() && <details class="col-span-2 rounded-xl border border-gray-200 p-3">
            <summary class="cursor-pointer text-sm text-gray-600">{t("deployments.advanced")}</summary>
            <div class="mt-4 grid grid-cols-2 gap-4">
              <label class="col-span-2 space-y-1 text-sm">{t("deployments.name")}<input id="deployment-name" class={inputClass} required maxLength={100} value={draft.name} onInput={e => patch({ name: e.currentTarget.value })} /></label>
              {isLlamaDeployment(modelKind) && <>
                <ContextWindowField kind={modelKind} value={draft.num_ctx} required inputClass={inputClass} onInput={value => patch({ num_ctx: Number(value) })} />
                <label class="space-y-1 text-sm">{t("deployments.parallel")}<input type="number" class={inputClass} min={1} max={16} required value={draft.num_parallel} onInput={e => patch({ num_parallel: Number(e.currentTarget.value) })} /></label>
                <label class="space-y-1 text-sm">{t("deployments.gpuLayers")}<input type="number" class={inputClass} min={-1} max={999} required value={draft.n_gpu_layers} onInput={e => patch({ n_gpu_layers: Number(e.currentTarget.value) })} /></label>
                <p class="col-span-2 text-xs text-gray-400">{t("deployments.llamaParamsHint")}</p>
                <label class="col-span-2 space-y-1 text-sm">{t("deployments.keepAlive")}
                  <input class={inputClass} placeholder="-1" value={draft.keep_alive ?? ""} onInput={e => patch({ keep_alive: e.currentTarget.value })} />
                  <span class="block text-xs text-gray-500">{t("deployments.keepAliveHint")}</span>
                </label>
              </>}
              <p class="col-span-2 text-xs text-gray-400">{t("deployments.registryContext")}</p>
            </div>
          </details>}
        </fieldset>
        {stale && <p role="status" class="text-sm text-amber-700">{t("cluster.modelStatusStale")}</p>}
        {keepAliveUnsupported && <p role="status" class="text-sm text-amber-700">{t("deployments.keepAliveUpgradeNode")}</p>}
        {formError && <p role="alert" class="text-sm text-red-600">{formError}</p>}
        <div class="flex justify-end gap-2"><button type="button" class={buttonClass} disabled={busy} onClick={() => setDraft(null)}>{t("deployments.cancel")}</button><button class={primaryClass} disabled={stale || busy || detecting || quantNoFit || keepAliveUnsupported || !/^[^/\s]+\/[^/\s]+$/.test(draft.repository.trim()) || !target || !isLlamaDeployment(modelKind)}>{t(busy ? "deployments.saving" : "deployments.deploy")}</button></div>
      </form>}
    </dialog>
    <dialog ref={publisher} class="w-full max-w-md rounded-2xl p-6 shadow-xl backdrop:bg-black/30" onCancel={e => { if (busy) e.preventDefault(); else setPublishing(null); }}>
      <form onSubmit={publish} class="space-y-4"><h2 class="text-lg font-semibold">{t("deployments.publish")}</h2><p class="text-sm text-gray-500">{t("deployments.publishHint")}</p><p class="break-all text-xs text-gray-500">{publishing?.model.repo || publishing?.model.id} · {publishing?.node.name || t("deployments.unknownNode")}</p>
        <label class="block space-y-1 text-sm">{t("deployments.serviceID")}<input class={inputClass} required value={serviceID} onInput={e => setServiceID(e.currentTarget.value)} /></label>
        {!canPublish && <p role="status" class="text-sm text-amber-700">{t("cluster.publishUnavailable")}</p>}
        {formError && <p role="alert" class="text-sm text-red-600">{formError}</p>}
        <div class="flex justify-end gap-2"><button type="button" class={buttonClass} disabled={busy} onClick={() => setPublishing(null)}>{t("deployments.cancel")}</button><button class={primaryClass} disabled={busy || !canPublish}>{t("deployments.publish")}</button></div>
      </form>
    </dialog>
  </>;
}

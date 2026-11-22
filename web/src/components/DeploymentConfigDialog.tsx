import { useEffect, useId, useRef, useState } from "preact/hooks";
import { configureClusterDeployment, getMarketplaceModelDetail } from "../api/client";
import type { ArtifactSource, ClusterDeployment, ClusterDeploymentConfig } from "../api/client";
import { isLlamaDeployment } from "../deploymentModels";
import { deploymentQuantOptions } from "../deploymentRecommendation";
import { t } from "../i18n";
import { ContextWindowField } from "./ContextWindowField";
import { DeploymentMessage } from "./DeploymentMessage";

const inputClass = "w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm focus:ring-2 focus:ring-indigo-500";

export function DeploymentConfigDialog({ deployment, nodeName, allowed, supportsKeepAlive, onClose, onSaved }: {
  deployment: ClusterDeployment; nodeName: string; allowed: boolean; supportsKeepAlive: boolean; onClose: () => void; onSaved: () => Promise<unknown>;
}) {
  const runtimeSupported = isLlamaDeployment(deployment.kind);
  const canEdit = allowed && runtimeSupported;
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId(), quantsID = useId();
  const [draft, setDraft] = useState<ClusterDeploymentConfig>(() => ({ quant: deployment.quant || "", num_ctx: deployment.num_ctx,
    num_parallel: deployment.num_parallel, n_gpu_layers: deployment.n_gpu_layers, keep_alive: deployment.keep_alive }));
  const [quants, setQuants] = useState<string[]>([]);
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  useEffect(() => { dialog.current?.showModal(); }, []);
  useEffect(() => {
    if (!runtimeSupported) return;
    let current = true;
    // Suggestions never overwrite the stored quant or block editing offline metadata.
    void getMarketplaceModelDetail(deployment.repository, { artifactSource: deployment.artifact_source as ArtifactSource, revision: deployment.revision })
      .then(detail => { if (current) setQuants(deploymentQuantOptions(detail.details, detail).map(option => option.quant)); })
      .catch(() => { /* Parameters remain editable when registry metadata is unavailable. */ });
    return () => { current = false; };
  }, [deployment.id, runtimeSupported]);
  const patch = (value: Partial<ClusterDeploymentConfig>) => setDraft(current => ({ ...current, ...value }));
  const save = async (event: Event) => {
    event.preventDefault();
    if (!canEdit || busy) return;
    setBusy(true); setError("");
    try {
      await configureClusterDeployment(deployment.id, deployment.generation, { ...draft, quant: draft.quant.trim(), keep_alive: supportsKeepAlive ? (draft.keep_alive ?? "").trim() : undefined });
      await onSaved();
    } catch (e) { setError((e as Error).message); }
    finally { setBusy(false); }
  };
  return <dialog ref={dialog} aria-labelledby={titleID} class="m-auto max-h-[90dvh] w-[min(540px,94vw)] max-w-none overflow-y-auto rounded-2xl p-0 shadow-xl backdrop:bg-black/30"
    onCancel={event => { if (busy) event.preventDefault(); else onClose(); }}>
    <form onSubmit={save} class="space-y-5 p-6">
      <h2 id={titleID} class="text-lg font-semibold">{t("deployments.editConfig")}</h2>
      <p class="break-words text-sm text-gray-600">{deployment.name} · {deployment.repository}<br />{nodeName} · {deployment.artifact_source} · {deployment.revision}</p>
      <p class="text-sm text-gray-500">{t("deployments.editConfigHint")}</p>
      {runtimeSupported ? <fieldset disabled={busy || !canEdit} class="grid grid-cols-2 gap-4">
        <label class="col-span-2 space-y-1 text-sm">{t("deployments.quant")}
          <input class={inputClass} list={quantsID} maxLength={32} value={draft.quant} onInput={e => patch({ quant: e.currentTarget.value })} />
          <datalist id={quantsID}>{quants.map(quant => <option key={quant} value={quant} />)}</datalist>
          <p class="text-xs text-gray-500">{t("deployments.editQuantHint")}</p>
        </label>
        <ContextWindowField kind={deployment.kind} value={draft.num_ctx} required inputClass={inputClass} onInput={value => patch({ num_ctx: Number(value) })} />
        <label class="space-y-1 text-sm">{t("deployments.parallel")}<input class={inputClass} type="number" min={1} max={16} step={1} required value={draft.num_parallel} onInput={e => patch({ num_parallel: Number(e.currentTarget.value) })} /></label>
        <label class="space-y-1 text-sm">{t("deployments.gpuLayers")}<input class={inputClass} type="number" min={-1} max={999} step={1} required value={draft.n_gpu_layers} onInput={e => patch({ n_gpu_layers: Number(e.currentTarget.value) })} /></label>
        <p class="col-span-2 text-xs text-gray-500">{t("deployments.llamaParamsHint")}</p>
        {supportsKeepAlive && <label class="col-span-2 space-y-1 text-sm">{t("deployments.keepAlive")}
          <input class={inputClass} placeholder="-1" value={draft.keep_alive ?? ""} onInput={e => patch({ keep_alive: e.currentTarget.value })} />
          <span class="block text-xs text-gray-500">{t("deployments.keepAliveHint")}</span>
        </label>}
      </fieldset> : <p role="status" class="text-sm text-amber-700">{t("deployments.unsupportedKind")}</p>}
      {!allowed && <p role="status" class="text-sm text-amber-700">{t("deployments.configChanged")}</p>}
      {error && <DeploymentMessage message={error} alert class="text-sm text-red-600" />}
      <div class="flex justify-end gap-2">
        <button type="button" class="rounded-lg border border-gray-200 px-3 py-2 text-sm disabled:opacity-40" disabled={busy} onClick={onClose}>{t("deployments.cancel")}</button>
        <button class="rounded-lg bg-indigo-600 px-4 py-2 text-sm text-white disabled:opacity-40" disabled={busy || !canEdit}>{t(busy ? "deployments.saving" : "deployments.saveConfig")}</button>
      </div>
    </form>
  </dialog>;
}

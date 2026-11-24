import { createPortal } from "preact/compat";
import { useLayoutEffect, useRef, useState } from "preact/hooks";
import type { ClusterDeployment, ClusterView } from "../api/client";
import type { InferenceTestTarget } from "../api/serviceTestProtocols";
import { deleteClusterDeployment, manageClusterNodeModel, updateClusterDeployment } from "../api/client";
import type { ModelInstanceRow } from "../clusterModels";
import { canPublishModelInstance, modelActionAvailability, modelPublication, modelTestTargets } from "../clusterModels";
import { t } from "../i18n";
import { ConfirmDialog } from "./ConfirmDialog";
import { InferenceTestDialog } from "./InferenceTestDialog";
import { DeploymentMessage } from "./DeploymentMessage";
import { DeploymentConfigDialog } from "./DeploymentConfigDialog";

export function ClusterModelActions({ row, rows, view, publication, stale, busy: parentBusy, onPublish, onChanged, onSync, syncBusy }: {
  row: ModelInstanceRow; rows: ModelInstanceRow[]; view: ClusterView; publication: ReturnType<typeof modelPublication>;
  stale: boolean; busy: boolean; onPublish: () => void; onChanged: () => Promise<unknown>; onSync?: () => void; syncBusy: boolean;
}) {
  const [testing, setTesting] = useState<InferenceTestTarget[] | null>(null);
  const [editing, setEditing] = useState<ClusterDeployment | null>(null);
  const [confirm, setConfirm] = useState<"unload" | "delete" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const menuID = `cluster-model-actions-${encodeURIComponent(row.key)}`;
  const unavailable = busy || parentBusy || stale;
  const testTargets = modelTestTargets(row, view, publication, unavailable);
  const actions = modelActionAvailability(row, view, unavailable, rows);
  const publishable = !unavailable && publication.state === "unpublished" && canPublishModelInstance(row, view);
  const publishReason = stale ? "cluster.modelStatusStale" : publication.state === "unknown" ? "cluster.publicationRefreshFailed"
    : publication.services.length ? "cluster.modelAlreadyPublished" : "cluster.publishUnavailable";
  const d = row.deployment;
  const stopLabel = d && d.status.phase !== "ready" ? "cluster.modelStop" : "cluster.modelUnload";
  const deleteLabel = d ? "cluster.modelRemove" : "cluster.modelDelete";
  const deleteHint = d ? "cluster.modelRemoveHint" : "cluster.modelDeleteHint";
  useLayoutEffect(() => {
    if (!position) return;
    menu.current?.querySelector<HTMLElement>('[role="menuitem"]:not(:disabled)')?.focus({ preventScroll: true });
    const outside = (event: PointerEvent) => {
      if (!menu.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) setPosition(null);
    };
    const dismiss = () => setPosition(null);
    document.addEventListener("pointerdown", outside);
    window.addEventListener("resize", dismiss);
    window.addEventListener("scroll", dismiss, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("resize", dismiss);
      window.removeEventListener("scroll", dismiss, true);
    };
  }, [!!position]);
  const act = (action: () => void) => { setPosition(null); action(); };
  const change = async (action: "unload" | "delete" | "start") => {
    if (!actions[action] || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      if (d) {
        if (action === "delete") await deleteClusterDeployment(d.id);
        else await updateClusterDeployment(d.id, action === "unload" ? "stopped" : "running");
      }
      else if (action !== "start") await manageClusterNodeModel(row.node.uuid, row.model, action);
      setConfirm(null); setNotice(t("cluster.modelActionSubmitted")); await onChanged();
    } catch (e) { setConfirm(null); setError((e as Error).message); }
    finally { setBusy(false); }
  };
  const buttonClass = "rounded-md border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-40";
  const itemClass = "block w-full rounded-lg px-3 py-2 text-left text-sm text-gray-700 hover:bg-gray-50 focus:bg-indigo-50 focus:outline-none disabled:cursor-not-allowed disabled:opacity-40";
  return <div class="max-w-60">
    <div class="flex items-center gap-2 whitespace-nowrap">
      <span title={publishable ? undefined : t(publishReason)}><button type="button" class={`${buttonClass} border-indigo-200 text-indigo-600 hover:bg-indigo-50`} disabled={!publishable} onClick={onPublish}>{t("cluster.modelPublish")}</button></span>
      <button type="button" ref={trigger} class={buttonClass} aria-haspopup="menu" aria-expanded={!!position} aria-controls={position ? menuID : undefined} onClick={() => {
        if (position) { setPosition(null); return; }
        const rect = trigger.current!.getBoundingClientRect();
        const height = (onSync ? 220 : 180) + (d ? 40 : 0);
        setPosition({ top: rect.bottom + height < window.innerHeight ? rect.bottom + 6 : Math.max(8, rect.top - height), left: Math.max(8, Math.min(rect.right - 176, window.innerWidth - 184)) });
      }}>{t("services.more")} <span aria-hidden="true">⌄</span></button>
    </div>
    {position && createPortal(<div ref={menu} id={menuID} role="menu" aria-label={t("services.more")} style={position} class="fixed z-50 w-44 rounded-xl border border-gray-200 bg-white p-1.5 shadow-lg" onKeyDown={event => {
      if (event.key === "Escape") { event.preventDefault(); setPosition(null); trigger.current?.focus({ preventScroll: true }); }
      if (event.key === "Tab") setPosition(null);
      if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
        event.preventDefault();
        const items = Array.from(menu.current?.querySelectorAll<HTMLElement>('[role="menuitem"]:not(:disabled)') || []);
        const current = items.indexOf(document.activeElement as HTMLElement);
        const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
        items[next]?.focus();
      }
    }}>
      <button type="button" role="menuitem" class={itemClass} disabled={!actions.start} title={!actions.start ? t(!d ? "cluster.modelStartsOnDemand" : actions.reason) : undefined} onClick={() => act(() => void change("start"))}>{t("cluster.modelStart")}</button>
      <button type="button" role="menuitem" class={itemClass} disabled={!actions.unload} title={!actions.unload ? t(actions.reason) : undefined} onClick={() => act(() => setConfirm("unload"))}>{t(stopLabel)}</button>
      {d && <button type="button" role="menuitem" class={itemClass} disabled={!actions.edit} title={!actions.edit ? t(actions.editReason) : undefined} onClick={() => act(() => setEditing(d))}>{t("deployments.editConfig")}</button>}
      {onSync && <button type="button" role="menuitem" class={itemClass} disabled={!actions.sync || syncBusy || unavailable} title={!actions.sync ? t(actions.reason) : undefined} onClick={() => act(onSync)}>{t(syncBusy ? "cluster.syncing" : "cluster.syncAll")}</button>}
      <button type="button" role="menuitem" class={itemClass} disabled={!testTargets.length} title={!testTargets.length ? t(stale ? "cluster.modelStatusStale" : "cluster.modelTestUnavailable") : undefined} onClick={() => act(() => setTesting(testTargets))}>{t("services.test")}</button>
      <div class="my-1 border-t border-gray-100" />
      <button type="button" role="menuitem" class={`${itemClass} text-rose-600`} disabled={!actions.delete} title={!actions.delete ? t(actions.reason) : undefined} onClick={() => act(() => setConfirm("delete"))}>{t(deleteLabel)}</button>
    </div>, document.body)}
    {testing && <InferenceTestDialog service={testing[0]} services={testing} onClose={() => setTesting(null)} />}
    {editing && <DeploymentConfigDialog deployment={editing} supportsKeepAlive={!!row.node.status?.deployment_keep_alive} allowed={actions.edit && d?.generation === editing.generation}
      engines={row.node.status?.engines}
      onClose={() => setEditing(null)} onSaved={async () => { setEditing(null); setNotice(t("deployments.configSaved")); await onChanged(); }} />}
    {error && <DeploymentMessage message={error} alert class="mt-2 text-xs text-red-600" />}
    {notice && <p role="status" class="mt-2 text-xs text-gray-500">{notice}</p>}
    <ConfirmDialog open={confirm !== null} title={t(confirm === "delete" ? deleteLabel : stopLabel)}
      name={`${row.model.id} · ${row.node.name || t("deployments.unknownNode")}`} description={t(confirm === "delete" ? deleteHint : "cluster.modelUnloadHint")}
      confirmLabel={t(confirm === "delete" ? deleteLabel : stopLabel)} busy={busy} busyLabel={t("cluster.modelApplying")}
      confirmDisabled={!!confirm && !actions[confirm]}
      onCancel={() => setConfirm(null)} onConfirm={() => { if (confirm) void change(confirm); }} />
  </div>;
}

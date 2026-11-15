import { createPortal } from "preact/compat";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "preact/hooks";
import { locale, t } from "../i18n";

export function DeploymentMessage({ message, class: className = "", alert = false }: {
  message: string; class?: string; alert?: boolean;
}) {
  void locale.value;
  const [expanded, setExpanded] = useState(false);
  const [truncated, setTruncated] = useState(false);
  const summary = useRef<HTMLParagraphElement>(null);
  const dialog = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const close = () => { dialog.current?.close(); setExpanded(false); };
  useLayoutEffect(() => {
    const element = summary.current;
    if (!element) return;
    const measure = () => setTruncated(element.scrollHeight > element.clientHeight + 1);
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(element);
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); window.removeEventListener("resize", measure); };
  }, [message]);
  useEffect(() => {
    if (expanded) dialog.current?.showModal();
  }, [expanded]);
  return <div class={`min-w-0 ${className}`}>
    <p ref={summary} role={alert ? "alert" : undefined} class="line-clamp-3 whitespace-pre-wrap break-all">{message}</p>
    {truncated && <button type="button" class="mt-1 text-xs text-indigo-600 hover:underline" aria-haspopup="dialog" onClick={() => setExpanded(true)}>{t("deployments.messageMore")}</button>}
    {expanded && createPortal(<dialog ref={dialog} aria-labelledby={titleID}
      class="w-[calc(100%_-_2rem)] max-w-3xl rounded-2xl p-0 shadow-xl backdrop:bg-black/40"
      onCancel={event => { event.preventDefault(); close(); }} onClose={() => setExpanded(false)}
      onClick={event => { if (event.target === event.currentTarget) close(); }}>
      <div class="flex items-center justify-between gap-4 border-b border-gray-100 px-5 py-4">
        <h2 id={titleID} class="font-semibold text-gray-900">{t("deployments.messageDetails")}</h2>
        <button type="button" class="rounded-lg px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-100" onClick={close}>{t("dash.close")}</button>
      </div>
      <pre class="max-h-[65vh] overflow-auto whitespace-pre-wrap break-all p-5 font-mono text-xs leading-5 text-gray-700">{message}</pre>
    </dialog>, document.body)}
  </div>;
}

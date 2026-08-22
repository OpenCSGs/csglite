import { useEffect } from "preact/hooks";
import { locale, t } from "../i18n";

export function ConfirmDialog({
  open,
  title,
  name,
  description,
  confirmLabel,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  name?: string;
  description: string;
  confirmLabel?: string;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  void locale.value;
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) onCancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, busy]);

  if (!open) return null;

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4"
      onClick={() => {
        if (!busy) onCancel();
      }}
    >
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-dialog-title"
        class="w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl"
        onClick={(event) => event.stopPropagation()}
      >
        <div class="flex items-start justify-between gap-4 border-b border-gray-100 px-5 py-4">
          <h2 id="confirm-dialog-title" class="text-lg font-semibold text-gray-900">
            {title}
          </h2>
          <button
            type="button"
            onClick={onCancel}
            disabled={busy}
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 disabled:cursor-not-allowed disabled:opacity-50"
            aria-label={t("dash.close")}
            title={t("dash.close")}
          >
            <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div class="space-y-2 px-5 py-4">
          {name && (
            <p class="break-all text-sm font-medium text-gray-900" title={name}>
              {name}
            </p>
          )}
          <p class="text-sm leading-6 text-gray-500">{description}</p>
        </div>

        <div class="flex justify-end gap-3 border-t border-gray-100 bg-gray-50 px-5 py-4">
          <button
            type="button"
            disabled={busy}
            onClick={onCancel}
            class="rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {t("confirm.cancel")}
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={onConfirm}
            class="rounded-lg bg-rose-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-rose-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {busy ? t("confirm.deleting") : confirmLabel || t("confirm.delete")}
          </button>
        </div>
      </section>
    </div>
  );
}

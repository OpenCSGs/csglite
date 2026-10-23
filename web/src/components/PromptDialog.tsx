import { useEffect, useRef } from "preact/hooks";
import { t } from "../i18n";

export function PromptDialog({
  open,
  title,
  description,
  initialValue = "",
  placeholder,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  description?: string;
  initialValue?: string;
  placeholder?: string;
  confirmLabel?: string;
  onConfirm: (value: string) => void;
  onCancel: () => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (!open) return;
    if (inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open]);

  if (!open) return null;

  const submit = () => {
    const value = (inputRef.current?.value ?? "").trim();
    if (!value) return;
    onConfirm(value);
  };

  return (
    <div
      class="fixed inset-0 z-50 flex items-center justify-center bg-gray-900/40 p-4"
      onClick={onCancel}
    >
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby="prompt-dialog-title"
        class="w-full max-w-md overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-2xl"
        onClick={(event) => event.stopPropagation()}
      >
        <div class="flex items-start justify-between gap-4 border-b border-gray-100 px-5 py-4">
          <h2 id="prompt-dialog-title" class="text-lg font-semibold text-gray-900">
            {title}
          </h2>
          <button
            type="button"
            onClick={onCancel}
            class="rounded-lg p-2 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700"
            aria-label={t("dash.close")}
            title={t("dash.close")}
          >
            <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>

        <div class="space-y-2 px-5 py-4">
          {description && <p class="text-sm leading-6 text-gray-500">{description}</p>}
          <input
            ref={inputRef}
            type="text"
            defaultValue={initialValue}
            placeholder={placeholder}
            onKeyDown={(event) => {
              if (event.key === "Enter") submit();
            }}
            class="w-full rounded-xl border border-gray-300 px-3 py-2 text-sm text-gray-900 focus:border-indigo-500 focus:outline-none focus:ring-2 focus:ring-indigo-200"
          />
        </div>

        <div class="flex justify-end gap-3 border-t border-gray-100 bg-gray-50 px-5 py-4">
          <button
            type="button"
            onClick={onCancel}
            class="rounded-lg border border-gray-200 bg-white px-4 py-2 text-sm text-gray-700 transition-colors hover:bg-gray-50"
          >
            {t("confirm.cancel")}
          </button>
          <button
            type="button"
            onClick={submit}
            class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-indigo-700"
          >
            {confirmLabel || t("confirm.save")}
          </button>
        </div>
      </section>
    </div>
  );
}

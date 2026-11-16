import { createPortal } from "preact/compat";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { licenseFeatureName } from "../license";
import { t } from "../i18n";

export function LicenseFeaturesTooltip({ features }: { features: string[] }) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ top: number; left: number; maxHeight: number }>();
  const trigger = useRef<HTMLButtonElement>(null);
  const tooltip = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout>>();
  const id = "license-enabled-features";
  const keepOpen = () => { clearTimeout(closeTimer.current); setOpen(true); };
  const hideSoon = () => {
    clearTimeout(closeTimer.current);
    closeTimer.current = setTimeout(() => { if (document.activeElement !== trigger.current) setOpen(false); }, 120);
  };
  useEffect(() => () => clearTimeout(closeTimer.current), []);
  useLayoutEffect(() => {
    if (!open) { setPosition(undefined); return; }
    const place = () => {
      const anchor = trigger.current?.getBoundingClientRect(), tip = tooltip.current;
      if (!anchor || !tip) return;
      const width = tip.getBoundingClientRect().width;
      const below = window.innerHeight - anchor.bottom - 16, above = anchor.top - 16;
      const down = below >= tip.scrollHeight || below >= above;
      const maxHeight = Math.max(40, Math.min(320, down ? below : above));
      const height = Math.min(tip.scrollHeight, maxHeight);
      setPosition({ top: down ? anchor.bottom + 8 : Math.max(8, anchor.top - 8 - height),
        left: Math.max(8, Math.min(anchor.left, window.innerWidth - width - 8)), maxHeight });
    };
    const outside = (event: PointerEvent) => {
      if (!trigger.current?.contains(event.target as Node) && !tooltip.current?.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [open, features.join("\n")]);

  if (!features.length) return <span>{t("settings.licenseFeaturesNone")}</span>;
  return <>
    <button ref={trigger} type="button" onPointerEnter={keepOpen} onPointerLeave={hideSoon} onFocus={keepOpen} onBlur={() => setOpen(false)} onClick={keepOpen}
      aria-label={t("settings.licenseFeatureList", features.length)} aria-describedby={open ? id : undefined}
      class="inline-flex items-center gap-1.5 rounded text-indigo-600 underline decoration-dotted underline-offset-4 hover:text-indigo-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2">
      {features.length}<svg aria-hidden="true" class="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"><circle cx="12" cy="12" r="9" /><path d="M12 11v6M12 7v1" /></svg>
    </button>
    {open && createPortal(<div ref={tooltip} id={id} role="tooltip" onPointerEnter={keepOpen} onPointerLeave={hideSoon}
      style={{ ...position, visibility: position ? "visible" : "hidden" }}
      class="fixed z-[100] w-72 max-w-[calc(100vw-2rem)] overflow-y-auto overscroll-contain rounded-xl bg-gray-900 px-4 py-3 text-left text-xs leading-5 text-white shadow-xl">
      <p class="mb-2 font-semibold">{t("settings.licenseFeatures")}</p>
      <ul class="space-y-1">{features.map(feature => <li key={feature} class="flex items-start gap-2 break-words"><span aria-hidden="true" class="shrink-0 text-emerald-300">✓</span><span class="min-w-0 break-words">{licenseFeatureName(feature)}</span></li>)}</ul>
    </div>, document.body)}
  </>;
}

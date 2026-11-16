import type { ComponentChildren } from "preact";
import { createPortal } from "preact/compat";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "preact/hooks";

/** Read-only details reachable by mouse, keyboard and touch, outside table clipping. */
export function InfoTooltip({ label, children, content, class: className = "" }: {
  label: string; children: ComponentChildren; content: ComponentChildren; class?: string;
}) {
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<{ top: number; left: number; maxHeight: number }>();
  const trigger = useRef<HTMLButtonElement>(null);
  const tooltip = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  const id = useId();
  const show = () => { clearTimeout(timer.current); setOpen(true); };
  const hideSoon = () => {
    clearTimeout(timer.current);
    timer.current = setTimeout(() => { if (document.activeElement !== trigger.current) setOpen(false); }, 150);
  };
  useEffect(() => () => clearTimeout(timer.current), []);
  useLayoutEffect(() => {
    if (!open) { setPosition(undefined); return; }
    const place = () => {
      const anchor = trigger.current?.getBoundingClientRect(), tip = tooltip.current;
      if (!anchor || !tip) return;
      const below = window.innerHeight - anchor.bottom - 16, above = anchor.top - 16;
      const down = below >= Math.min(360, tip.scrollHeight) || below >= above;
      const maxHeight = Math.max(40, Math.min(360, down ? below : above));
      const height = Math.min(tip.scrollHeight, maxHeight);
      setPosition({ top: Math.max(8, Math.min(down ? anchor.bottom + 8 : anchor.top - height - 8, window.innerHeight - height - 8)),
        left: Math.max(8, Math.min(anchor.left, window.innerWidth - tip.offsetWidth - 8)), maxHeight });
    };
    const outside = (event: PointerEvent) => {
      if (!trigger.current?.contains(event.target as Node) && !tooltip.current?.contains(event.target as Node)) setOpen(false);
    };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    place();
    const observer = new ResizeObserver(place);
    if (tooltip.current) observer.observe(tooltip.current);
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    document.addEventListener("pointerdown", outside);
    document.addEventListener("keydown", escape);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
      document.removeEventListener("pointerdown", outside);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);
  return <>
    <button ref={trigger} type="button" aria-label={label} aria-describedby={open ? id : undefined}
      onPointerEnter={show} onPointerLeave={hideSoon} onFocus={show} onBlur={() => setOpen(false)} onClick={show}
      class={`max-w-full rounded text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500 focus-visible:ring-offset-2 ${className}`}>{children}</button>
    {open && createPortal(<div ref={tooltip} id={id} role="tooltip" onPointerEnter={show} onPointerLeave={hideSoon}
      style={{ ...position, visibility: position ? "visible" : "hidden" }}
      class="fixed z-[100] w-80 max-w-[calc(100vw-2rem)] overflow-y-auto overscroll-contain rounded-xl border border-gray-200 bg-white p-4 text-left text-xs leading-5 text-gray-700 shadow-xl">
      {content}
    </div>, document.body)}
  </>;
}

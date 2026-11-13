import { useEffect, useRef, useState } from "preact/hooks";

interface MonthPickerProps {
  /** Selected month, "YYYY-MM". */
  value: string;
  /** Latest month that can be picked, "YYYY-MM"; later months are disabled. */
  max: string;
  locale: string;
  label: string;
  /** Labels of the year buttons, for screen readers. */
  prevYearLabel: string;
  nextYearLabel: string;
  onChange: (month: string) => void;
}

const pad = (n: number) => String(n).padStart(2, "0");

// Height of the open panel plus its gap, in pixels.
const PANEL_HEIGHT = 240;

/**
 * A month picker: a button showing the month that opens a year header and a
 * grid of its twelve months. Native <input type="month"> is a plain text box
 * in Safari and the desktop app's WebKit view, so this one is drawn here.
 */
export function MonthPicker({ value, max, locale, label, prevYearLabel, nextYearLabel, onChange }: MonthPickerProps) {
  const [open, setOpen] = useState(false);
  // Opens upward when the panel would not fit below the button.
  const [dropUp, setDropUp] = useState(false);
  const [year, setYear] = useState(() => Number(value.slice(0, 4)));
  const root = useRef<HTMLDivElement>(null);
  const maxYear = Number(max.slice(0, 4));

  useEffect(() => {
    if (!open) return;
    setYear(Number(value.slice(0, 4)));
    const onPointer = (ev: MouseEvent) => {
      if (root.current && !root.current.contains(ev.target as Node)) setOpen(false);
    };
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const monthName = new Intl.DateTimeFormat(locale, { month: "short" });
  const fullName = new Intl.DateTimeFormat(locale, { year: "numeric", month: "long" });
  const [vy, vm] = value.split("-").map(Number);
  const yearName = new Intl.DateTimeFormat(locale, { year: "numeric" }).format(new Date(year, 0, 1));

  const pick = (month: string) => {
    setOpen(false);
    if (month !== value) onChange(month);
  };

  return (
    <div ref={root} class="relative">
      <button
        type="button"
        class="inline-flex items-center gap-2 rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-sm text-gray-800 shadow-sm transition hover:border-indigo-300 hover:bg-indigo-50/40 focus:outline-none focus-visible:ring-2 focus-visible:ring-indigo-300"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={label}
        onClick={() => {
          if (!open && root.current) {
            setDropUp(root.current.getBoundingClientRect().bottom + PANEL_HEIGHT > window.innerHeight);
          }
          setOpen(!open);
        }}
      >
        <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" class="h-4 w-4 text-gray-400" aria-hidden="true">
          <rect x="3" y="4.5" width="14" height="12" rx="2" />
          <path d="M3 8.5h14M7 2.5v4M13 2.5v4" stroke-linecap="round" />
        </svg>
        <span class="tabular-nums">{fullName.format(new Date(vy, vm - 1, 1))}</span>
        <svg viewBox="0 0 20 20" fill="currentColor" class={`h-4 w-4 text-gray-400 transition-transform ${open ? "rotate-180" : ""}`} aria-hidden="true">
          <path d="M5.3 7.3a1 1 0 0 1 1.4 0L10 10.6l3.3-3.3a1 1 0 1 1 1.4 1.4l-4 4a1 1 0 0 1-1.4 0l-4-4a1 1 0 0 1 0-1.4z" />
        </svg>
      </button>
      {open && (
        <div
          role="dialog"
          aria-label={label}
          class={`absolute right-0 z-30 w-64 rounded-xl border border-gray-200 bg-white p-3 shadow-lg ${dropUp ? "bottom-full mb-2" : "top-full mt-2"}`}
        >
          <div class="mb-2 flex items-center justify-between">
            <button
              type="button"
              class="rounded-md p-1 text-gray-500 hover:bg-gray-100 hover:text-gray-800"
              aria-label={prevYearLabel}
              onClick={() => setYear(year - 1)}
            >
              <svg viewBox="0 0 20 20" fill="currentColor" class="h-4 w-4" aria-hidden="true">
                <path d="M12.7 5.3a1 1 0 0 1 0 1.4L9.4 10l3.3 3.3a1 1 0 1 1-1.4 1.4l-4-4a1 1 0 0 1 0-1.4l4-4a1 1 0 0 1 1.4 0z" />
              </svg>
            </button>
            <span class="text-sm font-semibold text-gray-900 tabular-nums">{yearName}</span>
            <button
              type="button"
              class="rounded-md p-1 text-gray-500 hover:bg-gray-100 hover:text-gray-800 disabled:cursor-not-allowed disabled:opacity-30 disabled:hover:bg-transparent"
              aria-label={nextYearLabel}
              disabled={year >= maxYear}
              onClick={() => setYear(year + 1)}
            >
              <svg viewBox="0 0 20 20" fill="currentColor" class="h-4 w-4" aria-hidden="true">
                <path d="M7.3 14.7a1 1 0 0 1 0-1.4l3.3-3.3-3.3-3.3a1 1 0 0 1 1.4-1.4l4 4a1 1 0 0 1 0 1.4l-4 4a1 1 0 0 1-1.4 0z" />
              </svg>
            </button>
          </div>
          <div class="grid grid-cols-3 gap-1.5">
            {Array.from({ length: 12 }, (_, i) => {
              const month = `${year}-${pad(i + 1)}`;
              const selected = month === value;
              const current = month === max;
              const disabled = month > max;
              return (
                <button
                  key={month}
                  type="button"
                  disabled={disabled}
                  aria-pressed={selected}
                  aria-label={fullName.format(new Date(year, i, 1))}
                  class={`relative rounded-lg py-2 text-sm transition ${
                    selected
                      ? "bg-indigo-600 font-medium text-white shadow-sm"
                      : disabled
                        ? "cursor-not-allowed text-gray-300"
                        : "text-gray-700 hover:bg-indigo-50 hover:text-indigo-700"
                  }`}
                  onClick={() => pick(month)}
                >
                  {monthName.format(new Date(year, i, 1))}
                  {current && !selected && <span class="absolute bottom-1 left-1/2 h-1 w-1 -translate-x-1/2 rounded-full bg-indigo-500" aria-hidden="true" />}
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

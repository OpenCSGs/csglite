import { useLayoutEffect, useRef, useState } from "preact/hooks";
import { t } from "../i18n";
import { isDecisionTag, orderModelTags } from "../utils/modelTags";

/**
 * ModelTags shows a model's card tags on one line. Tags that do not fit are
 * collapsed behind a "+N" button that expands the full list.
 */
export function ModelTags({ tags }: { tags: string[] }) {
  const ordered = orderModelTags(tags);
  const listRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [hidden, setHidden] = useState(0);

  // Count the chips that wrapped below the first row; only measured while
  // collapsed, where the list is clipped to a single row.
  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || expanded) return;
    const measure = () => {
      const chips = Array.from(list.children) as HTMLElement[];
      const firstTop = chips[0]?.offsetTop ?? 0;
      setHidden(chips.filter((chip) => chip.offsetTop > firstTop).length);
    };
    measure();
    // Chips change width without the row resizing when the web font loads,
    // so watch every chip as well as the row.
    const observer = new ResizeObserver(measure);
    observer.observe(list);
    for (const chip of Array.from(list.children)) observer.observe(chip);
    let active = true;
    document.fonts?.ready.then(() => { if (active) measure(); });
    return () => {
      active = false;
      observer.disconnect();
    };
  }, [expanded, ordered.join("\u0000")]);

  if (ordered.length === 0) return null;

  return (
    <div class="flex items-start gap-2 text-sm">
      <span class="mt-0.5 shrink-0 text-gray-400">{t("lib.tagsLabel")}</span>
      <div
        ref={listRef}
        class={`flex min-w-0 flex-1 flex-wrap gap-1.5 ${expanded ? "" : "h-6 overflow-hidden"}`}
      >
        {ordered.map((tag, i) => (
          <TagChip key={`${i}:${tag}`} tag={tag} />
        ))}
      </div>
      {/* The toggle keeps a fixed-width slot even when nothing is hidden, so
          showing it never narrows the row and invalidates the count. */}
      <button
        type="button"
        onClick={() => setExpanded(!expanded)}
        class={`h-6 w-14 shrink-0 rounded-full text-xs font-medium text-indigo-600 hover:bg-indigo-50 ${
          hidden > 0 || expanded ? "" : "invisible"
        }`}
        aria-expanded={expanded}
        title={expanded ? undefined : t("lib.tagsMoreHint", hidden)}
      >
        {expanded ? t("lib.tagsCollapse") : `+${hidden}`}
      </button>
    </div>
  );
}

function TagChip({ tag }: { tag: string }) {
  const decision = isDecisionTag(tag);
  return (
    <span
      class={`inline-flex h-6 items-center gap-1 rounded-full border px-2 text-xs font-medium ${
        decision
          ? "border-amber-200 bg-amber-50 text-amber-700"
          : "border-gray-200 bg-white text-gray-600"
      }`}
      title={decision ? t("lib.decisionTagHint") : tag}
    >
      {decision ? (
        <svg class="h-3 w-3 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" d="M13 2L4.5 13.5H12L11 22l8.5-11.5H12L13 2z" />
        </svg>
      ) : (
        <svg class="h-3 w-3 shrink-0 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2.2" aria-hidden="true">
          <path stroke-linecap="round" stroke-linejoin="round" d="M7 7h.01M3 11.6V5a2 2 0 012-2h6.6a2 2 0 011.4.6l8.4 8.4a2 2 0 010 2.8l-6.6 6.6a2 2 0 01-2.8 0L3.6 13a2 2 0 01-.6-1.4z" />
        </svg>
      )}
      <span class="max-w-[180px] truncate">{tag}</span>
    </span>
  );
}

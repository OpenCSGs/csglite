import { beforeAll, describe, expect, it, vi } from "vitest";

let en: Record<string, string>;
let zh: Record<string, string>;

beforeAll(async () => {
  // i18n.ts reads the saved locale at module scope, so localStorage has to exist
  // before it is imported, the same way downloads.test.ts arranges it.
  const values = new Map<string, string>();
  vi.stubGlobal("localStorage", {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => values.set(key, value),
    removeItem: (key: string) => values.delete(key),
  });
  ({ en, zh } = await import("./i18n"));
});

// A key present in only one catalogue renders as the raw key for users of the
// other locale, which is how "chat.copyModel" shipped untranslated.
describe("i18n catalogues", () => {
  it("defines every English key in Chinese", () => {
    expect(Object.keys(en).filter((key) => !(key in zh))).toEqual([]);
  });

  it("defines every Chinese key in English", () => {
    expect(Object.keys(zh).filter((key) => !(key in en))).toEqual([]);
  });

  it("leaves no value empty", () => {
    const empty = [
      ...Object.entries(en).filter(([, v]) => !v.trim()).map(([k]) => `en:${k}`),
      ...Object.entries(zh).filter(([, v]) => !v.trim()).map(([k]) => `zh:${k}`),
    ];
    expect(empty).toEqual([]);
  });
});

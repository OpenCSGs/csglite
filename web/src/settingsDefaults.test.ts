import { afterEach, describe, expect, it, vi } from "vitest";
import type { AppSettings } from "./api/client";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});
vi.mock("./api/client", () => ({ saveSettings: vi.fn() }));

import { saveSettings } from "./api/client";
import { locale } from "./i18n";
import { restoreApplicationDefaults } from "./settingsDefaults";

const originalLocale = locale.value;
afterEach(() => {
  locale.value = originalLocale;
  vi.resetAllMocks();
});

describe("restore application defaults", () => {
  it.each([
    [
      "en",
      "Application defaults restored. Your community and sign-in were kept.",
    ],
    ["zh", "已恢复应用默认设置，保留当前社区及登录状态。"],
  ] as const)(
    "preserves the community and explains that in %s",
    async (language, message) => {
      locale.value = language;
      const current = {
        server_url: "https://community.example.com",
        ai_gateway_url: "https://community.example.com/aigateway",
      } as AppSettings;
      vi.mocked(saveSettings).mockImplementationOnce(async (payload) => {
        // Including URLs or credentials here would change the community or sign out.
        expect(payload).not.toHaveProperty("server_url");
        expect(payload).not.toHaveProperty("ai_gateway_url");
        expect(payload).not.toHaveProperty("token");
        expect(payload).not.toHaveProperty("opencsg_api_key");
        return { ...current, ...payload };
      });
      const result = await restoreApplicationDefaults();
      expect(result.settings).toMatchObject(current);
      expect(result.settings).toMatchObject({
        cloud_provider_name: "",
        llama_use_model_max_ctx: false,
        llama_num_parallel: 1,
        context_compression: "off",
      });
      expect(result.message).toBe(message);
      expect(saveSettings).toHaveBeenCalledTimes(1);
    },
  );

  it("does not report success if saving defaults fails", async () => {
    vi.mocked(saveSettings).mockRejectedValueOnce(new Error("save failed"));
    await expect(restoreApplicationDefaults()).rejects.toThrow("save failed");
  });
});

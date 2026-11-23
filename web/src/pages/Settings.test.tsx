// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://localhost"}
import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, expect, it, vi } from "vitest";
import { cloudAuth, setCloudAuth } from "../cloudAccount";
import { setLocale } from "../i18n";
import { Settings } from "./Settings";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

let container: HTMLDivElement | undefined;
afterEach(async () => {
  if (container) {
    await act(() => render(null, container!));
    container.remove();
    container = undefined;
  }
  vi.unstubAllGlobals();
});

it.each([
  { authenticated: true, providerName: "new-provider", feedback: "测试账号" },
  { authenticated: false, providerName: "csghub", feedback: "已保存访问令牌，暂未能验证账号" },
])("uses the shared Save action for OpenCSG settings (verified=$authenticated)", async ({ authenticated, providerName, feedback }) => {
  setLocale("zh");
  const signedOut = {
    auth_mode: "token",
    has_token: false,
    authenticated: false,
    has_api_key: false,
    login_url: "",
    access_token_url: "https://opencsg.example/settings/access-token",
  };
  setCloudAuth(signedOut);
  const settings = { cloud_provider_name: "csghub" };
  const fetcher = vi.fn(async (url: string, options?: RequestInit) => {
    if (url === "/api/settings") {
      return Response.json(options?.method === "POST" ? { cloud_provider_name: providerName } : settings);
    }
    if (url === "/api/cloud/auth/token" && options?.method === "POST") {
      return Response.json({
        ...signedOut,
        has_token: true,
        authenticated,
        user: authenticated ? { username: "account", nickname: "测试账号" } : null,
      });
    }
    return Response.json(url === "/api/cloud/auth" ? signedOut : {});
  });
  vi.stubGlobal("fetch", fetcher);
  const changed = vi.fn();
  window.addEventListener("csghub:providers-changed", changed);
  try {
    const page = document.createElement("div");
    container = page;
    document.body.append(page);
    await act(() => render(<Settings />, page));

    const input = page.querySelector<HTMLInputElement>("#opencsg-access-token")!;
    const form = input.form!;
    const provider = form.querySelector<HTMLInputElement>('input[type="text"]')!;
    const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    await vi.waitFor(() => expect(provider.value).toBe(settings.cloud_provider_name));
    expect(input.type).toBe("password");
    expect(form.querySelector("a")?.href).toBe(signedOut.access_token_url);
    expect(provider.compareDocumentPosition(input) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(input.compareDocumentPosition(submit) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(submit.textContent).toBe("保存");
    expect([...form.querySelectorAll("button")].map((button) => button.textContent)).toEqual(["刷新模型", "保存"]);
    await act(() => {
      provider.value = providerName;
      provider.dispatchEvent(new Event("input", { bubbles: true }));
      input.value = "  fixture-access-token  ";
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
    expect(submit.disabled).toBe(false);
    await act(() => submit.click());
    await vi.waitFor(() => {
      expect(fetcher).toHaveBeenCalledWith("/api/cloud/auth/token", expect.objectContaining({
        method: "POST", body: JSON.stringify({ token: "fixture-access-token" }),
      }));
      expect(input.value).toBe("");
      expect(form.querySelector('[role="status"]')?.textContent).toContain(feedback);
      expect(cloudAuth.value?.authenticated).toBe(authenticated);
      const settingsWrites = fetcher.mock.calls.filter(([url, options]) => url === "/api/settings" && options?.method === "POST");
      if (providerName !== settings.cloud_provider_name) {
        expect(settingsWrites.map(([, options]) => options?.body)).toEqual([JSON.stringify({ cloud_provider_name: providerName })]);
        expect(changed).toHaveBeenCalledTimes(2);
      } else {
        expect(settingsWrites).toHaveLength(0);
        expect(changed).toHaveBeenCalledTimes(1);
      }
    });
  } finally {
    window.removeEventListener("csghub:providers-changed", changed);
  }
});

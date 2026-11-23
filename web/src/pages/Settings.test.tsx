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
  { authenticated: true, feedback: "测试账号" },
  { authenticated: false, feedback: "已保存访问令牌，暂未能验证账号" },
])("signs in from Settings with an OpenCSG access token (verified=$authenticated)", async ({ authenticated, feedback }) => {
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
  const fetcher = vi.fn(async (url: string, options?: RequestInit) => {
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
    const submit = form.querySelector<HTMLButtonElement>('button[type="submit"]')!;
    expect(input.type).toBe("password");
    expect(form.querySelector("a")?.href).toBe(signedOut.access_token_url);
    expect(submit.disabled).toBe(true);
    await act(() => {
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
      expect(changed).toHaveBeenCalledTimes(1);
    });
  } finally {
    window.removeEventListener("csghub:providers-changed", changed);
  }
});

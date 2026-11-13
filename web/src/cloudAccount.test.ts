import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AppSettings, CloudAuthStatus } from "./api/client";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});
vi.mock("./api/client", () => ({
  getCloudAuthStatus: vi.fn(),
  saveSettings: vi.fn(),
}));

import { getCloudAuthStatus, saveSettings } from "./api/client";
import {
  cloudAuth,
  cloudAuthError,
  communitySettings,
  invalidateCloudAuth,
  refreshCloudAuth,
  saveCommunitySelection,
  savingCommunity,
  setCloudAuth,
} from "./cloudAccount";

const signedOut: CloudAuthStatus = {
  auth_mode: "token",
  has_token: false,
  authenticated: false,
  login_url: "https://example.com/login",
  access_token_url: "",
  has_api_key: false,
};
const signedIn: CloudAuthStatus = {
  ...signedOut,
  has_token: true,
  authenticated: true,
  user: { username: "demo" },
};

beforeEach(() => {
  vi.mocked(getCloudAuthStatus).mockReset().mockResolvedValue(signedOut);
  vi.mocked(saveSettings).mockReset();
  vi.stubGlobal("window", { dispatchEvent: vi.fn() });
  communitySettings.value = null;
  setCloudAuth(signedOut);
});

describe("shared community account", () => {
  it("persists a community choice without requiring a login", async () => {
    const settings = {
      server_url: "https://sanxia.opencsg.com",
      ai_gateway_url: "https://sanxia.opencsg.com/aigateway",
    } as AppSettings;
    vi.mocked(saveSettings).mockResolvedValueOnce(settings);
    await saveCommunitySelection(settings.server_url, settings.ai_gateway_url);
    expect(saveSettings).toHaveBeenCalledWith({
      server_url: settings.server_url,
      ai_gateway_url: settings.ai_gateway_url,
    });
    expect(communitySettings.value).toEqual(settings);
    expect(savingCommunity.value).toBe(false);
    expect(cloudAuth.value?.authenticated).toBe(false);
  });

  it("serializes community writes so a slow earlier choice cannot overwrite the latest", async () => {
    const firstSettings = {
      server_url: "https://first.example",
      ai_gateway_url: "https://first.example/aigateway",
    } as AppSettings;
    const lastSettings = {
      server_url: "https://last.example",
      ai_gateway_url: "https://last.example/aigateway",
    } as AppSettings;
    let finish!: (settings: AppSettings) => void;
    vi.mocked(saveSettings)
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValueOnce(lastSettings);
    const first = saveCommunitySelection(
      firstSettings.server_url,
      firstSettings.ai_gateway_url,
    );
    await vi.waitFor(() => expect(saveSettings).toHaveBeenCalledTimes(1));
    const last = saveCommunitySelection(
      lastSettings.server_url,
      lastSettings.ai_gateway_url,
    );
    finish(firstSettings);
    await Promise.all([first, last]);
    expect(communitySettings.value).toEqual(lastSettings);
    expect(savingCommunity.value).toBe(false);
  });

  it("does not change communities while a token is present", async () => {
    vi.mocked(getCloudAuthStatus).mockResolvedValueOnce(signedIn);
    await expect(
      saveCommunitySelection(
        "https://another.example",
        "https://another.example/aigateway",
      ),
    ).rejects.toThrow();
    expect(saveSettings).not.toHaveBeenCalled();
    expect(savingCommunity.value).toBe(false);
  });

  it("shares one request when navigation and Settings refresh together", async () => {
    let finish!: (status: CloudAuthStatus) => void;
    vi.mocked(getCloudAuthStatus).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const navigation = refreshCloudAuth();
    const settings = refreshCloudAuth();
    expect(getCloudAuthStatus).toHaveBeenCalledTimes(1);
    finish(signedIn);
    await Promise.all([navigation, settings]);
    expect(cloudAuth.value).toEqual(signedIn);
  });

  it("does not restore a signed-in account from a request that finishes after logout", async () => {
    let finish!: (status: CloudAuthStatus) => void;
    vi.mocked(getCloudAuthStatus).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const oldRequest = refreshCloudAuth();
    setCloudAuth(signedOut);
    finish(signedIn);
    expect(await oldRequest).toBeNull();
    expect(cloudAuth.value).toEqual(signedOut);
  });

  it("ignores the previous community's response after a site change", async () => {
    let finish!: (status: CloudAuthStatus) => void;
    vi.mocked(getCloudAuthStatus).mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
    const previousCommunity = refreshCloudAuth();
    invalidateCloudAuth();
    const nextCommunity = {
      ...signedOut,
      login_url: "https://another.example/login",
    };
    vi.mocked(getCloudAuthStatus).mockResolvedValueOnce(nextCommunity);
    await refreshCloudAuth();
    finish(signedIn);
    await previousCommunity;
    expect(cloudAuth.value).toEqual(nextCommunity);
  });

  it("retains the last known account on a network failure and clears the error on retry", async () => {
    setCloudAuth(signedIn);
    vi.mocked(getCloudAuthStatus).mockRejectedValueOnce(new Error("offline"));
    expect(await refreshCloudAuth()).toBeNull();
    expect(cloudAuth.value).toEqual(signedIn);
    expect(cloudAuthError.value).not.toBe("");
    vi.mocked(getCloudAuthStatus).mockResolvedValueOnce(signedIn);
    await refreshCloudAuth();
    expect(cloudAuthError.value).toBe("");
  });
});

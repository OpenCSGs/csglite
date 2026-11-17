// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://localhost"}
import { render } from "preact";
import { act } from "preact/test-utils";
import { LocationProvider } from "preact-iso";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AIGateway } from "./AIGateway";
import { setLocale } from "../i18n";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

let container: HTMLDivElement;

beforeEach(() => {
  setLocale("en");
  container = document.createElement("div");
  document.body.append(container);
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, value() { this.open = true; } },
    close: { configurable: true, value() { this.open = false; } },
  });
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const path = new URL(input, "http://localhost").pathname;
    const bodies: Record<string, unknown> = {
      "/api/settings": { cloud_provider_enabled: true, cloud_provider_name: "Test provider" },
      "/api/providers": { providers: [] },
      "/api/provider-pools": { pools: [] },
      "/api/api-keys": { auth_enabled: false, keys: [] },
      "/api/api-usage": { summary: {}, daily: [], keys: [], providers: [], models: [] },
      "/api/cluster": { mode: "lan", members: [] },
      "/api/tags": { models: [] },
      "/api/tags/manage": { models: [] },
    };
    if (!(path in bodies)) throw new Error(`Unexpected request: ${input}`);
    return new Response(JSON.stringify(bodies[path]), { headers: { "Content-Type": "application/json" } });
  }));
});

afterEach(async () => {
  await act(() => { render(null, container); });
  container.remove();
  vi.unstubAllGlobals();
});

async function mountGateway() {
  await act(() => { render(<LocationProvider><AIGateway /></LocationProvider>, container); });
}

async function clickButton(label: string) {
  await vi.waitFor(async () => {
    const button = Array.from(container.querySelectorAll("button")).find(button => button.textContent === label);
    expect(button, `button ${label}: ${container.textContent}`).toBeDefined();
    await act(() => { button!.click(); });
  });
}

it("opens the provider catalog only for a new publish action, not when returning to the gateway", async () => {
  await mountGateway();
  expect(container.querySelector("dialog[open]")).toBeNull();

  for (let visit = 0; visit < 2; visit++) {
    await clickButton("Providers");
    await clickButton("Publish models");
    expect(container.querySelector("dialog[open]")).not.toBeNull();
    expect(container.querySelector<HTMLSelectElement>('select[aria-label="Source"]')?.value).toBe("cloud");
    await clickButton("Close");
    expect(container.querySelector("dialog[open]")).toBeNull();

    // The router unmounts the page when another navigation item is selected.
    await act(() => { render(null, container); });
    await mountGateway();
    expect(container.querySelector("dialog[open]")).toBeNull();
  }

  await clickButton("Add model");
  expect(container.querySelector("dialog[open]")).not.toBeNull();
});

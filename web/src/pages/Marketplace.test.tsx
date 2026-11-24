// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://localhost"}
import { render } from "preact";
import { act } from "preact/test-utils";
import { LocationProvider } from "preact-iso";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Marketplace } from "./Marketplace";
import { setLocale } from "../i18n";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

let container: HTMLDivElement;
let listRequests: string[];

beforeEach(() => {
  setLocale("en");
  listRequests = [];
  container = document.createElement("div");
  document.body.append(container);
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    const url = new URL(input, "http://localhost");
    switch (url.pathname) {
      case "/api/settings":
        return new Response(JSON.stringify({
          marketplace_model_source: "opencsg",
          marketplace_dataset_source: "opencsg",
        }), { headers: { "Content-Type": "application/json" } });
      case "/api/tags":
        return new Response(JSON.stringify({ models: [] }), { headers: { "Content-Type": "application/json" } });
      case "/api/datasets":
        return new Response(JSON.stringify({ datasets: [] }), { headers: { "Content-Type": "application/json" } });
      case "/api/marketplace/models": {
        listRequests.push(url.search);
        if (url.searchParams.get("page") === "1") {
          return new Response(JSON.stringify({
            data: [],
            total: 100,
            has_more: true,
            total_exact: true,
            exclude_paths: [],
          }), { headers: { "Content-Type": "application/json" } });
        }
        return new Response("rate limited", {
          status: 429,
          statusText: "Too Many Requests",
          headers: { "Content-Type": "text/plain" },
        });
      }
      default:
        throw new Error(`Unexpected request: ${input}`);
    }
  }));
});

afterEach(async () => {
  await act(() => { render(null, container); });
  container.remove();
  vi.unstubAllGlobals();
});

async function mountMarketplace() {
  await act(() => { render(<LocationProvider><Marketplace /></LocationProvider>, container); });
}

async function clickButton(label: string) {
  await vi.waitFor(async () => {
    const button = Array.from(container.querySelectorAll("button")).find((button) => button.textContent === label);
    expect(button, `button ${label}: ${container.textContent}`).toBeDefined();
    await act(() => { button!.click(); });
  });
}

function lastListPage(): string | null {
  return new URL(listRequests[listRequests.length - 1], "http://localhost").searchParams.get("page");
}

async function flush() {
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
}

it("keeps the page after a failed next-page request so a retry does not skip rows", async () => {
  await mountMarketplace();
  await vi.waitFor(() => expect(listRequests.length).toBe(1));
  expect(lastListPage()).toBe("1");

  await clickButton("Next");
  await vi.waitFor(() => expect(listRequests.length).toBe(2));
  expect(lastListPage()).toBe("2");
  await flush();

  // The 429 rolls the page back; retrying "Next" must request page 2 again
  // instead of page 3.
  await clickButton("Next");
  await vi.waitFor(() => expect(listRequests.length).toBe(3));
  expect(lastListPage()).toBe("2");
});

// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://localhost/edge?tab=models"}
import { render } from "preact";
import { act } from "preact/test-utils";
import { expect, it, vi } from "vitest";
import { setLocale } from "../i18n";
import { EdgeServing } from "./EdgeServing";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

it("groups shareable models by source and submits the selected routed ID", async () => {
  setLocale("en");
  const clusterID = "edge:cluster:acme/chat";
  const unavailableID = "edge:cluster:acme/offline";
  const fetcher = vi.fn(async (url: string, options?: RequestInit) => {
    if (url === "/api/edge") {
      return Response.json({
        enabled: true, state: "connected", signed_in: true, model_limit: 1,
        reconnects: 0, applications_changed: 0,
        models: [
          { id: "acme/chat", name: "chat", ready: true },
          { id: "edge:gateway:service", name: "chat", source: "gateway", ready: true },
          { id: clusterID, name: "chat", source: "cluster", ready: true },
          { id: unavailableID, name: "offline", source: "cluster", ready: false },
        ],
      });
    }
    return Response.json({ data: options?.method === "POST" ? { id: 1 } : [] });
  });
  vi.stubGlobal("fetch", fetcher);
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => render(<EdgeServing />, container));
    const apply = await vi.waitFor(() => {
      const button = [...container.querySelectorAll("button")].find((item) => item.textContent?.trim() === "Apply for a model");
      expect(button).toBeDefined();
      return button!;
    });
    await act(() => apply.click());
    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')!;
    const selector = dialog.querySelector("select")!;
    const groups = [...selector.querySelectorAll("optgroup")];
    expect(groups.map((group) => group.label)).toEqual(["Local", "AI Gateway", "Cluster"]);
    expect(groups.map((group) => group.querySelector("option")?.value)).toEqual(["acme/chat", "edge:gateway:service", clusterID]);
    expect([...selector.options].find((option) => option.value === unavailableID)?.disabled).toBe(true);
    await act(() => {
      selector.value = clusterID;
      selector.dispatchEvent(new Event("change", { bubbles: true }));
      for (const price of dialog.querySelectorAll<HTMLInputElement>('input[inputmode="decimal"]')) {
        price.value = "1.00";
        price.dispatchEvent(new Event("input", { bubbles: true }));
      }
    });
    await act(() => dialog.querySelector<HTMLButtonElement>('button[type="submit"]')!.click());
    await vi.waitFor(() => {
      const submission = fetcher.mock.calls.find(([url, options]) => url === "/api/edge/applications" && options?.method === "POST");
      expect(JSON.parse(String(submission?.[1]?.body))).toMatchObject({
        model_name: "chat", local_model_id: clusterID, prompt_price: 100, completion_price: 100,
      });
    });
  } finally {
    await act(() => render(null, container));
    container.remove();
    vi.unstubAllGlobals();
  }
});

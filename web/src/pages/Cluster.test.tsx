// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://localhost"}
import { render } from "preact";
import { act } from "preact/test-utils";
import { LocationProvider } from "preact-iso";
import { expect, it, vi } from "vitest";
import { setLocale } from "../i18n";
import { Cluster } from "./Cluster";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

it("lets the Master confirm removal of online and offline members", async () => {
  setLocale("en");
  const node = { uuid: "master", name: "Master", local: true, online: true, health: "healthy" };
  let members = [node,
    { uuid: "online", name: "Online worker", local: false, online: true, health: "healthy" },
    { uuid: "offline", name: "Offline worker", local: false, online: false, health: "down" },
  ];
  const cluster = { uuid: "cluster", name: "Test cluster", topology: "public", gateway_uuid: node.uuid };
  const fetcher = vi.fn(async (path: string, init?: RequestInit) => {
    if (init?.method === "DELETE") {
      const uuid = path.slice(path.lastIndexOf("/") + 1);
      members = members.filter(member => member.uuid !== uuid);
    }
    return Response.json({ mode: "gateway", node, cluster, members, node_limit: 0, settings: { state: "active" } });
  });
  vi.stubGlobal("fetch", fetcher);
  const container = document.createElement("div");
  document.body.append(container);
  try {
    await act(() => render(<LocationProvider><Cluster /></LocationProvider>, container));
    await vi.waitFor(() => expect(container.querySelector('[aria-label="Remove node Online worker"]')).not.toBeNull());
    expect(container.querySelector('[aria-label="Remove node Master"]')).toBeNull();
    for (const [uuid, name] of [["online", "Online worker"], ["offline", "Offline worker"]]) {
      await act(() => container.querySelector<HTMLButtonElement>(`[aria-label="Remove node ${name}"]`)!.click());
      const modal = container.querySelector('[role="dialog"]')!;
      expect(modal.textContent).toContain(name);
      expect(fetcher.mock.calls.some(([path]) => path.endsWith(`/nodes/${uuid}`))).toBe(false);
      const confirm = Array.from(modal.querySelectorAll("button")).find(button => button.textContent === "Remove node")!;
      await act(() => confirm.click());
      expect(fetcher).toHaveBeenCalledWith(`/api/cluster/nodes/${uuid}`, expect.objectContaining({ method: "DELETE" }));
      await vi.waitFor(() => expect(container.querySelector(`[aria-label="Remove node ${name}"]`)).toBeNull());
    }
  } finally {
    await act(() => render(null, container));
    container.remove();
    vi.unstubAllGlobals();
  }
});

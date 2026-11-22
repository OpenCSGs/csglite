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

it("opens a confirmation from the Remove option on online and offline nodes", async () => {
  setLocale("zh");
  const node = { uuid: "master", name: "Master", local: true, online: true, health: "healthy" };
  const status = { managed_by: node.uuid, node_state_management: true, state: "active", licensed: true, gpus: [], models: [], inflight: 0 };
  let members = [node,
    { uuid: "online", name: "Online worker", local: false, online: true, health: "healthy", status },
    { uuid: "offline", name: "Offline worker", local: false, online: false, health: "down", status: { ...status, state: "drain" } },
    { uuid: "missing", name: "Worker without status", local: false, online: false, health: "down" },
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
    await vi.waitFor(() => expect(container.querySelector('select[aria-label="节点操作（Online worker）"]')).not.toBeNull());
    const localMenu = container.querySelector('select[aria-label="节点工作状态：Master"]');
    expect(localMenu).not.toBeNull();
    expect(localMenu?.querySelector('option[value="remove"]')).toBeNull();
    expect(Array.from(container.querySelectorAll("button")).filter(button => button.textContent === "移除")).toHaveLength(0);
    for (const [uuid, name, previous] of [
      ["online", "Online worker", "active"],
      ["offline", "Offline worker", "drain"],
      ["missing", "Worker without status", ""],
    ]) {
      const selector = `select[aria-label="节点操作（${name}）"]`;
      const menu = container.querySelector<HTMLSelectElement>(selector)!;
      const option = menu.querySelector<HTMLOptionElement>('option[value="remove"]')!;
      expect(option.textContent).toBe("移除");
      expect(option.disabled).toBe(false);
      expect(menu.disabled).toBe(false);
      if (uuid === "offline") expect(menu.querySelector<HTMLOptionElement>('option[value="active"]')?.disabled).toBe(true);

      const selectRemove = () => act(() => {
        menu.value = "remove";
        menu.dispatchEvent(new Event("change", { bubbles: true }));
      });
      await selectRemove();
      const modal = () => container.querySelector('[role="dialog"]');
      expect(modal()?.textContent).toContain(name);
      expect(menu.value).toBe(previous);
      expect(fetcher.mock.calls.some(([path]) => path.endsWith(`/nodes/${uuid}`))).toBe(false);

      await act(() => Array.from(modal()!.querySelectorAll("button")).find(button => button.textContent === "取消")!.click());
      expect(modal()).toBeNull();
      expect(fetcher.mock.calls.some(([path]) => path.endsWith(`/nodes/${uuid}`))).toBe(false);

      await selectRemove();
      await act(() => Array.from(modal()!.querySelectorAll("button")).find(button => button.textContent === "移除节点")!.click());
      expect(fetcher).toHaveBeenCalledWith(`/api/cluster/nodes/${uuid}`, expect.objectContaining({ method: "DELETE" }));
      await vi.waitFor(() => expect(container.querySelector(selector)).toBeNull());
    }
  } finally {
    await act(() => render(null, container));
    container.remove();
    vi.unstubAllGlobals();
  }
});

// @vitest-environment jsdom
// @vitest-environment-options {"url":"http://localhost"}
import { render } from "preact";
import { act } from "preact/test-utils";
import { afterEach, expect, it, vi } from "vitest";
import { createClusterDeployment } from "../api/client";
import type { ClusterView } from "../api/client";
import { ClusterDeploymentControls } from "./ClusterDeployments";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

vi.mock("../api/client", () => ({
  createClusterDeployment: vi.fn(async () => ({})),
  getMarketplaceModels: vi.fn(async () => ({ data: [] })),
  getMarketplaceModelDetail: vi.fn(async () => ({
    details: { path: "example/model", tags: [{ category: "task", name: "text-generation" }] },
    local_inference: { supported: true, mode: "convert" },
  })),
}));

afterEach(() => {
  render(null, document.body);
});

it("lets a new native SGLang deployment set idle keep-alive", async () => {
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: { configurable: true, value() { this.open = true; } },
    close: { configurable: true, value() { this.open = false; } },
  });
  const view = {
    node: { uuid: "master" },
    members: [{
      uuid: "worker",
      name: "ROCm worker",
      online: true,
      status: {
        deployment_capable: true, deployment_keep_alive: true, licensed: true,
        accept_work: true, state: "active", managed_by: "master",
        gpus: [], ram: { total: 64 * 1024 ** 3, used: 0 },
        engines: [{ name: "sglang", known: true, available: true }],
      },
    }],
  } as unknown as ClusterView;

  await act(() => {
    render(<ClusterDeploymentControls view={view} deployments={[]} publications={null} stale={false} onChanged={async () => {}}>
      {({ create }) => <button onClick={create}>Create deployment</button>}
    </ClusterDeploymentControls>, document.body);
  });
  await act(() => document.querySelector("button")!.click());
  await act(() => {
    const repository = document.querySelector<HTMLInputElement>("#deployment-repository")!;
    repository.value = "example/model";
    repository.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await vi.waitFor(() => {
    expect(document.querySelector("[data-deployment-runtime]")?.textContent).toContain("SGLang");
  });

  const keepAlive = document.querySelector<HTMLInputElement>("#deployment-keep-alive");
  expect(keepAlive?.value).toBe("-1");
  await act(() => {
    keepAlive!.value = "5m";
    keepAlive!.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await act(() => {
    document.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
  expect(createClusterDeployment).toHaveBeenCalledWith(expect.objectContaining({
    backend: "sglang", node_uuid: "worker", keep_alive: "5m",
  }));
});

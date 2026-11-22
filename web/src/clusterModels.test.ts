import { describe, expect, it } from "vitest";
import type { ClusterNodeView, ClusterNodeModelStatus, ClusterNodeStatus, ClusterView, ClusterDeployment, ProviderPool } from "./api/client";
import { canPublishModelInstance, filterModelInstances, isManagedModelInstance, mergeClusterDeployments, modelInstanceRows, modelRowState, modelNodeState, measuredRate, modelPublication, modelActionAvailability, modelTestTargets } from "./clusterModels";
const model = (id: string, loaded = true): ClusterNodeModelStatus => ({ id, repo: "org/shared", size: 1024, loaded, loading: false, active: 0, native_tool_streaming: false, perf: { decode_tps: 32.42, samples: 2 } });
const node = (uuid: string, online: boolean, models: ClusterNodeModelStatus[]): ClusterNodeView => ({ uuid, name: uuid, online, local: false, health: "healthy", status: { models } as ClusterNodeStatus });
describe("model instance table", () => {
  it("keeps exact IDs, registry variants and copies on separate machines", () => {
    const rows = modelInstanceRows([node("a", true, [model("qwen"), model("hf/qwen")]), node("b", true, [model("qwen")])], []);
    expect(rows).toHaveLength(3);
    expect(new Set(rows.map(row => row.key)).size).toBe(3);
    expect(rows.filter(row => row.model.id === "qwen")).toHaveLength(2);
  });
  it("orders running models first and never shows stale offline throughput as live", () => {
    const rows = modelInstanceRows([node("offline", false, [model("qwen")]), node("stored", true, [model("qwen", false)]), node("running", true, [model("qwen")])], []);
    expect(rows.map(row => row.node.uuid)).toEqual(["running", "stored", "offline"]);
    expect(rows.map(measuredRate)).toEqual(["32.4", "—", "—"]);
    expect(rows.map(modelRowState)).toEqual(["cluster.instanceRunning", "cluster.instanceSleeping", "cluster.healthOffline"]);
  });
  it("does not substitute default estimates for missing measurements", () => {
    const m = model("qwen"); m.perf = { decode_tps: 45 };
    expect(measuredRate(modelInstanceRows([node("a", true, [m])], [])[0])).toBe("—");
  });
  it("includes unfinished downloads without creating duplicate stored instances", () => {
    const distribution = [{id:"qwen", repository:"org/shared", size:0, nodes:[], syncs:[{uuid:"a",name:"a",status:"running"}]}];
    const pending = modelInstanceRows([node("a", true, [])], distribution);
    expect(pending).toHaveLength(1);
    expect(modelRowState(pending[0])).toBe("cluster.cellSyncing");
    const present = modelInstanceRows([node("a", true, [model("hf/qwen")])], distribution);
    expect(present).toHaveLength(1);
    expect(present[0].sync).toBeUndefined();
  });
});

describe("publication status and lifecycle actions", () => {
  const view = { mode: "gateway", node: { uuid: "master" } } as ClusterView;
  const worker = () => {
    const n = node("worker", true, [{ ...model("qwen"), source: "opencsg" }]);
    Object.assign(n.status!, { state: "active", accept_work: true, licensed: true, managed_by: "master", model_management: true });
    return n;
  };
  const pool = (source = "node:worker", id = "qwen"): ProviderPool => ({ id: "service", name: "service", model: "public-qwen", enabled: true, policy: "fixed", members: [{ id: "binding", source, model: id }] });
  const task = (phase = "ready", desired: ClusterDeployment["desired"] = "running") => ({ id: "one", node_uuid: "worker", controller_uuid: "master", repository: "org/shared", artifact_source: "opencsg", desired, kind: "chat", status: { phase } }) as ClusterDeployment;

  it("tests published aliases with their advertised APIs, excluding disabled and nonselected bindings", () => {
    const row = modelInstanceRows([worker()], [])[0];
    const decision = { ...pool(), id: "decision", model: "public-decision", inference_protocols: ["decisions"] };
    const embedding = { ...pool("cluster"), id: "embedding", model: "public-embedding", category: "embedding" };
    const disabled = { ...pool(), enabled: false };
    const missing = { ...pool(), source_states: { binding: "missing" } };
    const noAPI = { ...pool(), inference_protocols: [] };
    const fixedElsewhere = { ...pool(), members: [{ id: "other", source: "node:other", model: "qwen" }, ...pool().members] };
    const publication = modelPublication(row, view, [disabled, missing, noAPI, fixedElsewhere, decision, embedding]);
    expect(modelTestTargets(row, view, publication)).toEqual([decision, embedding]);
    expect(modelTestTargets(row, view, publication).map(service => service.model)).toEqual(["public-decision", "public-embedding"]);

  });

  it("tests a running unpublished deployment on its exact node and runtime ID", () => {
    const d = { ...task(), kind: "embedding" as const };
    const row = modelInstanceRows([worker()], [], [d]).find(row => row.deployment)!;
    const unpublished = modelPublication(row, view, []);
    expect(modelTestTargets(row, view, unpublished)).toMatchObject([
      { nodeUUID: "worker", model: "deployment/one", category: "embedding" },
    ]);
    row.deployment = { ...d, status: { ...d.status, phase: "idle" } };
    expect(modelTestTargets(row, view, unpublished)).toEqual([]);
    row.deployment = undefined;
    expect(canPublishModelInstance(row, view)).toBe(false);
    expect(modelTestTargets(row, view, unpublished)[0].model).toBe("deployment/one");
    row.node.online = false;
    expect(modelTestTargets(row, view, unpublished)).toEqual([]);
  });

  it("disables testing for stale, offline, paused and unfinished models while allowing on-demand wakeup", () => {
    const row = modelInstanceRows([worker()], [])[0];
    const publication = modelPublication(row, view, [pool()]);
    expect(modelTestTargets(row, view, publication, true)).toEqual([]);
    row.node.online = false;
    expect(modelTestTargets(row, view, publication)).toEqual([]);
    row.node.online = true;
    row.node.status!.state = "maintenance";
    expect(modelTestTargets(row, view, publication)).toEqual([]);
    row.node.status!.state = "active";
    row.model.loaded = false;
    expect(modelTestTargets(row, view, publication)).toHaveLength(1);
    row.model.loading = true;
    expect(modelTestTargets(row, view, publication)).toEqual([]);
    row.model.loading = false;
    row.deployment = task("downloading");
    expect(modelTestTargets(row, view, publication)).toEqual([]);
    row.deployment = task("failed");
    expect(modelTestTargets(row, view, publication)).toEqual([]);
  });

  it("matches publications by exact model ID and node, excluding unrelated sources and repository aliases", () => {
    const row = modelInstanceRows([worker()], [])[0];
    expect(modelPublication(row, view, null).state).toBe("unknown");
    for (const unrelated of [pool("node:other"), pool("local"), pool("provider:cloud"), pool("node:worker", "org/shared")]) {
      expect(modelPublication(row, view, [unrelated]).state).toBe("unpublished");
    }
    expect(modelPublication(row, view, [pool()]).state).toBe("published");
    expect(modelPublication(row, view, [pool("cluster")]).state).toBe("published");
    expect(modelPublication(row, { ...view, node: { uuid: "worker" } } as ClusterView, [pool("local")]).state).toBe("published");
  });
  it("retains disabled bindings and respects fixed-policy selection without claiming they are enabled", () => {
    const row = modelInstanceRows([worker()], [])[0];
    const p = pool();
    p.enabled = false;
    expect(modelPublication(row, view, [p]).state).toBe("disabled");
    p.enabled = true; p.members[0].disabled = true;
    expect(modelPublication(row, view, [p]).state).toBe("disabled");
    p.members[0].disabled = false; p.source_states = { binding: "missing" };
    expect(modelPublication(row, view, [p]).state).toBe("disabled");
    delete p.source_states;
    p.members.unshift({ id: "other", source: "node:other", model: "qwen" });
    expect(modelPublication(row, view, [p]).state).toBe("disabled");
    p.policy = "failover";
    expect(modelPublication(row, view, [p]).state).toBe("published");
    expect(modelPublication(row, view, [p]).services).toHaveLength(1);
  });
  it("allows cancellation during preparation and restart after stopping, while blocking transitions and stale controls", () => {
    const n = worker();
    const d = task("downloading");
    const row = modelInstanceRows([n], [], [d]).find(row => row.deployment)!;
    expect(modelActionAvailability(row, view)).toMatchObject({ start: false, unload: true, delete: true });
    d.desired = "stopped";
    expect(modelActionAvailability(row, view)).toMatchObject({ start: false, unload: false, delete: false });
    d.status.phase = "stopped";
    expect(modelActionAvailability(row, view)).toMatchObject({ start: true, unload: false, delete: true });
    n.status!.state = "maintenance";
    expect(modelActionAvailability(row, view)).toMatchObject({ start: false, delete: true });
    n.status!.state = "active";
    expect(modelActionAvailability(row, view, true)).toMatchObject({ start: false, unload: false, delete: false });
    n.online = false;
    expect(modelActionAvailability(row, view)).toMatchObject({ start: false, unload: false, delete: false });
  });
  it("blocks foreign controllers and unmanaged busy files, protects shared artifacts, and permits failed deletion retry", () => {
    const n = worker();
    const ordinary = modelInstanceRows([n], [])[0];
    expect(modelActionAvailability(ordinary, view)).toMatchObject({ start: false, unload: true, delete: true });
    ordinary.model.active = 1;
    expect(modelActionAvailability(ordinary, view)).toMatchObject({ unload: false, delete: false });
    ordinary.model.active = 0;
    const d = task();
    const rows = modelInstanceRows([n], [], [d]);
    expect(modelActionAvailability(ordinary, view, false, rows).delete).toBe(false);
    const managed = rows.find(row => row.deployment)!;
    d.controller_uuid = "other";
    expect(modelActionAvailability(managed, view)).toMatchObject({ start: false, unload: false, delete: false });
    d.controller_uuid = "master"; d.desired = "deleted"; d.status.phase = "failed";
    expect(modelActionAvailability(managed, view)).toMatchObject({ start: false, unload: false, delete: true });
    expect(modelActionAvailability(managed, { ...view, mode: "worker" })).toMatchObject({ start: false, unload: false, delete: false });
  });
});

it("keeps node maintenance separate from actual runtime state", () => {
 const n = node("worker", true, [model("qwen")]); n.status!.state = "maintenance";
 const row = modelInstanceRows([n], [])[0];
 expect(modelRowState(row)).toBe("cluster.instanceRunning");
 expect(modelNodeState(row)).toBe("cluster.nodeMaintenance");
 n.status!.state = "drain"; n.status!.accept_work = false;
 expect(modelNodeState(row)).toBe("cluster.nodeDraining");
});

it("retains stopped deployments and removes acknowledged deleted instances", () => {
 const n=node("worker",true,[]);
 const d={id:"one",node_uuid:"worker",controller_uuid:"master",desired:"stopped",repository:"org/model",status:{phase:"stopped"}} as import("./api/client").ClusterDeployment;
 const rows=modelInstanceRows([n],[],[d]);
 expect(rows).toHaveLength(1); expect(modelRowState(rows[0])).toBe("deployments.phase.stopped");
 expect(rows[0].synthetic).toBe(true);
 d.desired="deleted";d.status.phase="pending";
 expect(modelRowState(modelInstanceRows([n],[],[d])[0])).toBe("deployments.phase.deleting");
 d.status.phase="deleted";
 expect(modelInstanceRows([n],[],[d])).toHaveLength(0);
});


describe("unified cluster model management", () => {
  const task = (id: string, phase = "ready") => ({ id, node_uuid: "worker", controller_uuid: "master", repository: "org/model", kind: "chat", name: `runtime-${id}`, desired: "running", status: { phase } }) as import("./api/client").ClusterDeployment;
  it("joins receipts to the exact reported runtime once while retaining distinct deployments", () => {
    const current = task("one");
    const owned = { ...current, desired: "stopped" } as import("./api/client").ClusterDeployment;
    const merged = mergeClusterDeployments([owned, task("two")], [current, task("external")]);
    expect(merged).toHaveLength(3);
    expect(merged.find(d => d.id === "one")?.desired).toBe("stopped");
    const rows = modelInstanceRows([node("worker", true, [model("deployment/one")])], [], merged);
    expect(rows).toHaveLength(3);
    expect(rows.filter(row => row.model.id === "deployment/one")).toHaveLength(1);
    expect(rows.find(row => row.model.id === "deployment/one")?.deployment?.desired).toBe("stopped");
  });
  it("keeps downloads, failures, stopped tasks and removed target machines visible", () => {
    const pending = task("download", "downloading");
    const failed = task("failed", "failed");
    const stopped = { ...task("stopped", "stopped"), desired: "stopped" } as import("./api/client").ClusterDeployment;
    const missing = { ...task("removed"), node_uuid: "removed-machine" };
    const rows = modelInstanceRows([node("worker", true, [])], [], [pending, failed, stopped, missing]);
    expect(rows).toHaveLength(4);
    expect(rows.map(modelRowState)).toEqual(["deployments.phase.downloading", "deployments.phase.failed", "deployments.phase.stopped", "cluster.healthOffline"]);
    expect(rows.find(row => row.deployment?.id === "removed")?.node.uuid).toBe("removed-machine");
  });
  it("combines search, machine and lifecycle filters without merging independent copies", () => {
    const rows = modelInstanceRows([node("worker", true, [model("local-model")]), node("other", true, [model("local-model", false)])], [], [task("download", "downloading"), task("bad", "failed")]);
    expect(filterModelInstances(rows, " RUNTIME-DOWNLOAD ", "worker", "preparing").map(row => row.deployment?.id)).toEqual(["download"]);
    expect(filterModelInstances(rows, "", "", "running")).toHaveLength(1);
    expect(filterModelInstances(rows, "local-model", "other", "inactive")).toHaveLength(1);
    expect(filterModelInstances(rows, "", "", "failed").map(row => row.deployment?.id)).toEqual(["bad"]);
    expect(filterModelInstances(rows, "not-present", "", "all")).toHaveLength(0);
  });
  it("only publishes valid source bindings and keeps managed runtime controls scoped", () => {
    const worker = node("worker", true, [model("local-model", false), model("deployment/one")]);
    Object.assign(worker.status!, { state: "active", accept_work: true });
    const view = { mode: "gateway", node: { uuid: "master" } } as import("./api/client").ClusterView;
    const rows = modelInstanceRows([worker], [], [task("one")]);
    const local = rows.find(row => row.model.id === "local-model")!;
    const managed = rows.find(row => row.deployment)!;
    expect(canPublishModelInstance(local, view)).toBe(true);
    expect(canPublishModelInstance(managed, view)).toBe(true);
    expect(canPublishModelInstance(managed, { ...view, mode: "worker" })).toBe(false);
    const orphan = modelInstanceRows([worker], [])[0];
    expect(isManagedModelInstance(orphan)).toBe(true);
    expect(canPublishModelInstance(orphan, view)).toBe(false);
    managed.deployment!.desired = "stopped";
    expect(canPublishModelInstance(managed, view)).toBe(false);
    worker.status!.state = "maintenance";
    expect(canPublishModelInstance(local, view)).toBe(false);
  });
});


describe("stopped deployment configuration", () => {
  const view = {mode:"gateway",node:{uuid:"master"}} as ClusterView;
  function editable() {
    const worker=node("worker",true,[]); worker.status!.deployment_configurable=true;
    const d={id:"edit",kind:"chat",node_uuid:"worker",controller_uuid:"master",desired:"stopped",status:{phase:"stopped"}} as ClusterDeployment;
    return modelInstanceRows([worker],[],[d])[0];
  }
  it("allows edits only on stopped owned deployments with capable online workers",()=>{
    const row=editable();expect(modelActionAvailability(row,view).edit).toBe(true);
    expect(modelActionAvailability(row,view,true).edit).toBe(false);
    expect(modelActionAvailability(row,{...view,mode:"worker"}).edit).toBe(false);
    row.node.online=false;expect(modelActionAvailability(row,view).edit).toBe(false);row.node.online=true;
    row.deployment!.controller_uuid="another";expect(modelActionAvailability(row,view).edit).toBe(false);
  });
  it("keeps context-based configuration restricted to chat and embedding runtimes",()=>{
    for (const kind of ["chat", "embedding"]) {
      const row=editable(); row.deployment!.kind=kind as ClusterDeployment["kind"];
      expect(modelActionAvailability(row,view).edit).toBe(true);
    }
    for (const kind of ["image", "asr", "tts", "unknown"]) {
      const row=editable(); row.deployment!.kind=kind as ClusterDeployment["kind"];
      const actions=modelActionAvailability(row,view);
      expect(actions.edit).toBe(false);
      expect(actions.editReason).toBe("deployments.unsupportedKind");
    }
  });
  it("requires a completed stop even after failure and explains old node versions",()=>{
    const row=editable();
    for(const phase of ["ready","pending","stopping","failed","unreachable","deleted"]){row.deployment!.status.phase=phase;expect(modelActionAvailability(row,view).edit).toBe(false);}
    row.deployment!.status.phase="stopped";row.deployment!.desired="running";expect(modelActionAvailability(row,view).edit).toBe(false);
    row.deployment!.desired="stopped";delete row.node.status!.deployment_configurable;
    expect(modelActionAvailability(row,view).edit).toBe(false);
    expect(modelActionAvailability(row,view).editReason).toBe("deployments.configUpgradeNode");
  });
});

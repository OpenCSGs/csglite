import { describe, expect, it } from "vitest";
import type { ClusterDeployment, ClusterNodeView, ClusterView, MarketplaceModel } from "./api/client";
import { localDeploymentAssignments, deploymentProgress, deploymentModelKind, deploymentName, deploymentRevision, deploymentTargets, workerDeploymentAssignments, recommendedDeploymentBackend } from "./deploymentModels";

describe("deployment model task detection", () => {
  const base = { path: "org/chat-image-ASR", tags: [] } as unknown as MarketplaceModel;
  it.each([
    ["text-generation", "chat"], ["image-text-to-text", "chat"], ["sentence-similarity", "embedding"],
    ["text-to-image", "image"], ["automatic-speech-recognition", "asr"], ["text-to-speech", "tts"],
    ["text-classification", "unknown"], ["", "unknown"],
  ])("uses the OpenCSG task %s without guessing from the name", (task, kind) => {
    expect(deploymentModelKind({ ...base, tags: [{ category: "task", name: task, show_name: "翻译名称" }] })).toBe(kind);
  });
  it("uses provider metadata across registries", () => {
    expect(deploymentModelKind({ ...base, provider: { huggingface: { pipeline_tag: "automatic-speech-recognition" } } })).toBe("asr");
    expect(deploymentModelKind({ ...base, provider: { modelscope: { tasks: ["text-to-image"] } } })).toBe("image");
  });
  it("uses inspected runtime metadata when task tags are missing", () => {
    expect(deploymentModelKind(base, { supported: true, mode: "embedding", runtime: "llama" })).toBe("embedding");
    expect(deploymentModelKind(base, { supported: true, mode: "direct", runtime: "llama" })).toBe("chat");
    expect(deploymentModelKind(base, { supported: false, mode: "none" })).toBe("unknown");
    expect(deploymentModelKind({ ...base, tags: [{ category: "task", name: "automatic-speech-recognition", show_name: "ASR" }] }, { supported: true, mode: "direct" })).toBe("asr");
  });
  it("uses the latest default branch instead of a resolved or historical commit", () => {
    expect(deploymentRevision({ ...base, default_branch: "release", revision: "abc123" }, "opencsg")).toBe("release");
    expect(deploymentRevision({ ...base, revision: "abc123" }, "huggingface")).toBe("main");
    expect(deploymentRevision(undefined, "modelscope")).toBe("master");
  });
  it("generates distinct valid names for repeated deployments of the same repository", () => {
    expect(deploymentName("org/Qwen", ["qwen", "Qwen-2"])).toBe("Qwen-3");
    const long = "q".repeat(100);
    expect(deploymentName(`org/${long}`, [long])).toBe("q".repeat(98) + "-2");
  });
});

describe("deployment target eligibility", () => {
  const llama = { name: "llama" as const, known: true, installed: true, available: true, running: 0 };
  const sglang = { name: "sglang" as const, known: true, installed: true, available: true, running: 0 };
  it("uses target format/task and never sends embedding to SGLang", () => {
    expect(recommendedDeploymentBackend("embedding", "safetensors", [llama, sglang])).toBe("llama");
    expect(recommendedDeploymentBackend("embedding", "safetensors", [sglang])).toBe("unknown");
    expect(recommendedDeploymentBackend("chat", "gguf", [llama, sglang])).toBe("llama");
    expect(recommendedDeploymentBackend("chat", "safetensors", [llama, sglang])).toBe("sglang");
  });
  it("keeps unknown honest and falls back only when SGLang is actually absent", () => {
    expect(recommendedDeploymentBackend("chat", "safetensors", [])).toBe("unknown");
    expect(recommendedDeploymentBackend("chat", "safetensors", [llama, { ...sglang, known: false, available: false }])).toBe("unknown");
    expect(recommendedDeploymentBackend("chat", "safetensors", [llama, { ...sglang, available: false }])).toBe("none");
    expect(recommendedDeploymentBackend("chat", "safetensors", [llama, { ...sglang, installed: false, available: false }])).toBe("llama");
  });
  it("excludes offline, paused, unlicensed and foreign-controller nodes", () => {
    const node = { uuid: "worker", online: true, status: { deployment_capable: true, managed_by: "master", licensed: true, accept_work: true, state: "active" } } as ClusterNodeView;
    const view = { node: { uuid: "master" }, members: [
      node, { ...node, uuid: "offline", online: false },
      ...[{ state: "maintenance" }, { state: "drain" }, { accept_work: false }, { licensed: false }, { managed_by: "other" }, { deployment_capable: false }].map((status, i) => ({ ...node, uuid: `excluded-${i}`, status: { ...node.status, ...status } })),
      { ...node, uuid: "master", status: { ...node.status, managed_by: "" } },
    ] } as ClusterView;
    expect(deploymentTargets(view).map(n => n.uuid)).toEqual(["worker", "master"]);
  });
});

describe("worker execution view", () => {
  it("shows only this node's assignments from its current Master", () => {
    const task = { id: "current", node_uuid: "worker", controller_uuid: "master", status: {phase:"ready"} } as ClusterDeployment;
    const assignments = [task, { ...task, id: "remote", node_uuid: "another-node" }, { ...task, id: "old", controller_uuid: "previous-master" }];
    expect(workerDeploymentAssignments(assignments, "worker", "master").map(d => d.id)).toEqual(["current"]);
    expect(workerDeploymentAssignments(assignments, "worker")).toEqual([]);
  });
});

describe("node library deployment receipts", () => {
  const task = { id:"local", node_uuid:"worker", controller_uuid:"master", status:{phase:"downloading",completed_bytes:250,total_bytes:1000} } as ClusterDeployment;
  const view = { mode:"worker",node:{uuid:"worker"},cluster:{gateway_uuid:"master"},settings:{} } as ClusterView;
  it("includes only this node's current assignments, even when the same repository appears elsewhere", () => {
    const tasks=[task,{...task,id:"remote",node_uuid:"master"},{...task,id:"old",controller_uuid:"old-master"},{...task,id:"self",controller_uuid:"worker"},{...task,id:"deleted",status:{phase:"deleted"}} as ClusterDeployment];
    expect(localDeploymentAssignments(tasks,view).map(d=>d.id)).toEqual(["local"]);
    expect(localDeploymentAssignments(tasks,{...view,cluster:null})).toEqual([]);
    expect(localDeploymentAssignments(tasks,{...view,cluster:{...view.cluster!,gateway_uuid:undefined}})).toEqual([]);
  });
  it("keeps ready, failed and stopped models visible after download", () => {
    for(const phase of ["ready","loading","failed","stopped"]) {
      expect(localDeploymentAssignments([{...task,status:{phase}} as ClusterDeployment],view)).toHaveLength(1);
    }
  });
  it("includes local Master execution without showing its remote deployments", () => {
    const master={...view,mode:"gateway",node:{uuid:"master"}} as ClusterView;
    expect(localDeploymentAssignments([task,{...task,id:"self",node_uuid:"master"}],master).map(d=>d.id)).toEqual(["self"]);
  });
  it("preserves unknown totals and never mistakes an absent total for completion", () => {
    expect(deploymentProgress(task.status)).toEqual({completed:250,total:1000,percent:25});
    expect(deploymentProgress({...task.status,total_bytes:0})).toEqual({completed:250,total:null,percent:null});
    expect(deploymentProgress({...task.status,completed_bytes:-1})).toEqual({completed:0,total:1000,percent:0});
    expect(deploymentProgress({...task.status,completed_bytes:1200}).percent).toBe(100);
    expect(deploymentProgress({...task.status,completed_bytes:999}).percent).toBe(99.9);
  });
});

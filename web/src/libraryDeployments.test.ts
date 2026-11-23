import { describe, expect, it } from "vitest";
import type { ClusterDeployment, RunningModel } from "./api/client";
import { deploymentLibraryRows, mergeDeploymentLibraryRows } from "./libraryDeployments";
const task = { id:"one",name:"Demo",repository:"org/demo",artifact_source:"modelscope",revision:"main",kind:"chat",created_at:"2026-11-15T00:00:00Z",status:{phase:"downloading",completed_bytes:25,total_bytes:100,updated_at:"2026-11-15T00:01:00Z"} } as ClusterDeployment;
describe("deployment rows in the existing model library", () => {
  it("uses existing download progress and retains the same row after completion", () => {
    const before=deploymentLibraryRows([task],[],"","all")[0];
    const after=deploymentLibraryRows([{...task,status:{...task.status,phase:"ready",size:100}}],[],"","all")[0];
    expect(before.task?.percent).toBe(25);expect(before.task?.status).toBe("downloading");
    expect(after.task?.status).toBe("success");expect(before.model.name).toBe(after.model.name);
    expect(after.model.repository).toBe("org/demo");expect(after.model.size).toBe(100);
  });
  it("uses search and actual runtime format filtering", () => {
    const running=[{name:"deployment/one",model:"deployment/one",format:"gguf",size:100}] as RunningModel[];
    expect(deploymentLibraryRows([task],running,"ORG/DEMO","gguf")).toHaveLength(1);
    expect(deploymentLibraryRows([task],running,"absent","all")).toHaveLength(0);
    expect(deploymentLibraryRows([task],running,"","safetensors")).toHaveLength(0);
  });
  it("keeps same-repository instances distinct and removes acknowledged deletes", () => {
    const rows=deploymentLibraryRows([task,{...task,id:"two"},{...task,id:"deleted",status:{...task.status,phase:"deleted"}}],[],"","all");
    expect(rows.map(r=>r.model.name)).toEqual(["modelscope/org/demo","modelscope/org/demo"]);
    expect(mergeDeploymentLibraryRows([],rows)).toHaveLength(1);
  });
  it("never reports a download as finished solely from its byte counter", () => {
    const row=deploymentLibraryRows([{...task,status:{...task.status,completed_bytes:100}}],[],"","all")[0];
    expect(row.task?.status).toBe("downloading");expect(row.task?.percent).toBeLessThan(100);
  });
  it("carries the worker download speed only while downloading", () => {
    const downloading=deploymentLibraryRows([{...task,status:{...task.status,speed_bytes_per_sec:2*1024*1024}}],[],"","all")[0];
    const ready=deploymentLibraryRows([{...task,status:{...task.status,phase:"ready",size:100,speed_bytes_per_sec:2*1024*1024}}],[],"","all")[0];
    expect(downloading.task?.speedBytesPerSec).toBe(2*1024*1024);
    expect(ready.task?.speedBytesPerSec).toBe(0);
  });
});

it("merges managed receipts into the actual library artifact without duplicating files", () => {
 const local={model:{id:"modelscope/org/demo",name:"demo",model:"modelscope/org/demo",repository:"org/demo",artifact_source:"modelscope",format:"gguf",size:100,modified_at:task.created_at},downloadOnly:false} as import("./libraryDeployments").LibraryModelRow;
 const managed=deploymentLibraryRows([task,{...task,id:"two"}],[],"","gguf",[local.model]);
 const rows=mergeDeploymentLibraryRows([local],managed);
 expect(rows).toHaveLength(1);expect(rows[0].model).toBe(local.model);expect(rows[0].task?.percent).toBe(25);expect(rows[0].deployment?.id).toBe("one");
});

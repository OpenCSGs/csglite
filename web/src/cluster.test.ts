import { describe, expect, it } from "vitest";

import {
  clusterReasonText,
  canManageNodeState,
  workerNodeState,
  clusterNodeRole,
  fmtGB,
  fmtGBPair,
  fmtSeconds,
  formatAdmissionCode,
  healthKey,
  healthTone,
  isValidAdmissionCode,
  nodeLimitLabel,
  normalizeAdmissionCode,
  percentOf,
  secondsUntil,
  stateKey,
} from "./cluster";

describe("cluster formatting helpers", () => {
  it("formats bytes as gigabytes with one decimal", () => {
    expect(fmtGB(24 * 1024 ** 3)).toBe("24.0");
    expect(fmtGB(1.25 * 1024 ** 3)).toBe("1.3");
    expect(fmtGB(0)).toBe("0.0");
    expect(fmtGB(Number.NaN)).toBe("0.0");
    expect(fmtGBPair(1024 ** 3, 4 * 1024 ** 3)).toBe("1.0 / 4.0 GB");
  });

  it("clamps percentages and tolerates unknown totals", () => {
    expect(percentOf(1, 4)).toBe(25);
    expect(percentOf(5, 4)).toBe(100);
    expect(percentOf(1, 0)).toBe(0);
    expect(percentOf(-1, 4)).toBe(0);
  });

  it("formats estimated seconds compactly", () => {
    expect(fmtSeconds(0.4)).toBe("<1s");
    expect(fmtSeconds(12.2)).toBe("12s");
    expect(fmtSeconds(65)).toBe("1m 05s");
    expect(fmtSeconds(-1)).toBe("—");
  });

  it("counts down to an ISO timestamp", () => {
    const now = Date.parse("2026-09-18T10:00:00Z");
    expect(secondsUntil("2026-09-18T10:00:30Z", now)).toBe(30);
    expect(secondsUntil("2026-09-18T09:59:00Z", now)).toBe(0);
    expect(secondsUntil("not a date", now)).toBeNull();
    expect(secondsUntil(undefined, now)).toBeNull();
  });
});

describe("node health mapping", () => {
  it("colours online nodes by gossip health", () => {
    expect(healthTone("healthy", true)).toBe("ok");
    expect(healthTone("suspect", true)).toBe("warn");
    expect(healthTone("probing", true)).toBe("warn");
    expect(healthTone("down", true)).toBe("error");
    expect(healthTone("unknown", true)).toBe("neutral");
    expect(healthTone(undefined, true)).toBe("neutral");
  });

  it("shows an offline node as red whatever its health says", () => {
    expect(healthTone("healthy", false)).toBe("error");
    expect(healthKey("healthy", false)).toBe("cluster.healthOffline");
    expect(healthKey("suspect", true)).toBe("cluster.healthSuspect");
  });

  it("maps states to i18n keys", () => {
    expect(stateKey("drain")).toBe("cluster.stateDrain");
    expect(stateKey("maintenance")).toBe("cluster.stateMaintenance");
    expect(stateKey("active")).toBe("cluster.stateActive");
    expect(stateKey(undefined)).toBe("");
  });
});

describe("node limit label", () => {
  it("treats a zero limit as unlimited", () => {
    expect(nodeLimitLabel(5, 0)).toEqual({ key: "cluster.nodesUnlimited", args: [5], atLimit: false });
  });

  it("reports the cap with the upgrade hint once it is reached", () => {
    expect(nodeLimitLabel(1, 2)).toEqual({ key: "cluster.nodesOfLimit", args: [1, 2], atLimit: false });
    expect(nodeLimitLabel(2, 2)).toEqual({ key: "cluster.nodesAtLimit", args: [2, 2], atLimit: true });
    expect(nodeLimitLabel(3, 2).atLimit).toBe(true);
  });
});

describe("admission code", () => {
  it("accepts eight digits with cosmetic separators", () => {
    expect(normalizeAdmissionCode("1234 5678")).toBe("12345678");
    expect(isValidAdmissionCode("1234-5678")).toBe(true);
    expect(isValidAdmissionCode("12345678")).toBe(true);
    expect(isValidAdmissionCode("1234567")).toBe(false);
    expect(isValidAdmissionCode("abcd1234")).toBe(false);
    expect(formatAdmissionCode("12345678")).toBe("1234 5678");
    expect(formatAdmissionCode("1234-5678")).toBe("1234 5678");
    expect(formatAdmissionCode("abc")).toBe("abc");
  });
});

describe("cluster node roles", () => {
  const publicCluster = { uuid: "cluster", name: "Public", created_at: "", topology: "public" as const, gateway_uuid: "gateway" };
  it("identifies the gateway independently of which node serves the UI", () => {
    expect(clusterNodeRole("gateway", publicCluster)).toBe("master");
    expect(clusterNodeRole("worker", publicCluster)).toBe("slave");
  });
  it("does not invent master/slave roles for peer or standalone nodes", () => {
    expect(clusterNodeRole("node", { uuid: "lan", name: "LAN", created_at: "" })).toBe("peer");
    expect(clusterNodeRole("node", null)).toBe("standalone");
    expect(clusterNodeRole("node", { ...publicCluster, gateway_uuid: undefined })).toBe("unknown");
  });
});

describe("workerNodeState", () => {
  it("shows a legacy stopped worker as maintenance and preserves draining", () => {
    expect(workerNodeState({ state: "active", accept_work: false })).toBe("maintenance");
    expect(workerNodeState({ state: "active", accept_work: true })).toBe("active");
    expect(workerNodeState({ state: "drain", accept_work: false })).toBe("drain");
    expect(workerNodeState({ state: "maintenance", accept_work: true })).toBe("maintenance");
  });
});

describe("remote node state authority", () => {
 const view = { mode: "gateway", node: {uuid: "master"} } as import("./api/client").ClusterView;
 const node = { local: false, online: true, status: { managed_by: "master", node_state_management: true } } as import("./api/client").ClusterNodeView;
 it("offers controls only for nodes accepting this Master", () => {
  expect(canManageNodeState(view, node)).toBe(true);
  expect(canManageNodeState({...view, mode: "worker"}, node)).toBe(false);
  expect(canManageNodeState(view, {...node, status: {...node.status!, managed_by: "other"}})).toBe(false);
  expect(canManageNodeState(view, {...node, status: {...node.status!, node_state_management: undefined}})).toBe(false);
  expect(canManageNodeState(view, {...node, status: undefined})).toBe(false);
 });
});

describe("localized scheduling reasons", () => {
 const translate = (key: string, ...args: (string | number)[]) => ({
  "cluster.reason.not_accepting": "节点已暂停接单",
  "cluster.reason.node_state": `节点处于${args[0]}`,
  "cluster.stateDrain": "排空中",
  "cluster.reason.decode_measured": `实测速度 ${args[0]}`,
 }[key] ?? key);
 it("uses reason codes and parameters while supporting legacy responses", () => {
  expect(clusterReasonText({code:"not_accepting"}, "node is not accepting work", translate)).toBe("节点已暂停接单");
  expect(clusterReasonText({code:"node_state",args:["drain"]}, "node state is drain", translate)).toBe("节点处于排空中");
  expect(clusterReasonText({code:"decode_measured",args:[11.4]}, "decode 11.4 tok/s", translate)).toBe("实测速度 11.4");
  expect(clusterReasonText(undefined, "legacy", translate)).toBe("legacy");
  expect(clusterReasonText({code:"future"}, "new reason", translate)).toBe("new reason");
 });
});

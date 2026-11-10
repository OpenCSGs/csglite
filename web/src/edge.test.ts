import { describe, expect, it } from "vitest";
import { canWithdraw, centsToYuan, currentMonth, isEdgeGatewayUnreachableMessage, isValidEdgeModelName, suggestEdgeModelName, yuanToCents } from "./edge";

describe("edge serving helpers", () => {
  it("converts owner prices between yuan and cents", () => {
    expect(yuanToCents("1.5")).toBe(150);
    expect(yuanToCents("0")).toBe(0);
    expect(yuanToCents("12.34")).toBe(1234);
    expect(yuanToCents("1.234")).toBeNull();
    expect(yuanToCents("-1")).toBeNull();
    expect(yuanToCents("")).toBeNull();
    expect(centsToYuan(480)).toBe("4.80");
  });

  it("validates and suggests public model names like the gateway", () => {
    expect(isValidEdgeModelName("qwen3-8b")).toBe(true);
    expect(isValidEdgeModelName("bad name")).toBe(false);
    expect(isValidEdgeModelName("-lead")).toBe(false);
    expect(suggestEdgeModelName("OpenCSG/Qwen3-8B-GGUF")).toBe("Qwen3-8B-GGUF");
    expect(suggestEdgeModelName("hf/org/model:Q4_K_M")).toBe("model-Q4_K_M");
  });

  it("treats the gateway's own 404 page as unreachable", () => {
    expect(isEdgeGatewayUnreachableMessage("404 page not found")).toBe(true);
    expect(isEdgeGatewayUnreachableMessage("cannot connect to the CSGHub AI gateway")).toBe(true);
    expect(isEdgeGatewayUnreachableMessage("Cannot connect to the CSGHub AI gateway.")).toBe(true);
    expect(isEdgeGatewayUnreachableMessage("register node: cannot connect to the CSGHub AI gateway")).toBe(true);
    expect(isEdgeGatewayUnreachableMessage("不能连接 CSGHub AI 网关")).toBe(true);
    expect(isEdgeGatewayUnreachableMessage("this model of this node already has a live application")).toBe(false);
  });

  it("formats the current month and withdraw rules", () => {
    expect(currentMonth(new Date(2026, 9, 29))).toBe("2026-10");
    expect(canWithdraw("approved")).toBe(true);
    expect(canWithdraw("rejected")).toBe(false);
  });
});

import { formatYuan, isLiveApplication, mergeEarningsByModel, sinceLabel } from "./edge";

describe("edge page formatting", () => {
  it("never shows a positive sub-fen amount as zero", () => {
    expect(formatYuan(0)).toBe("¥0.00");
    expect(formatYuan(0.006)).toBe("<¥0.01");
    expect(formatYuan(480)).toBe("¥4.80");
  });

  it("separates live applications from history", () => {
    expect(isLiveApplication("suspended")).toBe(true);
    expect(isLiveApplication("withdrawn")).toBe(false);
  });

  it("merges earning rows of one public model", () => {
    const rows = [
      { public_model_id: "a/m", requests: 1, prompt_tokens: 10, completion_tokens: 5, amount: 1, owner_earning: 0.8 },
      { public_model_id: "a/m", requests: 2, prompt_tokens: 20, completion_tokens: 5, amount: 2, owner_earning: 1.6 },
      { public_model_id: "a/n", requests: 9, prompt_tokens: 1, completion_tokens: 1, amount: 0, owner_earning: 0 },
    ];
    const merged = mergeEarningsByModel(rows);
    expect(merged).toHaveLength(2);
    expect(merged[0]).toMatchObject({ public_model_id: "a/m", requests: 3, prompt_tokens: 30 });
    expect(merged[0].owner_earning).toBeCloseTo(2.4);
  });

  it("labels connection age compactly", () => {
    const now = Date.parse("2026-10-29T12:00:00Z");
    expect(sinceLabel("2026-10-29T11:59:20Z", now)).toBe("40s");
    expect(sinceLabel("2026-10-29T09:48:00Z", now)).toBe("2h 12m");
    expect(sinceLabel("2026-10-27T10:00:00Z", now)).toBe("2d 2h");
  });
});

import { hasStats, percent, ttftLabel } from "./edge";

describe("edge serving stats labels", () => {
  const base = { window_days: 7, requests: 3, failed: 0, success_rate: 1, avg_ttft_ms: 600, p50_ttft_ms: 500, p90_ttft_ms: 2000, avg_latency_ms: 900, tokens_per_second: 42.5, online_seconds: 60, uptime_ratio: 0.5 };
  it("labels the TTFT bucket", () => {
    expect(ttftLabel(base)).toBe("≤500 ms");
    expect(ttftLabel({ ...base, p50_ttft_ms: 2000 })).toBe("≤2 s");
    expect(ttftLabel({ ...base, p50_ttft_ms: -1 })).toBe(">5 s");
    expect(ttftLabel({ ...base, p50_ttft_ms: 0 })).toBe("");
    expect(ttftLabel(undefined)).toBe("");
  });
  it("formats ratios and detects empty stats", () => {
    expect(percent(0.987)).toBe("99%");
    expect(hasStats(base)).toBe(true);
    expect(hasStats({ ...base, requests: 0, online_seconds: 0 })).toBe(false);
  });
});

import { durationLabel, onlineLabel } from "./edge";

describe("edge online time label", () => {
  const base = { window_days: 7, requests: 1, failed: 0, success_rate: 1, avg_ttft_ms: 0, p50_ttft_ms: 0, p90_ttft_ms: 0, avg_latency_ms: 0, tokens_per_second: 0, online_seconds: 480, uptime_ratio: 0.0008 };
  it("shows the duration, and the share once it is meaningful", () => {
    expect(durationLabel(480)).toBe("8m");
    expect(onlineLabel(base)).toBe("8m");
    expect(onlineLabel({ ...base, online_seconds: 3 * 86400, uptime_ratio: 3 / 7 })).toBe("3d 0h (43%)");
  });
});

import { servingState } from "./edge";

describe("edge serving state", () => {
  it("shows nothing until approved and stopped while suspended", () => {
    expect(servingState({ status: "pending", node_status: "online", model_state: "loaded" })).toBe("none");
    expect(servingState({ status: "rejected", node_status: "online" })).toBe("none");
    expect(servingState({ status: "suspended", node_status: "online", model_state: "loaded" })).toBe("stopped");
  });

  it("follows the node and model once approved", () => {
    expect(servingState({ status: "approved", node_status: "offline", model_state: "loaded" })).toBe("offline");
    expect(servingState({ status: "approved", node_status: "online", model_state: "offline" })).toBe("offline");
    expect(servingState({ status: "approved", node_status: "online", model_state: "unavailable" })).toBe("unavailable");
    expect(servingState({ status: "approved", node_status: "online", model_state: "sleeping" })).toBe("serving");
    expect(servingState({ status: "approved", node_status: "online" })).toBe("serving");
  });
});

import { formatMonth } from "./edge";

describe("edge earnings months", () => {
  it("names a month in the reader's language", () => {
    expect(formatMonth("2026-10", "zh")).toBe("2026年10月");
    expect(formatMonth("2026-10", "en")).toBe("October 2026");
  });
});

import { statementTone } from "./edge";

describe("edge statement badges", () => {
  it("colours each settlement status", () => {
    expect(statementTone("settled")).toContain("emerald");
    expect(statementTone("unsettled")).toContain("amber");
    expect(statementTone("open")).toContain("sky");
  });
});

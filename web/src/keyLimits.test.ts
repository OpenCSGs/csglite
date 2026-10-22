import { describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

import { budgetUsed, draftBudget, draftLimits, limitsDraft } from "./components/KeyLimits";

describe("key limit drafts", () => {
  it("round-trips limits through the form", () => {
    const limits = { models: ["Qwen/*", "gpt-4o"], rpm: 10, budget: { tokens: 1000, period: "day" as const } };
    expect(draftLimits(limitsDraft(limits))).toEqual(limits);
  });

  it("drops empty and invalid fields", () => {
    const draft = limitsDraft();
    draft.models = " , \n";
    draft.rpm = "-3";
    draft.tpm = "abc";
    expect(draftLimits(draft)).toEqual({});
  });

  it("splits models on commas and new lines", () => {
    const draft = limitsDraft();
    draft.models = "a, b\nc";
    expect(draftLimits(draft).models).toEqual(["a", "b", "c"]);
  });

  it("keeps the currency only with a cost cap", () => {
    expect(draftBudget({ tokens: "", cost: "", currency: "CNY", period: "month" })).toBeNull();
    expect(draftBudget({ tokens: "", cost: "12.5", currency: "usd", period: "" })).toEqual({ cost: 12.5, currency: "USD", period: "" });
  });

  it("reports the largest share used", () => {
    const budget = { tokens: 100, cost: 10, currency: "CNY" };
    expect(budgetUsed(budget, { tokens: 50, cost: 9, exhausted: false })).toBeCloseTo(0.9);
    expect(budgetUsed(budget, { tokens: 500, cost: 0, exhausted: true })).toBe(1);
  });
});

import { describe, expect, it } from "vitest";
import {
  buildDecisionRequest, canSendDecision, decisionUserSummary, formatDecisionAnswer, isDecisionModeActive, parseDecisionLines,
} from "./decision";
import type { DecisionLabels } from "./decision";

const labels: DecisionLabels = {
  kind: { predicate: "Yes/No", choice: "Choice", score: "Score" },
  state: "Context",
  options: "Options",
  levels: "Levels",
  probability: "Probability",
  probabilityTrue: "Probability true",
  choice: "Decision",
  score: "Score",
  confidence: "confidence",
  option: "Option",
  level: "Level",
  yes: "Yes",
  no: "No",
  refused: "Refused",
};

describe("decision helpers", () => {
  it("keeps decision-only models in decision mode", () => {
    expect(isDecisionModeActive({ typed_decisions: true, decision_only: true }, false)).toBe(true);
    expect(isDecisionModeActive({ typed_decisions: true }, false)).toBe(false);
    expect(isDecisionModeActive({ typed_decisions: true }, true)).toBe(true);
    expect(isDecisionModeActive({}, true)).toBe(false);
    expect(isDecisionModeActive(null, true)).toBe(false);
  });

  it("parses one entry per line", () => {
    expect(parseDecisionLines(" warn \n\nwait\r\norder\n")).toEqual(["warn", "wait", "order"]);
  });

  it("requires a question, and two options or levels", () => {
    expect(canSendDecision("predicate", "Refund?", "")).toBe(true);
    expect(canSendDecision("predicate", "  ", "")).toBe(false);
    expect(canSendDecision("choice", "Next?", "only one")).toBe(false);
    expect(canSendDecision("score", "How bad?", "low\nhigh")).toBe(true);
  });

  it("builds OpenAI Decisions requests", () => {
    expect(buildDecisionRequest("m", "local", "choice", "Which department?", "Charged twice", "billing\nother")).toEqual({
      model: "m",
      source: "local",
      input: "Charged twice",
      questions: [{ type: "choice", name: "choice", instructions: "Which department?", choices: [{ value: "billing" }, { value: "other" }] }],
    });
    const score = buildDecisionRequest("m", undefined, "score", "How severe?", "", "Cosmetic\nBlocked");
    expect(score.input).toBe("How severe?");
    expect(score.questions[0].levels).toEqual([{ label: "Cosmetic" }, { label: "Blocked" }]);
    expect(buildDecisionRequest("m", undefined, "predicate", "Refund?", "x", "ignored").questions[0]).toEqual({
      type: "predicate", name: "predicate", instructions: "Refund?",
    });
  });

  it("summarizes the question for the conversation", () => {
    expect(decisionUserSummary("choice", "Next?", "low stock", "warn\nwait", labels))
      .toBe("[Choice] Next?\nContext: low stock\nOptions: warn / wait");
    expect(decisionUserSummary("score", "How bad?", "", "low\nhigh", labels)).toBe("[Score] How bad?\nLevels: low < high");
  });

  it("renders choice answers with the pick in bold", () => {
    const md = formatDecisionAnswer({
      type: "choice",
      name: "choice",
      choice: "a|b",
      probabilities: [{ value: "warn", probability: 0.25 }, { value: "a|b", probability: 0.75 }],
      confidence: 0.5,
    }, labels);
    expect(md).toContain("**Decision: a\\|b** · confidence 0.50");
    expect(md).toContain("| warn | 25.0% | ███ |");
    expect(md).toContain("| **a\\|b** | 75.0% | ████████ |");
  });

  it("renders predicate and score answers", () => {
    const predicate = formatDecisionAnswer({ type: "predicate", name: null, probability: 0.9932 }, labels);
    expect(predicate).toContain("**Probability true: 99.3%**");
    expect(predicate).toContain("| **Yes** | 99.3% |");
    const score = formatDecisionAnswer({
      type: "score",
      name: "s",
      score: 1.1,
      confidence: 0.5,
      probabilities: [
        { value: 0, label: "Cosmetic", probability: 0.1 },
        { value: 1, label: "Workaround", probability: 0.7 },
        { value: 2, label: "Blocked", probability: 0.2 },
      ],
    }, labels);
    expect(score).toContain("**Score: 1.10** · confidence 0.50");
    expect(score).toContain("| **1 · Workaround** | 70.0% |");
    expect(formatDecisionAnswer({ type: "refusal", name: "x" }, labels)).toBe("Refused");
  });
});

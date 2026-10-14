import type { CreateDecisionRequest, DecisionAnswer, DecisionQuestionType } from "../api/client";

export interface DecisionLabels {
  kind: Record<DecisionQuestionType, string>;
  state: string;
  options: string;
  levels: string;
  probability: string;
  probabilityTrue: string;
  choice: string;
  score: string;
  confidence: string;
  option: string;
  level: string;
  yes: string;
  no: string;
  refused: string;
}

/** Splits a textarea into one trimmed, non-empty entry per line. */
export function parseDecisionLines(text: string): string[] {
  return text.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

export function canSendDecision(type: DecisionQuestionType, question: string, linesText: string): boolean {
  if (!question.trim()) return false;
  return type === "predicate" || parseDecisionLines(linesText).length >= 2;
}

/** Builds an OpenAI Decisions request with one question from the composer fields. */
export function buildDecisionRequest(
  model: string,
  source: string | undefined,
  type: DecisionQuestionType,
  question: string,
  input: string,
  linesText: string,
): CreateDecisionRequest {
  const lines = parseDecisionLines(linesText);
  return {
    model,
    source,
    input: input.trim() || question.trim(),
    questions: [{
      type,
      name: type,
      instructions: question.trim(),
      ...(type === "choice" ? { choices: lines.map((value) => ({ value })) } : {}),
      ...(type === "score" ? { levels: lines.map((label) => ({ label })) } : {}),
    }],
  };
}

/** The user turn as stored in the conversation. */
export function decisionUserSummary(
  type: DecisionQuestionType,
  question: string,
  input: string,
  linesText: string,
  labels: DecisionLabels,
): string {
  const out = [`[${labels.kind[type]}] ${question.trim()}`];
  if (input.trim()) out.push(`${labels.state}: ${input.trim()}`);
  const lines = parseDecisionLines(linesText);
  if (type === "choice") out.push(`${labels.options}: ${lines.join(" / ")}`);
  if (type === "score") out.push(`${labels.levels}: ${lines.join(" < ")}`);
  return out.join("\n");
}

function escapeTableCell(text: string): string {
  return text.replace(/\\/g, "\\\\").replace(/\|/g, "\\|").replace(/\r?\n/g, " ");
}

function percent(p: number): string {
  return `${(p * 100).toFixed(p >= 0.001 || p === 0 ? 1 : 2)}%`;
}

// At most 10 blocks so the bar column never wraps in the chat bubble.
function bar(p: number): string {
  return "█".repeat(Math.round(p * 10)) || "▏";
}

function table(header: string, rows: { name: string; probability: number; bold: boolean }[], labels: DecisionLabels): string[] {
  return [
    `| ${header} | ${labels.probability} | |`,
    "| --- | ---: | --- |",
    ...rows.map((row) => {
      const name = escapeTableCell(row.name);
      return `| ${row.bold ? `**${name}**` : name} | ${percent(row.probability)} | ${bar(row.probability)} |`;
    }),
  ];
}

/** Renders one OpenAI Decisions answer as Markdown. */
export function formatDecisionAnswer(answer: DecisionAnswer, labels: DecisionLabels): string {
  switch (answer.type) {
    case "predicate": {
      const p = answer.probability ?? 0;
      return [
        `**${labels.probabilityTrue}: ${percent(p)}**`,
        "",
        ...table(labels.option, [
          { name: labels.yes, probability: p, bold: p >= 0.5 },
          { name: labels.no, probability: 1 - p, bold: p < 0.5 },
        ], labels),
      ].join("\n");
    }
    case "choice": {
      const probs = answer.probabilities || [];
      return [
        `**${labels.choice}: ${escapeTableCell(String(answer.choice))}** · ${labels.confidence} ${(answer.confidence ?? 0).toFixed(2)}`,
        "",
        ...table(labels.option, probs.map((p) => ({
          name: String(p.value),
          probability: p.probability,
          bold: p.value === answer.choice,
        })), labels),
      ].join("\n");
    }
    case "score": {
      const probs = answer.probabilities || [];
      const best = probs.reduce((b, p, i) => (p.probability > probs[b].probability ? i : b), 0);
      return [
        `**${labels.score}: ${(answer.score ?? 0).toFixed(2)}** · ${labels.confidence} ${(answer.confidence ?? 0).toFixed(2)}`,
        "",
        ...table(labels.level, probs.map((p, i) => ({
          name: `${p.value} · ${p.label ?? ""}`,
          probability: p.probability,
          bold: i === best,
        })), labels),
      ].join("\n");
    }
    default:
      return labels.refused;
  }
}

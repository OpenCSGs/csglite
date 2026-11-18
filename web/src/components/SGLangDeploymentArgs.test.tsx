// @vitest-environment jsdom
import { render } from "preact";
import { useState } from "preact/hooks";
import { act } from "preact/test-utils";
import { expect, it, vi } from "vitest";
import { SGLangDeploymentArgs, SGLangMTPParams } from "./SGLangDeploymentArgs";

vi.hoisted(() => {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: { getItem: () => null, setItem: () => undefined },
  });
});

it("edits stored MTP flags separately from collapsed advanced arguments", () => {
  const container = document.createElement("div");
  function Form() {
    const [args, setArgs] = useState("--speculative-algorithm=EAGLE --speculative-num-steps 2\n--chunked-prefill-size 4096");
    return <>
      <SGLangMTPParams value={args} onInput={setArgs} />
      <SGLangDeploymentArgs value={args} onInput={setArgs} />
      <output>{args}</output>
    </>;
  }

  act(() => render(<Form />, container));
  const algorithm = container.querySelector("select")!;
  const steps = container.querySelector("input")!;
  const advanced = container.querySelector("details")!;
  const textarea = advanced.querySelector("textarea")!;
  expect(advanced.open).toBe(false);
  expect(advanced.contains(algorithm)).toBe(false);
  expect(algorithm.value).toBe("EAGLE");
  expect(steps.value).toBe("2");
  expect(textarea.value).toBe("--chunked-prefill-size 4096");

  act(() => {
    textarea.value += "\n";
    textarea.dispatchEvent(new Event("input", { bubbles: true }));
  });
  expect(textarea.value.endsWith("\n")).toBe(true);
  act(() => {
    algorithm.value = "none";
    algorithm.dispatchEvent(new Event("change", { bubbles: true }));
  });
  expect(steps.disabled).toBe(true);
  expect(container.querySelector("output")!.textContent).toBe("--speculative-algorithm none\n--chunked-prefill-size 4096\n");
  act(() => render(null, container));
});

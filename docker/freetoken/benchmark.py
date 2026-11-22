"""Measure single-request decode speed with the A800 comparison prompt."""

import argparse
import json
import statistics
import time
from urllib.error import HTTPError
from urllib.request import ProxyHandler, Request, build_opener

PROMPT = (
    "请用中文写一份详细的、逐条编号的数据中心 GPU 推理性能排查手册，"
    "至少给出 30 个完整步骤，每步写出原理和操作细节。直接开始列清单。"
)


def measure(opener, request, warmup):
    for attempt in range(60):
        start = time.monotonic()
        try:
            response = opener.open(request, timeout=240)
            break
        except HTTPError as error:
            if not warmup or error.code != 503 or attempt == 59:
                raise
            error.close()
            time.sleep(4)

    first = last = None
    usage = {}
    timings = {}
    with response:
        for line in response:
            if not line.startswith(b"data:"):
                continue
            payload = line[5:].strip()
            if payload == b"[DONE]":
                break
            event = json.loads(payload)
            if event.get("error"):
                raise RuntimeError(event["error"])
            if event.get("usage"):
                usage = event["usage"]
            if event.get("timings"):
                timings = event["timings"]
            for choice in event.get("choices", []):
                delta = choice.get("delta", {})
                if any(delta.get(field) for field in ("content", "reasoning_content", "reasoning")):
                    last = time.monotonic()
                    if first is None:
                        first = last

    tokens = usage.get("completion_tokens", 0)
    if first is None or last <= first or tokens < 2:
        raise RuntimeError("Missing streaming token timings or usage")
    sample = {
        "warmup": warmup,
        "ttft_s": round(first - start, 3),
        "completion_tokens": tokens,
        "prompt_tokens": usage.get("prompt_tokens"),
        "decode_tps": round((tokens - 1) / (last - first), 2),
    }
    if timings:
        sample["engine_timings"] = timings
    return sample


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("base_url", help="Server address without /v1")
    parser.add_argument("model", help="OpenAI API model ID")
    args = parser.parse_args()

    body = {
        "model": args.model,
        "messages": [{"role": "user", "content": PROMPT}],
        "temperature": 0,
        "max_tokens": 384,
        "stream": True,
        "stream_options": {"include_usage": True},
    }
    request = Request(
        args.base_url.rstrip("/") + "/v1/chat/completions",
        data=json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    opener = build_opener(ProxyHandler({}))
    samples = []
    for index in range(4):
        sample = measure(opener, request, warmup=index == 0)
        print(json.dumps(sample), flush=True)
        if index > 0:
            samples.append(sample)
    print(json.dumps({
        "median_ttft_s": statistics.median(s["ttft_s"] for s in samples),
        "median_decode_tps": statistics.median(s["decode_tps"] for s in samples),
    }))


if __name__ == "__main__":
    main()

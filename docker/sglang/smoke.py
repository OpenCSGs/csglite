"""Run inside the gateway image with --network none; no GPU or model required.

Verifies the real CSGLite binary discovers the seeded provider and forwards chat
to an OpenAI fixture. This does not validate SGLang GPU compatibility.
"""
import json
import os
from pathlib import Path
import signal
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import ProxyHandler, Request, build_opener


class Backend(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        payload = {"object": "list", "data": [{"id": "local-sglang", "object": "model"}]}
        self.reply(payload)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        assert body["model"] == "local-sglang", body
        if body.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            chunk = {"id": "test", "object": "chat.completion.chunk", "model": body["model"],
                     "choices": [{"index": 0, "delta": {"content": "sglang-stream-ok"},
                                  "finish_reason": None}]}
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n").encode())
            return
        self.reply({"id": "test", "object": "chat.completion", "model": body["model"],
                    "choices": [{"index": 0, "message": {"role": "assistant", "content": "sglang-smoke-ok"},
                                 "finish_reason": "stop"}],
                    "usage": {"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}})

    def reply(self, payload):
        data = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)


home = Path.home() / ".csghub-lite"
home.mkdir(exist_ok=True)
(home / "config.json").write_text(json.dumps({"cloud_provider_enabled": False}))
server = ThreadingHTTPServer(("127.0.0.1", 30000), Backend)
threading.Thread(target=server.serve_forever, daemon=True).start()
env = dict(os.environ, SGLANG_BASE_URL="http://127.0.0.1:30000/v1",
           CSGHUB_LITE_CLUSTER_DISABLED="1", CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER="0")
process = subprocess.Popen(["python3", "/opt/csglite-sglang/launcher.py"], env=env)
client = build_opener(ProxyHandler({}))
try:
    deadline = time.monotonic() + 60
    while True:
        try:
            with client.open("http://127.0.0.1:11435/api/health", timeout=2):
                break
        except OSError:
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("CSGLite failed to become ready")
            time.sleep(0.5)
    with client.open("http://127.0.0.1:11435/v1/models", timeout=5) as response:
        models = json.load(response)
    assert any(m["id"] == "local-sglang" for m in models["data"]), models
    request = Request("http://127.0.0.1:11435/v1/chat/completions", data=json.dumps({
        "model": "local-sglang", "messages": [{"role": "user", "content": "hello"}],
        "stream": False}).encode(), headers={"Content-Type": "application/json"})
    with client.open(request, timeout=20) as response:
        result = json.load(response)
    assert result["choices"][0]["message"]["content"] == "sglang-smoke-ok", result
    request = Request("http://127.0.0.1:11435/v1/chat/completions", data=json.dumps({
        "model": "local-sglang", "messages": [{"role": "user", "content": "hello"}],
        "stream": True}).encode(), headers={"Content-Type": "application/json"})
    with client.open(request, timeout=20) as response:
        events = response.read().decode()
    assert "sglang-stream-ok" in events and "[DONE]" in events, events
    subprocess.run(["python3", "/opt/csglite-sglang/launcher.py", "health"], env=env, check=True)
    print("PASS: model registration, real CSGLite chat/stream proxy, combined health")
finally:
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=25)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
    server.shutdown()
assert process.returncode == 0, process.returncode
print("PASS: graceful shutdown")

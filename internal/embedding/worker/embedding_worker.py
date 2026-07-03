#!/usr/bin/env python3
import argparse
import base64
import io
import json
import os
import tempfile
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any, Dict, List, Optional
from urllib.parse import urlparse
from urllib.request import urlopen

import librosa
import torch
from PIL import Image
from transformers import AutoModel, AutoProcessor, WhisperFeatureExtractor


WORKER = None


def _device() -> str:
    if torch.cuda.is_available():
        return "cuda"
    if getattr(torch.backends, "mps", None) is not None and torch.backends.mps.is_available():
        return "mps"
    return "cpu"


def _is_data_url(value: str) -> bool:
    return value.strip().startswith("data:")


def _decode_data_url(value: str) -> bytes:
    payload = value.strip()
    if "," in payload:
        payload = payload.split(",", 1)[1]
    return base64.b64decode(payload, validate=False)


def _is_http_url(value: str) -> bool:
    parsed = urlparse(value)
    return parsed.scheme in ("http", "https")


def _safe_local_path(value: str, storage_root: str) -> str:
    path = Path(value).expanduser().resolve()
    root = Path(storage_root).expanduser().resolve()
    if path == root or root in path.parents:
        return str(path)
    raise ValueError("local media path must be inside the csghub-lite storage root")


def _stage_bytes(data: bytes, suffix: str, temp_dir: str) -> str:
    os.makedirs(temp_dir, exist_ok=True)
    fd, path = tempfile.mkstemp(prefix="embedding-", suffix=suffix, dir=temp_dir)
    with os.fdopen(fd, "wb") as f:
        f.write(data)
    return path


def _media_to_path(value: str, storage_root: str, temp_dir: str, suffix: str) -> str:
    value = value.strip()
    if _is_data_url(value):
        return _stage_bytes(_decode_data_url(value), suffix, temp_dir)
    if _is_http_url(value):
        with urlopen(value, timeout=30) as resp:
            return _stage_bytes(resp.read(), suffix, temp_dir)
    return _safe_local_path(value, storage_root)


class Worker:
    def __init__(self, model_dir: str, model_name: str, storage_root: str, temp_dir: str) -> None:
        self.model_dir = model_dir
        self.model_name = model_name
        self.storage_root = storage_root
        self.temp_dir = temp_dir
        self.device = _device()
        self.model = AutoModel.from_pretrained(
            model_dir,
            trust_remote_code=True,
            default_task="retrieval",
        ).eval()
        self.model = self.model.to(self.device)
        self.processor = AutoProcessor.from_pretrained(model_dir, trust_remote_code=True)
        self.audio_extractor = WhisperFeatureExtractor(feature_size=128)

    def _text_embedding(self, text: str, prompt_name: str) -> torch.Tensor:
        if hasattr(self.model, "encode"):
            return self.model.encode([text], task="retrieval", prompt_name=prompt_name)[0]
        prefix = "Query: " if prompt_name == "query" else "Document: "
        batch = self.processor(text=prefix + text, return_tensors="pt").to(self.device)
        return self.model.embed(**batch)[0]

    def _image_embedding(self, item: Dict[str, Any], prompt_name: str) -> torch.Tensor:
        image_value = str(item.get("image") or "")
        if not image_value:
            raise ValueError("image input is required")
        image_path = _media_to_path(image_value, self.storage_root, self.temp_dir, ".img")
        image = Image.open(image_path).convert("RGB")
        text = str(item.get("text") or "<image>")
        if "<image>" not in text:
            text = ("Query: " if prompt_name == "query" else "Document: ") + text + " <image>"
        batch = self.processor(images=image, text=text, return_tensors="pt").to(self.device)
        return self.model.embed(**batch)[0]

    def _audio_embedding(self, item: Dict[str, Any]) -> torch.Tensor:
        audio_value = str(item.get("audio") or "")
        if not audio_value:
            raise ValueError("audio input is required")
        audio_path = _media_to_path(audio_value, self.storage_root, self.temp_dir, ".audio")
        audio, _ = librosa.load(audio_path, sr=16000)
        feat = self.audio_extractor(audio, sampling_rate=16000, return_tensors="pt")["input_features"]
        cfg = self.model.config
        n = feat.shape[-1] // 4
        audio_ids = [cfg.audio_token_id for _ in range(n)]
        ids = torch.tensor([[cfg.audio_start_token_id, *audio_ids, cfg.audio_end_token_id]])
        return self.model.embed(
            input_ids=ids.to(self.device),
            attention_mask=torch.ones_like(ids).to(self.device),
            input_features=feat.to(self.device, dtype=next(self.model.parameters()).dtype),
        )[0]

    def _embed_one(self, item: Any, prompt_name: str) -> List[float]:
        if isinstance(item, str):
            vec = self._text_embedding(item, prompt_name)
        elif isinstance(item, dict):
            if item.get("audio"):
                vec = self._audio_embedding(item)
            elif item.get("image"):
                vec = self._image_embedding(item, prompt_name)
            elif item.get("text") is not None:
                vec = self._text_embedding(str(item.get("text") or ""), prompt_name)
            else:
                raise ValueError("embedding input object must include text, image, or audio")
        else:
            raise ValueError("embedding input must be a string, object, or array of those")
        return vec.detach().float().cpu().tolist()

    def embeddings(self, req: Dict[str, Any]) -> Dict[str, Any]:
        raw_input = req.get("input")
        if raw_input is None:
            raise ValueError("input is required")
        inputs = raw_input if isinstance(raw_input, list) else [raw_input]
        prompt_name = str(req.get("prompt_name") or req.get("encoding_task") or "document").lower()
        if prompt_name in ("query", "encode_query"):
            prompt_name = "query"
        else:
            prompt_name = "document"
        data = []
        for idx, item in enumerate(inputs):
            data.append({
                "object": "embedding",
                "index": idx,
                "embedding": self._embed_one(item, prompt_name),
            })
        return {
            "object": "list",
            "model": req.get("model") or self.model_name,
            "data": data,
            "usage": {
                "prompt_tokens": 0,
                "total_tokens": 0,
            },
        }


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt: str, *args: Any) -> None:
        return

    def write_json(self, status: int, payload: Dict[str, Any]) -> None:
        data = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self) -> None:
        if self.path != "/health":
            self.write_json(404, {"error": "not found"})
            return
        self.write_json(200, {"status": "ok", "model": WORKER.model_name, "device": WORKER.device})

    def do_POST(self) -> None:
        if self.path != "/v1/embeddings":
            self.write_json(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            req = json.loads(self.rfile.read(length).decode("utf-8"))
            self.write_json(200, WORKER.embeddings(req))
        except ValueError as exc:
            self.write_json(400, {"error": str(exc)})
        except Exception as exc:
            self.write_json(500, {"error": str(exc)})


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", required=True)
    parser.add_argument("--model-name", required=True)
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--storage-root", required=True)
    parser.add_argument("--temp-dir", required=True)
    args = parser.parse_args()

    global WORKER
    WORKER = Worker(args.model_dir, args.model_name, args.storage_root, args.temp_dir)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()

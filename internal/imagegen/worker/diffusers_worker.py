import argparse
import base64
from pathlib import Path
import inspect
import io
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading
import time
from typing import Any, Dict, List, Optional

import torch
from diffusers import DiffusionPipeline
from PIL import Image


def parse_size(value: Optional[str]) -> tuple[int, int]:
    if not value:
        return 1024, 1024
    parts = value.lower().split("x", 1)
    if len(parts) != 2:
        raise ValueError("size must be WIDTHxHEIGHT")
    try:
        width = int(parts[0])
        height = int(parts[1])
    except ValueError as exc:
        raise ValueError("size must be WIDTHxHEIGHT") from exc
    if width <= 0 or height <= 0:
        raise ValueError("size must be positive")
    return width, height


def decode_image(value: str) -> Image.Image:
    payload = value.strip()
    if payload.startswith("data:"):
        _, _, payload = payload.partition(",")
    try:
        data = base64.b64decode(payload, validate=True)
    except (ValueError, TypeError) as exc:
        raise ValueError("image must be valid base64 data") from exc
    try:
        image = Image.open(io.BytesIO(data))
    except OSError as exc:
        raise ValueError("image must be a PNG or JPEG file") from exc
    return image.convert("RGB")


def is_qwen_image_model(model_dir: str, model_name: str) -> bool:
    model_tokens = f"{model_name} {model_dir}".lower()
    if "qwen-image" in model_tokens or "qwen_image" in model_tokens:
        return True

    model_index = Path(model_dir) / "model_index.json"
    try:
        data = json.loads(model_index.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return False
    class_name = str(data.get("_class_name", "")).lower()
    return "qwenimage" in class_name


def detect_device(model_dir: str, model_name: str) -> tuple[str, torch.dtype]:
    if torch.cuda.is_available():
        if is_qwen_image_model(model_dir, model_name) and torch.cuda.is_bf16_supported():
            return "cuda", torch.bfloat16
        return "cuda", torch.float16
    if getattr(torch.backends, "mps", None) is not None and torch.backends.mps.is_available():
        return "mps", torch.float16
    return "cpu", torch.float32


class Worker:
    def __init__(self, model_dir: str, model_name: str) -> None:
        self.model_dir = model_dir
        self.model_name = model_name
        self.device, self.dtype = detect_device(model_dir, model_name)
        self.pipeline = None
        self.lock = threading.Lock()

    def load(self) -> None:
        kwargs: Dict[str, Any] = {"torch_dtype": self.dtype, "local_files_only": True}
        self.pipeline = DiffusionPipeline.from_pretrained(self.model_dir, **kwargs)
        if self.device == "cuda":
            self.pipeline = self.pipeline.to("cuda")
            if hasattr(self.pipeline, "enable_model_cpu_offload"):
                self.pipeline.enable_model_cpu_offload()
        elif self.device == "mps":
            self.pipeline = self.pipeline.to("mps")
        else:
            self.pipeline = self.pipeline.to("cpu")
        if hasattr(self.pipeline, "enable_attention_slicing"):
            self.pipeline.enable_attention_slicing()
        if hasattr(self.pipeline, "enable_vae_tiling"):
            self.pipeline.enable_vae_tiling()

    def _input_images(self, req: Dict[str, Any]) -> List[str]:
        images: List[str] = []
        if req.get("image"):
            images.append(str(req["image"]))
        extra = req.get("images")
        if isinstance(extra, list):
            for item in extra:
                if item:
                    images.append(str(item))
        return images

    def build_call_kwargs(self, req: Dict[str, Any]) -> Dict[str, Any]:
        if self.pipeline is None:
            raise RuntimeError("pipeline is not loaded")
        signature = inspect.signature(self.pipeline.__call__)
        params = signature.parameters
        kwargs: Dict[str, Any] = {"prompt": req.get("prompt", "")}

        input_images = self._input_images(req)
        if "image" in params:
            if not input_images:
                image_param = params["image"]
                if image_param.default is inspect.Parameter.empty:
                    raise ValueError("image is required for this model")
            else:
                decoded = [decode_image(item) for item in input_images]
                kwargs["image"] = decoded if len(decoded) > 1 else decoded[0]

        if "width" in params and "height" in params:
            width, height = parse_size(req.get("size"))
            kwargs["width"] = width
            kwargs["height"] = height

        if "num_images_per_prompt" in params:
            count = max(1, min(int(req.get("n") or 1), 4))
            kwargs["num_images_per_prompt"] = count

        if req.get("negative_prompt") and "negative_prompt" in params:
            kwargs["negative_prompt"] = req["negative_prompt"]

        if req.get("steps") and "num_inference_steps" in params:
            kwargs["num_inference_steps"] = int(req["steps"])

        if req.get("cfg_scale") is not None:
            cfg_scale = float(req["cfg_scale"])
            if "true_cfg_scale" in params:
                kwargs["true_cfg_scale"] = cfg_scale
            elif "guidance_scale" in params:
                kwargs["guidance_scale"] = cfg_scale

        if "true_cfg_scale" in kwargs and "guidance_scale" in params and "guidance_scale" not in kwargs:
            kwargs["guidance_scale"] = 1.0

        if req.get("seed") is not None:
            generator = torch.Generator(device=self.device if self.device != "mps" else "cpu").manual_seed(int(req["seed"]))
            if "generator" in params:
                kwargs["generator"] = generator

        return kwargs

    def generate(self, req: Dict[str, Any]) -> Dict[str, Any]:
        kwargs = self.build_call_kwargs(req)
        with self.lock:
            result = self.pipeline(**kwargs)
        data: List[Dict[str, str]] = []
        for image in result.images:
            buf = io.BytesIO()
            image.save(buf, format="PNG")
            encoded = base64.b64encode(buf.getvalue()).decode("ascii")
            data.append({"b64_json": encoded})
        return {"created": int(time.time()), "data": data}


class Handler(BaseHTTPRequestHandler):
    worker: Worker

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
        self.write_json(
            200,
            {
                "status": "ok",
                "device": self.worker.device,
                "dtype": str(self.worker.dtype).removeprefix("torch."),
                "model": self.worker.model_name,
            },
        )

    def do_POST(self) -> None:
        if self.path != "/generate":
            self.write_json(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            req = json.loads(self.rfile.read(length).decode("utf-8"))
            if not req.get("prompt"):
                self.write_json(400, {"error": "prompt is required"})
                return
            self.write_json(200, self.worker.generate(req))
        except ValueError as exc:
            self.write_json(400, {"error": str(exc)})
        except Exception as exc:
            self.write_json(500, {"error": str(exc)})


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", required=True)
    parser.add_argument("--model-name", required=True)
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()

    worker = Worker(args.model_dir, args.model_name)
    worker.load()
    Handler.worker = worker
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()

import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
import inspect
import io
import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import os
import platform
import tempfile
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


MLX_NATIVE_COMPONENTS = ("transformer", "text_encoder", "vae")


def _safetensors_metadata(path: Path) -> Dict[str, Any]:
    try:
        with path.open("rb") as handle:
            size = int.from_bytes(handle.read(8), "little")
            header = json.loads(handle.read(size))
    except (OSError, ValueError):
        return {}
    metadata = header.get("__metadata__")
    return metadata if isinstance(metadata, dict) else {}


def is_mlx_native_checkpoint(model_dir: str) -> bool:
    """Reports whether model_dir is a pre-converted MLX checkpoint such as
    mlx-community/Qwen-Image-2.1-MLX-4bit: diffusers folder layout, but weights
    already in MLX layout (optionally quantized, described by each component's
    config.json) rather than torch layout or mflux's own saved format."""
    for name in MLX_NATIVE_COMPONENTS:
        files = sorted((Path(model_dir) / name).glob("*.safetensors"))
        if not files:
            return False
        metadata = _safetensors_metadata(files[0])
        if metadata.get("format") != "mlx" or "quantization_level" in metadata:
            return False
    return True


def _rename_qwen21_transformer_key(key: str) -> Optional[str]:
    if key.startswith("modulation."):
        # The converter dropped the SiLU from diffusers' Sequential(SiLU, Linear).
        index, _, rest = key[len("modulation."):].partition(".")
        return f"modulation.layers.{int(index) + 1}.{rest}"
    if key.startswith("time_text_embed.linear_"):
        return "time_text_embed.timestep_embedder." + key[len("time_text_embed."):]
    return key


def _rename_qwen21_text_encoder_key(key: str) -> Optional[str]:
    prefix = "language_model.model."
    if not key.startswith(prefix):
        # lm_head and vision_tower are unused by the text-to-image encoder.
        return None
    return key[len(prefix):]


def _rename_qwen21_vae_key(key: str) -> Optional[str]:
    if ".time_conv." in key:
        # Video-only temporal convolutions; the image VAE has no counterpart.
        return None
    key = key.replace(".resample.1.", ".conv.")
    if key.endswith(".gamma"):
        key = key[: -len(".gamma")] + ".weight"
    return key


def _map_mlx_native_weights(module: Any, weights: Dict[str, Any], rename: Any, component: str) -> Dict[str, Any]:
    from mlx.utils import tree_flatten

    expected = {key for key, _ in tree_flatten(module.parameters())}
    mapped: Dict[str, Any] = {}
    for key, value in weights.items():
        target = rename(key)
        if target is None:
            continue
        base, _, leaf = target.rpartition(".")
        if leaf in ("weight", "bias") and target not in expected and f"{base}.conv.{leaf}" in expected:
            # mflux wraps convolutions in a module holding a .conv child.
            target = f"{base}.conv.{leaf}"
        mapped[target] = value
    wanted = {key for key in expected if key.rpartition(".")[2] in ("weight", "bias")}
    missing = sorted(wanted - set(mapped))
    if missing:
        raise RuntimeError(
            f"MLX checkpoint {component} is missing {len(missing)} weights, for example {missing[:3]}"
        )
    return mapped


def load_mlx_native_qwen21(model_dir: str) -> Any:
    import mlx.core as mx
    from mlx import nn
    from mlx.utils import tree_unflatten
    from mflux.models.common.config import ModelConfig
    from mflux.models.qwen21.qwen21_initializer import Qwen21Initializer
    from mflux.models.qwen21.variants.txt2img.qwen_image_21 import QwenImage21

    pipeline = QwenImage21.__new__(QwenImage21)
    nn.Module.__init__(pipeline)
    Qwen21Initializer._init_config(pipeline, ModelConfig.qwen_image_21())
    Qwen21Initializer._init_tokenizers(pipeline, model_dir)
    Qwen21Initializer._init_models(pipeline)

    renames = {
        "transformer": _rename_qwen21_transformer_key,
        "text_encoder": _rename_qwen21_text_encoder_key,
        "vae": _rename_qwen21_vae_key,
    }
    bits: Optional[int] = None
    for name in MLX_NATIVE_COMPONENTS:
        component_dir = Path(model_dir) / name
        module = getattr(pipeline, name)
        raw: Dict[str, Any] = {}
        for shard in sorted(component_dir.glob("*.safetensors")):
            raw.update(mx.load(str(shard)))
        mapped = _map_mlx_native_weights(module, raw, renames[name], name)

        try:
            config = json.loads((component_dir / "config.json").read_text(encoding="utf-8"))
        except (OSError, ValueError):
            config = {}
        quantization = config.get("quantization")
        if isinstance(quantization, dict):
            # Quantize exactly the layers the checkpoint stores packed, before
            # loading, so packed weights land in QuantizedLinear/Embedding.
            nn.quantize(
                module,
                group_size=int(quantization.get("group_size", 64)),
                bits=int(quantization.get("bits", 4)),
                class_predicate=lambda path, layer: hasattr(layer, "to_quantized") and f"{path}.scales" in mapped,
            )
            bits = int(quantization.get("bits", 4))
        module.update(tree_unflatten(list(mapped.items())), strict=False)
        mx.eval(module.parameters())
    pipeline.bits = bits
    return pipeline


class Worker:
    def __init__(self, model_dir: str, model_name: str, backend: str) -> None:
        self.model_dir = model_dir
        self.model_name = model_name
        self.backend = backend
        self.device, self.dtype = detect_device(model_dir, model_name)
        if backend == "mlx-qwen-image-2.1":
            self.device = "mlx"
            self.dtype = None
        self.pipeline = None
        self.lock = threading.Lock()
        # MLX streams are per-thread, and mx.load returns lazy arrays bound to
        # the loading thread's stream. Evaluating them from an HTTP handler
        # thread aborts the process ("There is no Stream(gpu, N) in current
        # thread"), so MLX load and generate share one dedicated thread.
        self.mlx_executor: Optional[ThreadPoolExecutor] = None
        if backend == "mlx-qwen-image-2.1":
            self.mlx_executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix="mlx")

    def load(self) -> None:
        if self.mlx_executor is not None:
            self.mlx_executor.submit(self._load_mlx_qwen21).result()
            return
        if self.backend != "diffusers":
            raise ValueError(f"unsupported image backend: {self.backend}")
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

    def _load_mlx_qwen21(self) -> None:
        if platform.system() != "Darwin" or platform.machine() != "arm64":
            raise RuntimeError("the MLX Qwen-Image-2.1 backend requires Apple Silicon")
        from mflux.models.common.config import ModelConfig
        from mflux.models.qwen21.variants.txt2img.qwen_image_21 import QwenImage21

        if is_mlx_native_checkpoint(self.model_dir):
            self.pipeline = load_mlx_native_qwen21(self.model_dir)
            return
        self.pipeline = QwenImage21(
            model_path=self.model_dir,
            model_config=ModelConfig.qwen_image_21(),
        )

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

        if "width" in params and "height" in params and req.get("size"):
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
        if self.mlx_executor is not None:
            return self.mlx_executor.submit(self._generate_mlx_qwen21, req).result()
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

    def _generate_mlx_qwen21(self, req: Dict[str, Any]) -> Dict[str, Any]:
        input_images = self._input_images(req)
        if len(input_images) > 1:
            raise ValueError("the MLX Qwen-Image-2.1 backend supports one input image only")
        if req.get("n") not in (None, 1):
            raise ValueError("the MLX Qwen-Image-2.1 backend supports n=1 only")

        width, height = parse_size(req.get("size"))
        image_path = None
        try:
            if input_images:
                image = decode_image(input_images[0])
                fd, image_path = tempfile.mkstemp(dir=self.model_dir, prefix=".csglite-qwen21-", suffix=".png")
                os.close(fd)
                image.save(image_path, format="PNG")
            kwargs: Dict[str, Any] = {
                "seed": int(req.get("seed") or 0),
                "prompt": str(req.get("prompt") or ""),
                "width": width,
                "height": height,
                "num_inference_steps": int(req.get("steps") or 40),
                "image_path": image_path,
            }
            if req.get("cfg_scale") is not None:
                kwargs["guidance"] = float(req["cfg_scale"])
            if req.get("negative_prompt"):
                kwargs["negative_prompt"] = str(req["negative_prompt"])
            with self.lock:
                result = self.pipeline.generate_image(**kwargs)
            image = getattr(result, "image", result)
            buf = io.BytesIO()
            image.save(buf, format="PNG")
            return {
                "created": int(time.time()),
                "data": [{"b64_json": base64.b64encode(buf.getvalue()).decode("ascii")}],
            }
        finally:
            if image_path:
                try:
                    os.unlink(image_path)
                except OSError:
                    pass


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
                "dtype": "mlx" if self.worker.dtype is None else str(self.worker.dtype).removeprefix("torch."),
                "model": self.worker.model_name,
                "backend": self.worker.backend,
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
    parser.add_argument("--backend", default="diffusers")
    parser.add_argument("--port", type=int, required=True)
    args = parser.parse_args()

    worker = Worker(args.model_dir, args.model_name, args.backend)
    worker.load()
    Handler.worker = worker
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.serve_forever()


if __name__ == "__main__":
    main()

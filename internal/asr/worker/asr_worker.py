#!/usr/bin/env python3
import argparse
import json
import os
import re
import shutil
import sys
import tempfile

import uvicorn
from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse


ENGINE = None
app = FastAPI(title="csghub-lite ASR worker", version="1.0.0")


def _env_bool(name, default=False):
    value = os.getenv(name)
    if value is None:
        return default
    return value.lower() in {"1", "true", "yes", "on"}


def _clean_text(text):
    return re.sub(r"<\|[^|]*\|>", "", text or "").strip()


def _load_config(model_dir):
    path = os.path.join(model_dir, "config.json")
    if not os.path.exists(path):
        return {}
    with open(path, "r", encoding="utf-8") as f:
        return json.load(f)


def _is_whisper_model(model_dir):
    cfg = _load_config(model_dir)
    model_type = str(cfg.get("model_type", "")).lower()
    archs = [str(x).lower() for x in cfg.get("architectures", [])]
    return model_type == "whisper" or any("whisper" in x for x in archs)


def _safetensors_is_valid(model_dir):
    path = os.path.join(model_dir, "model.safetensors")
    if not os.path.exists(path):
        return False
    try:
        from safetensors import safe_open

        with safe_open(path, framework="pt", device="cpu") as f:
            _ = list(f.keys())
        return True
    except Exception:
        return False


def _device_for_funasr(hardware):
    if hardware == "cuda":
        return "cuda:0"
    if hardware == "mps":
        return "mps"
    return "cpu"


def _device_for_transformers(hardware):
    if hardware == "cuda":
        return 0
    if hardware == "mps":
        return "mps"
    return -1


def _ensure_python_ffmpeg_on_path():
    try:
        import imageio_ffmpeg

        ffmpeg = imageio_ffmpeg.get_ffmpeg_exe()
        if ffmpeg and os.path.exists(ffmpeg):
            shim_dir = os.path.join(tempfile.gettempdir(), "csghub-lite-ffmpeg")
            os.makedirs(shim_dir, exist_ok=True)
            shim_name = "ffmpeg.exe" if os.name == "nt" else "ffmpeg"
            shim_path = os.path.join(shim_dir, shim_name)
            if not os.path.exists(shim_path):
                try:
                    os.symlink(ffmpeg, shim_path)
                except Exception:
                    shutil.copy2(ffmpeg, shim_path)
            try:
                os.chmod(shim_path, 0o755)
            except Exception:
                pass
            os.environ["PATH"] = shim_dir + os.pathsep + os.environ.get("PATH", "")
    except Exception:
        # Transformers can still use a system ffmpeg if one exists.
        pass


class TransformersASREngine:
    def __init__(self, model_dir, hardware):
        from transformers import pipeline

        _ensure_python_ffmpeg_on_path()
        self.backend = "transformers"
        model_kwargs = {}
        if os.path.exists(os.path.join(model_dir, "pytorch_model.bin")) and not _safetensors_is_valid(model_dir):
            model_kwargs["use_safetensors"] = False
        self.pipe = pipeline(
            "automatic-speech-recognition",
            model=model_dir,
            device=_device_for_transformers(hardware),
            model_kwargs=model_kwargs,
        )

    def transcribe(self, req):
        kwargs = {}
        language = (req.get("language") or "").strip()
        prompt = (req.get("prompt") or "").strip()
        if language or prompt:
            generate_kwargs = {}
            if language:
                generate_kwargs["language"] = language
            if prompt:
                generate_kwargs["prompt_ids"] = self.pipe.tokenizer.get_prompt_ids(prompt)
            kwargs["generate_kwargs"] = generate_kwargs
        result = self.pipe(req["file_path"], **kwargs)
        text = result.get("text", "") if isinstance(result, dict) else str(result)
        return {
            "text": text,
            "backend": self.backend,
            "language": language,
        }


class FunASREngine:
    def __init__(self, model_dir, hardware):
        _ensure_python_ffmpeg_on_path()
        from funasr import AutoModel

        self.backend = "funasr"
        self.model = AutoModel(
            model=model_dir,
            trust_remote_code=_env_bool("FUNASR_TRUST_REMOTE_CODE", False),
            device=_device_for_funasr(hardware),
            disable_update=True,
            disable_pbar=True,
        )

    def transcribe(self, req):
        kwargs = {
            "input": req["file_path"],
            "batch_size": 1,
        }
        language = (req.get("language") or "").strip()
        if language:
            kwargs["language"] = language
        if "itn" in req and req["itn"] is not None:
            kwargs["itn"] = bool(req["itn"])
        hotwords = req.get("hotwords") or []
        if hotwords:
            kwargs["hotwords"] = hotwords
        result = self.model.generate(**kwargs)
        first = result[0] if result else {}
        text = _clean_text(first.get("text", "") if isinstance(first, dict) else str(first))
        segments = []
        for i, item in enumerate(first.get("sentence_info", []) if isinstance(first, dict) else []):
            segments.append({
                "id": i,
                "start": float(item.get("start", 0)) / 1000.0,
                "end": float(item.get("end", 0)) / 1000.0,
                "text": _clean_text(item.get("text", "")),
            })
        return {
            "text": text,
            "backend": self.backend,
            "language": language,
            "segments": segments,
        }


def load_engine(model_dir, hardware):
    if _is_whisper_model(model_dir):
        return TransformersASREngine(model_dir, hardware)
    try:
        return FunASREngine(model_dir, hardware)
    except Exception as funasr_error:
        try:
            return TransformersASREngine(model_dir, hardware)
        except Exception as transformers_error:
            raise RuntimeError(
                "loading ASR model failed; "
                f"funasr={funasr_error}; transformers={transformers_error}"
            ) from transformers_error


@app.get("/health")
async def health():
    return {"status": "ok" if ENGINE is not None else "loading", "backend": getattr(ENGINE, "backend", "")}


@app.post("/transcribe")
async def transcribe(request: Request):
    if ENGINE is None:
        raise HTTPException(status_code=503, detail="model is not loaded")
    try:
        req = await request.json()
        if not req.get("file_path"):
            raise HTTPException(status_code=400, detail="file_path is required")
        return JSONResponse(ENGINE.transcribe(req))
    except HTTPException:
        raise
    except Exception as exc:
        print(f"ASR worker transcription error: {exc}", file=sys.stderr)
        raise HTTPException(status_code=500, detail=str(exc)) from exc


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", required=True)
    parser.add_argument("--model-name", required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--hardware", default="cpu")
    args = parser.parse_args()

    global ENGINE
    ENGINE = load_engine(args.model_dir, args.hardware)
    print(f"ASR worker ready model={args.model_name} backend={ENGINE.backend} port={args.port}", flush=True)
    uvicorn.run(app, host="127.0.0.1", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()

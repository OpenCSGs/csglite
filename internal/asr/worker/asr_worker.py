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
from fastapi.responses import JSONResponse, StreamingResponse


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


def _is_qwen3_asr_model(model_dir):
    cfg = _load_config(model_dir)
    model_type = str(cfg.get("model_type", "")).lower().replace("-", "_")
    archs = [str(x) for x in cfg.get("architectures", [])]
    return model_type == "qwen3_asr" or "Qwen3ASRForConditionalGeneration" in archs


def _is_glm_asr_model(model_dir):
    cfg = _load_config(model_dir)
    model_type = str(cfg.get("model_type", "")).lower().replace("-", "_")
    archs = [str(x) for x in cfg.get("architectures", [])]
    return model_type in ("glm_asr", "glmasr") or any(
        arch in ("GlmAsrForConditionalGeneration", "GlmasrModel") for arch in archs
    )


def _funasr_wrapper_model_key(model_dir, model_name):
    if not _is_qwen3_asr_model(model_dir):
        if _is_glm_asr_model(model_dir):
            return _glm_asr_model_key(model_dir, model_name)
        return ""
    candidates = [model_name, os.path.basename(os.path.normpath(model_dir))]
    for candidate in candidates:
        candidate = (candidate or "").strip()
        if candidate in ("Qwen/Qwen3-ASR-0.6B", "Qwen/Qwen3-ASR-1.7B"):
            return candidate
        if candidate in ("Qwen3-ASR-0.6B", "Qwen3-ASR-1.7B"):
            return f"Qwen/{candidate}"
    return "Qwen/Qwen3-ASR-1.7B"


def _glm_asr_model_key(model_dir, model_name):
    candidates = [model_name, os.path.basename(os.path.normpath(model_dir))]
    for candidate in candidates:
        candidate = (candidate or "").strip()
        if candidate in ("zai-org/GLM-ASR-Nano-2512", "ZhipuAI/GLM-ASR-Nano-2512"):
            return candidate
        if candidate == "GLM-ASR-Nano-2512":
            return "zai-org/GLM-ASR-Nano-2512"
    return "zai-org/GLM-ASR-Nano-2512"


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


def _audio_duration_seconds(path):
    try:
        import soundfile as sf

        info = sf.info(path)
        return float(info.duration or 0)
    except Exception:
        return 0.0


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
    def __init__(self, model_dir, model_name, hardware):
        _ensure_python_ffmpeg_on_path()
        from funasr import AutoModel

        wrapper_model_key = _funasr_wrapper_model_key(model_dir, model_name)
        model_kwargs = {
            "model": wrapper_model_key or model_dir,
            "trust_remote_code": _env_bool("FUNASR_TRUST_REMOTE_CODE", False),
            "device": _device_for_funasr(hardware),
            "disable_update": True,
            "disable_pbar": True,
        }
        if wrapper_model_key:
            model_kwargs["model_path"] = model_dir

        self.backend = "funasr"
        self.model = AutoModel(**model_kwargs)
        self.chunk_seconds = int(os.getenv("CSGHUB_ASR_CHUNK_SECONDS", "30"))
        self.long_audio_threshold_seconds = int(os.getenv("CSGHUB_ASR_LONG_AUDIO_THRESHOLD_SECONDS", str(self.chunk_seconds)))
        self.vad_model = None
        self.vad_max_segment_ms = int(os.getenv("CSGHUB_ASR_VAD_MAX_SEGMENT_MS", "30000"))
        if _env_bool("CSGHUB_ASR_USE_VAD", False):
            self.vad_model = AutoModel(
                model=os.getenv("CSGHUB_ASR_VAD_MODEL", "fsmn-vad"),
                trust_remote_code=_env_bool("FUNASR_TRUST_REMOTE_CODE", False),
                device=_device_for_funasr(hardware),
                disable_update=True,
                disable_pbar=True,
                max_single_segment_time=self.vad_max_segment_ms,
            )

    def transcribe(self, req):
        file_path = req["file_path"]
        duration = _audio_duration_seconds(file_path)
        if duration > self.long_audio_threshold_seconds:
            return self._transcribe_long_audio(req, duration)

        kwargs = {
            "input": file_path,
            "batch_size": 1,
        }
        self._apply_request_options(kwargs, req)
        result = self.model.generate(**kwargs)
        return self._format_result(result, req)

    def _apply_request_options(self, kwargs, req):
        language = (req.get("language") or "").strip()
        if language:
            kwargs["language"] = language
        if "itn" in req and req["itn"] is not None:
            kwargs["itn"] = bool(req["itn"])
        hotwords = req.get("hotwords") or []
        if hotwords:
            kwargs["hotwords"] = hotwords

    def _format_result(self, result, req, offset_seconds=0.0):
        first = result[0] if result else {}
        text = _clean_text(first.get("text", "") if isinstance(first, dict) else str(first))
        segments = []
        for i, item in enumerate(first.get("sentence_info", []) if isinstance(first, dict) else []):
            segments.append({
                "id": i,
                "start": (float(item.get("start", 0)) / 1000.0) + offset_seconds,
                "end": (float(item.get("end", 0)) / 1000.0) + offset_seconds,
                "text": _clean_text(item.get("text", "")),
            })
        return {
            "text": text,
            "backend": self.backend,
            "language": (req.get("language") or "").strip(),
            "segments": segments,
        }

    def _transcribe_long_audio(self, req, duration):
        text_parts = []
        segments = []
        segment_id = 0

        for chunk in self._iter_long_audio_chunks(req, duration):
            if chunk["text"]:
                text_parts.append(chunk["text"])
            for item in chunk.get("segments", []):
                item["id"] = segment_id
                segment_id += 1
                segments.append(item)

        return {
            "text": "".join(text_parts),
            "backend": self.backend,
            "language": (req.get("language") or "").strip(),
            "segments": segments,
        }

    def stream_transcribe(self, req):
        file_path = req["file_path"]
        duration = _audio_duration_seconds(file_path)
        if duration > self.long_audio_threshold_seconds:
            yield from self._iter_long_audio_chunks(req, duration)
            return
        yield self.transcribe(req)

    def _iter_long_audio_chunks(self, req, duration):
        try:
            segments = self._vad_segments(req["file_path"])
        except Exception as exc:
            print(f"ASR worker VAD segmentation failed, falling back to fixed chunks: {exc}", file=sys.stderr)
            segments = []
        if segments:
            yield from self._iter_audio_segments(req, segments)
            return
        yield from self._iter_fixed_audio_chunks(req, duration)

    def _vad_segments(self, file_path):
        if self.vad_model is None:
            return []
        result = self.vad_model.generate(input=file_path, cache={}, is_final=True)
        first = result[0] if result else {}
        segments = first.get("value", []) if isinstance(first, dict) else []
        out = []
        for item in segments:
            if not isinstance(item, (list, tuple)) or len(item) < 2:
                continue
            start_ms = max(0.0, float(item[0]))
            end_ms = max(start_ms, float(item[1]))
            if end_ms > start_ms:
                out.append((start_ms / 1000.0, end_ms / 1000.0))
        return out

    def _iter_fixed_audio_chunks(self, req, duration):
        chunk_seconds = max(10, self.chunk_seconds)
        segments = []
        for offset in range(0, int(duration) + 1, chunk_seconds):
            remaining = duration - float(offset)
            if remaining <= 0:
                break
            segments.append((float(offset), float(offset) + min(float(chunk_seconds), remaining)))
        yield from self._iter_audio_segments(req, segments)

    def _iter_audio_segments(self, req, segments):
        import librosa
        import soundfile as sf

        for start, end in segments:
            duration = max(0.0, float(end) - float(start))
            if duration <= 0:
                continue
            audio, _ = librosa.load(
                req["file_path"],
                sr=16000,
                mono=True,
                offset=float(start),
                duration=duration,
            )
            if audio.size == 0:
                continue
            chunk_path = ""
            try:
                fd, chunk_path = tempfile.mkstemp(prefix="csghub-asr-chunk-", suffix=".wav")
                os.close(fd)
                sf.write(chunk_path, audio, 16000)
            except Exception:
                if chunk_path:
                    try:
                        os.remove(chunk_path)
                    except Exception:
                        pass
                raise
            kwargs = {
                "input": chunk_path,
                "batch_size": 1,
            }
            try:
                self._apply_request_options(kwargs, req)
                result = self.model.generate(**kwargs)
            finally:
                try:
                    os.remove(chunk_path)
                except Exception:
                    pass
            yield self._format_result(result, req, offset_seconds=float(start))


def load_engine(model_dir, model_name, hardware):
    if _is_whisper_model(model_dir):
        return TransformersASREngine(model_dir, hardware)
    try:
        return FunASREngine(model_dir, model_name, hardware)
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


@app.post("/transcribe_stream")
async def transcribe_stream(request: Request):
    if ENGINE is None:
        raise HTTPException(status_code=503, detail="model is not loaded")
    try:
        req = await request.json()
        if not req.get("file_path"):
            raise HTTPException(status_code=400, detail="file_path is required")

        def generate():
            for chunk in ENGINE.stream_transcribe(req):
                yield json.dumps(chunk, ensure_ascii=False) + "\n"

        return StreamingResponse(generate(), media_type="application/x-ndjson")
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
    ENGINE = load_engine(args.model_dir, args.model_name, args.hardware)
    print(f"ASR worker ready model={args.model_name} backend={ENGINE.backend} port={args.port}", flush=True)
    uvicorn.run(app, host="127.0.0.1", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()

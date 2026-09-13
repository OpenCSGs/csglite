#!/usr/bin/env python3
import argparse
import asyncio
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import threading
import time

import uvicorn
from fastapi import FastAPI, HTTPException, Request, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse, StreamingResponse


ENGINE = None
# The backends are not safe to call from two threads at once, and a second
# concurrent inference would only contend for the same cores anyway. Every
# entry point runs the model off the event loop but under this lock, so
# recognition serialises while /health stays answerable throughout.
ENGINE_LOCK = threading.Lock()
app = FastAPI(title="csghub-lite ASR worker", version="1.0.0")


def _env_bool(name, default=False):
    value = os.getenv(name)
    if value is None:
        return default
    return value.lower() in {"1", "true", "yes", "on"}


def _env_float(name, default):
    try:
        return float(os.getenv(name, ""))
    except ValueError:
        return default


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
    duration, _ = _soundfile_audio_info(path)
    if duration > 0:
        return duration
    duration = _ffmpeg_duration_seconds(path)
    if duration > 0:
        return duration
    try:
        import librosa

        return float(librosa.get_duration(path=path) or 0)
    except Exception:
        return 0.0


def _soundfile_audio_info(path):
    try:
        import soundfile as sf

        info = sf.info(path)
        return float(info.duration or 0), True
    except Exception:
        return 0.0, False


def _audio_needs_wav_decode(path):
    _, soundfile_readable = _soundfile_audio_info(path)
    return not soundfile_readable


def _ffmpeg_exe():
    try:
        import imageio_ffmpeg

        ffmpeg = imageio_ffmpeg.get_ffmpeg_exe()
        if ffmpeg and os.path.exists(ffmpeg):
            return ffmpeg
    except Exception:
        pass
    return shutil.which("ffmpeg") or ""


def _ffmpeg_duration_seconds(path):
    ffmpeg = _ffmpeg_exe()
    if not ffmpeg:
        return 0.0
    try:
        proc = subprocess.run(
            [ffmpeg, "-hide_banner", "-i", path],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=15,
        )
    except Exception:
        return 0.0
    output = (proc.stderr or "") + "\n" + (proc.stdout or "")
    match = re.search(r"Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)", output)
    if not match:
        return 0.0
    hours, minutes, seconds = match.groups()
    return (int(hours) * 3600) + (int(minutes) * 60) + float(seconds)


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
        if _audio_needs_wav_decode(file_path):
            return self._transcribe_decoded_audio(req, duration)

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
        if _audio_needs_wav_decode(file_path):
            yield self._transcribe_decoded_audio(req, duration)
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
        for start, end in segments:
            duration = max(0.0, float(end) - float(start))
            if duration <= 0:
                continue
            chunk_path = _decode_audio_to_wav(req["file_path"], start=float(start), duration=duration)
            if not chunk_path:
                continue
            result = self._transcribe_audio_path(req, chunk_path)
            yield self._format_result(result, req, offset_seconds=float(start))

    def _transcribe_decoded_audio(self, req, duration):
        decode_duration = float(duration) if duration > 0 else None
        chunk_path = _decode_audio_to_wav(req["file_path"], start=0.0, duration=decode_duration)
        if not chunk_path:
            return {
                "text": "",
                "backend": self.backend,
                "language": (req.get("language") or "").strip(),
                "segments": [],
            }
        result = self._transcribe_audio_path(req, chunk_path)
        return self._format_result(result, req)

    def _transcribe_audio_path(self, req, audio_path):
        kwargs = {
            "input": audio_path,
            "batch_size": 1,
        }
        try:
            self._apply_request_options(kwargs, req)
            return self.model.generate(**kwargs)
        finally:
            try:
                os.remove(audio_path)
            except Exception:
                pass


def _decode_audio_to_wav(path, start=0.0, duration=None):
    chunk_path = _decode_audio_to_wav_with_ffmpeg(path, start=start, duration=duration)
    if chunk_path:
        return chunk_path

    import librosa
    import soundfile as sf

    kwargs = {
        "sr": 16000,
        "mono": True,
        "offset": float(start),
    }
    if duration is not None:
        kwargs["duration"] = float(duration)
    audio, _ = librosa.load(path, **kwargs)
    if audio.size == 0:
        return ""
    chunk_path = ""
    try:
        fd, chunk_path = tempfile.mkstemp(prefix="csghub-asr-chunk-", suffix=".wav")
        os.close(fd)
        sf.write(chunk_path, audio, 16000)
        return chunk_path
    except Exception:
        if chunk_path:
            try:
                os.remove(chunk_path)
            except Exception:
                pass
        raise


def _decode_audio_to_wav_with_ffmpeg(path, start=0.0, duration=None):
    ffmpeg = _ffmpeg_exe()
    if not ffmpeg:
        return ""
    chunk_path = ""
    try:
        fd, chunk_path = tempfile.mkstemp(prefix="csghub-asr-chunk-", suffix=".wav")
        os.close(fd)
        cmd = [ffmpeg, "-hide_banner", "-v", "error", "-y"]
        if float(start) > 0:
            cmd.extend(["-ss", str(float(start))])
        cmd.extend(["-i", path])
        if duration is not None:
            cmd.extend(["-t", str(float(duration))])
        cmd.extend(["-ar", "16000", "-ac", "1", "-f", "wav", chunk_path])
        subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True, timeout=600)
        if os.path.getsize(chunk_path) == 0:
            os.remove(chunk_path)
            return ""
        return chunk_path
    except Exception:
        if chunk_path:
            try:
                os.remove(chunk_path)
            except Exception:
                pass
        return ""


def _warm_up(engine):
    """Run one throwaway inference before reporting ready.

    The first real inference on a freshly loaded backend costs several seconds
    more than the ones after it -- graph construction, kernel compilation and
    lazy imports all land on it. Paying that here means a caller never waits
    for it, and in a live session it no longer blocks the first turn."""
    if not _env_bool("CSGHUB_ASR_WARMUP", True):
        return 0.0
    started = time.monotonic()
    path = ""
    try:
        import wave

        fd, path = tempfile.mkstemp(prefix="csghub-asr-warmup-", suffix=".wav")
        os.close(fd)
        with wave.open(path, "wb") as out:
            out.setnchannels(1)
            out.setsampwidth(2)
            out.setframerate(16000)
            out.writeframes(b"\x00\x00" * 16000)
        engine.transcribe({"file_path": path, "response_format": "json"})
    except Exception as exc:
        # Warming up is an optimisation; a backend that refuses silence still
        # serves real audio.
        print(f"ASR worker warmup skipped: {exc}", file=sys.stderr)
    finally:
        if path:
            try:
                os.remove(path)
            except OSError:
                pass
    return time.monotonic() - started


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


def _transcribe_locked(req):
    with ENGINE_LOCK:
        return ENGINE.transcribe(req)


@app.post("/transcribe")
async def transcribe(request: Request):
    if ENGINE is None:
        raise HTTPException(status_code=503, detail="model is not loaded")
    try:
        req = await request.json()
        if not req.get("file_path"):
            raise HTTPException(status_code=400, detail="file_path is required")
        started = time.monotonic()
        # Off the event loop: a clip takes seconds to transcribe, and running
        # it here would stall /health and every live session on this worker.
        result = await asyncio.to_thread(_transcribe_locked, req)
        print(
            f"ASR worker transcribe inference={time.monotonic() - started:.2f}s",
            flush=True,
        )
        return JSONResponse(result)
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
            # Starlette iterates a sync generator in its threadpool, so this
            # yields without occupying the event loop. The lock covers
            # producing a chunk, not writing it out: holding it across the
            # yield would let a slow reader keep the model for the length of
            # the download.
            chunks = ENGINE.stream_transcribe(req)
            while True:
                with ENGINE_LOCK:
                    try:
                        chunk = next(chunks)
                    except StopIteration:
                        break
                yield json.dumps(chunk, ensure_ascii=False) + "\n"

        return StreamingResponse(generate(), media_type="application/x-ndjson")
    except HTTPException:
        raise
    except Exception as exc:
        print(f"ASR worker transcription error: {exc}", file=sys.stderr)
        raise HTTPException(status_code=500, detail=str(exc)) from exc



class LiveSession:
    """Turns a continuous PCM stream into partial and final transcripts.

    The loaded backends transcribe a finished clip rather than a running
    stream, so segmentation happens here: audio is appended to a buffer as it
    arrives, the tail of that buffer is periodically re-transcribed to produce
    a partial, and the whole buffer is transcribed to finalise a turn. When
    fsmn-vad is enabled a turn also finalises on silence.

    A partial is the current best hypothesis for the segment in progress, not
    an append-only prefix. Clients should treat `completed` as authoritative.

    Every method that runs a model blocks for as long as inference takes and is
    called from a worker thread, never from the event loop: holding the loop
    for seconds stalls /health and the WebSocket handshake of every other
    session on this worker.
    """

    def __init__(self, engine, sample_rate=24000, partial_interval=0.6):
        self.engine = engine
        self.sample_rate = int(sample_rate or 24000)
        self.partial_interval = float(partial_interval)
        # A partial only covers the tail of the buffer. Re-transcribing
        # everything since the last commit makes each partial cost more than
        # the one before it, so on a long turn the cost grows without bound.
        self.partial_window_seconds = _env_float("CSGHUB_ASR_LIVE_PARTIAL_WINDOW_SECONDS", 15.0)
        # A caller that streams continuously without ever committing must not
        # grow the buffer forever; the oldest audio is dropped past this point.
        self.max_buffer_seconds = _env_float("CSGHUB_ASR_LIVE_MAX_BUFFER_SECONDS", 60.0)
        self.partials_enabled = _env_bool("CSGHUB_ASR_LIVE_PARTIALS", True)
        # Audio arrives on the event loop while inference reads the buffer on a
        # worker thread, so every mutation is guarded. The lock is never held
        # across inference -- only across the append or the snapshot.
        self.lock = threading.Lock()
        self.buffer = bytearray()
        self.vad_pending = bytearray()
        self.vad_cache = {}
        self.speaking = False
        self.dropped_bytes = 0
        # Counts audio appended to the turn. A partial over audio that has not
        # changed would only reproduce the last answer, and a session whose
        # caller has gone away -- ICE dead but the socket still open -- would
        # otherwise re-transcribe the same seconds for as long as it lasted.
        self.appends = 0

    # -- buffer -----------------------------------------------------------
    def append(self, pcm):
        """Accept audio. Runs on the event loop, so it only ever appends."""
        cap = self._seconds_to_bytes(self.max_buffer_seconds)
        with self.lock:
            self.buffer.extend(pcm)
            self.vad_pending.extend(pcm)
            self.appends += 1
            if cap and len(self.buffer) > cap:
                drop = len(self.buffer) - cap
                drop -= drop % 2
                del self.buffer[:drop]
                self.dropped_bytes += drop

    def take_vad_chunk(self):
        # With no VAD loaded the audio would only be copied to be thrown away,
        # and that copy is the whole stream every time the pump comes round.
        if getattr(self.engine, "vad_model", None) is None:
            with self.lock:
                self.vad_pending = bytearray()
            return b""
        with self.lock:
            chunk = bytes(self.vad_pending)
            self.vad_pending = bytearray()
        return chunk

    def audio_revision(self):
        """Identifies the current audio, so unchanged audio is not re-run."""
        with self.lock:
            return self.appends if self.buffer else 0

    def _seconds_to_bytes(self, seconds):
        if not seconds or seconds <= 0:
            return 0
        return int(seconds * self.sample_rate) * 2

    # -- audio helpers ----------------------------------------------------
    def _float_samples(self, pcm):
        import numpy as np

        if len(pcm) < 2:
            return np.zeros(0, dtype="float32")
        array = np.frombuffer(bytes(pcm[: len(pcm) // 2 * 2]), dtype="<i2")
        return array.astype("float32") / 32768.0

    def _write_wav(self, samples):
        import tempfile, wave

        import numpy as np

        handle = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
        with wave.open(handle, "wb") as out:
            out.setnchannels(1)
            out.setsampwidth(2)
            out.setframerate(self.sample_rate)
            out.writeframes((np.clip(samples, -1.0, 1.0) * 32767.0).astype("<i2").tobytes())
        handle.close()
        return handle.name

    def _transcribe_samples(self, samples, req):
        import os

        if samples.size < self.sample_rate // 10:  # under 100ms carries no words
            return ""
        path = self._write_wav(samples)
        try:
            payload = dict(req or {})
            payload["file_path"] = path
            with ENGINE_LOCK:
                result = self.engine.transcribe(payload)
            return _clean_text((result or {}).get("text", ""))
        finally:
            try:
                os.unlink(path)
            except OSError:
                pass

    # -- VAD --------------------------------------------------------------
    def vad_events(self, pcm):
        """Feed a chunk to the VAD and report ("start"|"end", offset_ms) pairs.

        Returns an empty list when no VAD model is loaded, in which case
        segmentation falls back to explicit commits from the caller."""
        vad = getattr(self.engine, "vad_model", None)
        if vad is None:
            return []
        samples = self._float_samples(pcm)
        if samples.size == 0:
            return []
        try:
            with ENGINE_LOCK:
                result = vad.generate(
                    input=samples,
                    cache=self.vad_cache,
                    is_final=False,
                    chunk_size=max(10, int(1000 * samples.size / self.sample_rate)),
                )
        except Exception:
            # A VAD failure must not take the session down; fall back to commits.
            return []
        first = result[0] if result else {}
        value = first.get("value", []) if isinstance(first, dict) else []
        events = []
        for item in value:
            if not isinstance(item, (list, tuple)) or len(item) < 2:
                continue
            begin, end = item[0], item[1]
            if begin != -1 and end == -1:
                events.append(("start", begin))
            elif begin == -1 and end != -1:
                events.append(("end", end))
            elif begin != -1 and end != -1:
                events.append(("start", begin))
                events.append(("end", end))
        return events

    # -- model work (blocking; call from a worker thread) -----------------
    def partial(self, req):
        """Best-effort hypothesis for the tail of the turn in progress."""
        window = self._seconds_to_bytes(self.partial_window_seconds)
        with self.lock:
            raw = bytes(self.buffer[-window:] if window and len(self.buffer) > window else self.buffer)
        return self._transcribe_samples(self._float_samples(raw), req)

    def finalize(self, req):
        """Transcribe the turn and start a new one. Returns the final text."""
        with self.lock:
            raw = bytes(self.buffer)
            self.buffer = bytearray()
            self.vad_pending = bytearray()
            self.vad_cache = {}
            self.speaking = False
        return self._transcribe_samples(self._float_samples(raw), req), len(raw)

    def reset(self):
        with self.lock:
            self.buffer = bytearray()
            self.vad_pending = bytearray()
            self.vad_cache = {}
            self.speaking = False


@app.websocket("/transcribe_live")
async def transcribe_live(websocket: WebSocket):
    """Streaming recognition over a WebSocket.

    Binary frames are mono PCM16 at the rate given in the opening JSON frame;
    text frames are control messages ({"type": "commit"|"reset"|"close"}).
    Responses are JSON transcript events.

    Receiving and recognising are deliberately separate: this coroutine only
    buffers audio, and a pump task runs the model in a worker thread. That
    keeps the event loop answering /health and new handshakes while inference
    runs, and it means a model slower than real time drops partials instead of
    building a backlog of audio frames it can never catch up with.
    """
    await websocket.accept()
    if ENGINE is None:
        await websocket.send_json({"kind": "failed", "error": "engine not loaded"})
        await websocket.close()
        return

    session = None
    req = {}
    pump = None
    commits = asyncio.Queue()
    stopping = asyncio.Event()
    # The pump task and this coroutine both send, and two coroutines writing to
    # one WebSocket can interleave their frames.
    sending = asyncio.Lock()

    async def send(event):
        async with sending:
            await websocket.send_json(event)

    async def finalize_turn(committed):
        """End a turn and report exactly one terminal event for it.

        A commit always answers, empty transcript included: a client that only
        ever sees `committed` cannot tell silence from a wedged worker and has
        nothing to wait on but its own timeout."""
        started = time.monotonic()
        try:
            text, audio_bytes = await asyncio.to_thread(session.finalize, req)
        except Exception as exc:
            print(f"ASR worker live transcription error: {exc}", file=sys.stderr)
            await send({"kind": "failed", "error": str(exc)})
        else:
            elapsed = time.monotonic() - started
            audio_seconds = audio_bytes / 2.0 / session.sample_rate
            if text or committed:
                await send({"kind": "completed", "text": text})
            print(
                f"ASR worker live turn audio={audio_seconds:.2f}s inference={elapsed:.2f}s "
                f"chars={len(text)} committed={committed}",
                flush=True,
            )
        if committed:
            await send({"kind": "committed"})

    async def pump_loop():
        # Partials are paced from the end of the previous one. Pacing from the
        # start lets a partial that overruns the interval make the next one due
        # the moment it finishes, which on a slow model is a continuous busy
        # loop that never lets the buffer settle.
        last_partial_end = time.monotonic()
        last_partial_revision = 0
        while not stopping.is_set():
            worked = False

            chunk = session.take_vad_chunk()
            if chunk:
                for kind, _offset in await asyncio.to_thread(session.vad_events, chunk):
                    if kind == "start" and not session.speaking:
                        session.speaking = True
                        await send({"kind": "speech_started"})
                        worked = True
                    elif kind == "end" and session.speaking:
                        session.speaking = False
                        await send({"kind": "speech_stopped"})
                        await finalize_turn(committed=False)
                        last_partial_end = time.monotonic()
                        worked = True

            if not commits.empty():
                commits.get_nowait()
                await finalize_turn(committed=True)
                last_partial_end = time.monotonic()
                continue

            revision = session.audio_revision()
            if (
                session.partials_enabled
                and revision
                and revision != last_partial_revision
                and time.monotonic() - last_partial_end >= session.partial_interval
            ):
                last_partial_revision = revision
                try:
                    text = await asyncio.to_thread(session.partial, req)
                except Exception as exc:
                    # A failed partial is not worth ending the turn over; the
                    # commit that follows still gets its own attempt.
                    print(f"ASR worker live partial error: {exc}", file=sys.stderr)
                    text = ""
                last_partial_end = time.monotonic()
                if text:
                    await send({"kind": "delta", "text": text})
                worked = True

            if not worked:
                await asyncio.sleep(0.05)

    try:
        while True:
            message = await websocket.receive()
            if message.get("type") == "websocket.disconnect":
                break
            if message.get("text") is not None:
                control = json.loads(message["text"])
                kind = control.get("type")
                if kind == "start":
                    if pump is not None:
                        pump.cancel()
                    req = control.get("request") or {}
                    session = LiveSession(
                        ENGINE,
                        sample_rate=control.get("sample_rate") or 24000,
                        partial_interval=control.get("partial_interval") or 0.6,
                    )
                    pump = asyncio.create_task(pump_loop())
                    await send({"kind": "ready"})
                elif kind == "commit" and session is not None:
                    commits.put_nowait(True)
                elif kind == "reset" and session is not None:
                    session.reset()
                    while not commits.empty():
                        commits.get_nowait()
                    await send({"kind": "cleared"})
                elif kind == "close":
                    break
                continue
            data = message.get("bytes")
            if not data or session is None:
                continue
            session.append(data)
    except WebSocketDisconnect:
        pass
    except Exception as exc:
        try:
            await send({"kind": "failed", "error": str(exc)})
        except Exception:
            pass
    finally:
        stopping.set()
        if pump is not None:
            # An inference already running in its thread cannot be interrupted,
            # but nothing waits for it: the task is cancelled and its result
            # discarded so the socket closes now rather than in a minute.
            pump.cancel()
            try:
                await pump
            except (asyncio.CancelledError, Exception):
                pass
        try:
            await websocket.close()
        except Exception:
            pass


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", required=True)
    parser.add_argument("--model-name", required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--hardware", default="cpu")
    args = parser.parse_args()

    global ENGINE
    started = time.monotonic()
    ENGINE = load_engine(args.model_dir, args.model_name, args.hardware)
    loaded = time.monotonic()
    warmed = _warm_up(ENGINE)
    print(
        f"ASR worker ready model={args.model_name} backend={ENGINE.backend} "
        f"port={args.port} pid={os.getpid()} load={loaded - started:.2f}s "
        f"warmup={warmed:.2f}s",
        flush=True,
    )
    uvicorn.run(app, host="127.0.0.1", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()

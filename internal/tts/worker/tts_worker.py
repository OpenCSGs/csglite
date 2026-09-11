"""Local text-to-speech worker.

Speech models are served here rather than through llama.cpp because the vocoder
or codec decoder that turns model output into a waveform only exists in the
model's own inference stack. Mirrors asr_worker.py: one model per process,
FastAPI on a loopback port, JSON in and base64 audio out.
"""

import argparse
import base64
import io
import json
import os
import subprocess
import wave

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse

app = FastAPI()
ENGINE = None

DEFAULT_SAMPLE_RATE = 24000
# Containers ffmpeg produces from raw PCM. wav and pcm are written directly.
FFMPEG_FORMATS = {
    "mp3": ("mp3", "libmp3lame"),
    "opus": ("opus", "libopus"),
    "flac": ("flac", "flac"),
    "aac": ("adts", "aac"),
}


def _load_config(model_dir):
    path = os.path.join(model_dir, "config.json")
    if not os.path.exists(path):
        return {}
    try:
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    except Exception:
        return {}


def _architectures(cfg):
    return [str(a) for a in (cfg.get("architectures") or [])]


def _is_kokoro_model(model_dir, model_name):
    cfg = _load_config(model_dir)
    if "istftnet" in cfg or "plbert" in cfg:
        return True
    return "kokoro" in str(model_name).lower()


def _transformers_tts_class(cfg):
    archs = " ".join(_architectures(cfg))
    for marker, cls in (
        ("VitsModel", "VitsModel"),
        ("SpeechT5ForTextToSpeech", "SpeechT5ForTextToSpeech"),
        ("BarkModel", "BarkModel"),
        ("ParlerTTSForConditionalGeneration", "ParlerTTSForConditionalGeneration"),
        ("CsmForConditionalGeneration", "CsmForConditionalGeneration"),
        ("DiaForConditionalGeneration", "DiaForConditionalGeneration"),
        ("FastSpeech2Conformer", "FastSpeech2ConformerWithHifiGan"),
    ):
        if marker in archs:
            return cls
    return None


def _is_qwen3_tts_model(model_dir):
    cfg = _load_config(model_dir)
    if str(cfg.get("model_type", "")).lower() == "qwen3_tts":
        return True
    return any("Qwen3TTS" in a for a in _architectures(cfg))


def _has_audio_span_tokens(model_dir):
    """A causal LM with a matched pair of audio span tokens emits audio codec
    tokens. Detecting it lets us explain why it cannot be synthesised rather
    than producing silence."""
    starts = ("<|start_of_audio|>", "<|begin_of_audio|>", "<|start_of_speech|>", "<|begin_of_speech|>")
    ends = ("<|end_of_audio|>", "<|end_of_speech|>")
    start = end = False
    for name in ("added_tokens.json", "special_tokens_map.json", "tokenizer_config.json"):
        path = os.path.join(model_dir, name)
        if not os.path.exists(path):
            continue
        try:
            with open(path, "r", encoding="utf-8") as handle:
                blob = handle.read().lower()
        except Exception:
            continue
        start = start or any(token in blob for token in starts)
        end = end or any(token in blob for token in ends)
    return start and end


def _device(hardware):
    hardware = (hardware or "").lower()
    if "cuda" in hardware:
        return "cuda"
    if "mps" in hardware or "metal" in hardware:
        return "mps"
    return "cpu"


def _ffmpeg_exe():
    exe = os.getenv("CSGHUB_FFMPEG")
    if exe and os.path.exists(exe):
        return exe
    try:
        import imageio_ffmpeg

        return imageio_ffmpeg.get_ffmpeg_exe()
    except Exception:
        return "ffmpeg"


def _pcm16_bytes(samples):
    """Clamp float samples in [-1, 1] to signed 16-bit little-endian PCM."""
    import numpy as np

    array = np.asarray(samples, dtype="float32").reshape(-1)
    array = np.clip(array, -1.0, 1.0)
    return (array * 32767.0).astype("<i2").tobytes()


def _wav_bytes(pcm, sample_rate):
    buffer = io.BytesIO()
    with wave.open(buffer, "wb") as handle:
        handle.setnchannels(1)
        handle.setsampwidth(2)
        handle.setframerate(sample_rate)
        handle.writeframes(pcm)
    return buffer.getvalue()


def _ffmpeg_args(fmt, sample_rate):
    container, codec = FFMPEG_FORMATS[fmt]
    return [
        _ffmpeg_exe(), "-hide_banner", "-loglevel", "error",
        "-f", "s16le", "-ar", str(sample_rate), "-ac", "1", "-i", "pipe:0",
        "-c:a", codec, "-f", container, "pipe:1",
    ]


def _encode(pcm, sample_rate, fmt):
    if fmt == "pcm":
        return pcm
    if fmt == "wav":
        return _wav_bytes(pcm, sample_rate)
    if fmt not in FFMPEG_FORMATS:
        raise ValueError("unsupported response_format: %s" % fmt)
    proc = subprocess.run(
        _ffmpeg_args(fmt, sample_rate), input=pcm,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    if proc.returncode != 0:
        raise RuntimeError("ffmpeg failed: %s" % proc.stderr.decode("utf-8", "replace")[:400])
    return proc.stdout


class KokoroEngine:
    """Kokoro ships its own weights, config and voice files, so it needs nothing
    beyond the kokoro package."""

    backend = "kokoro"
    streaming = True

    def __init__(self, model_dir, model_name, hardware):
        from kokoro import KModel, KPipeline

        self.model_dir = model_dir
        weights = None
        for candidate in sorted(os.listdir(model_dir)):
            if candidate.endswith(".pth"):
                weights = os.path.join(model_dir, candidate)
                break
        if weights is None:
            raise RuntimeError("no .pth weights found in %s" % model_dir)
        self.sample_rate = 24000
        self.model = KModel(
            config=os.path.join(model_dir, "config.json"), model=weights,
        ).to(_device(hardware)).eval()
        self._pipelines = {}
        self._KPipeline = KPipeline
        self.voices = self._discover_voices()

    def _discover_voices(self):
        voices = []
        voice_dir = os.path.join(self.model_dir, "voices")
        if os.path.isdir(voice_dir):
            for entry in sorted(os.listdir(voice_dir)):
                if not entry.endswith(".pt"):
                    continue
                vid = entry[:-3]
                voices.append({
                    "id": vid,
                    "language": self._language_of(vid),
                    "gender": {"f": "female", "m": "male"}.get(vid[1:2], ""),
                })
        return voices

    @staticmethod
    def _language_of(voice_id):
        # Kokoro voice ids are prefixed with a language letter, e.g. zf_xiaobei.
        return {
            "a": "en-US", "b": "en-GB", "z": "zh", "j": "ja",
            "e": "es", "f": "fr", "h": "hi", "i": "it", "p": "pt-BR",
        }.get(voice_id[:1], "")

    def _pipeline(self, voice):
        lang = (voice or "a")[:1]
        if lang not in self._pipelines:
            self._pipelines[lang] = self._KPipeline(
                lang_code=lang, model=self.model, repo_id=None,
            )
        return self._pipelines[lang]

    def default_voice(self):
        return self.voices[0]["id"] if self.voices else "af_heart"

    def iter_pcm(self, text, voice, speed, instruct=None):
        voice = voice or self.default_voice()
        pipeline = self._pipeline(voice)
        voice_path = os.path.join(self.model_dir, "voices", voice + ".pt")
        if not os.path.exists(voice_path):
            raise ValueError("unknown voice: %s" % voice)
        for result in pipeline(text, voice=voice_path, speed=speed):
            audio = getattr(result, "audio", None)
            if audio is None and isinstance(result, (tuple, list)):
                audio = result[-1]
            if audio is None:
                continue
            if hasattr(audio, "detach"):
                audio = audio.detach().cpu().numpy()
            yield _pcm16_bytes(audio)


class QwenTTSEngine:
    """Official Qwen3-TTS. The repository is self-contained: the codec that turns
    generated tokens into a waveform ships inside it as speech_tokenizer/, so no
    second download is needed.

    Preset speakers come from config.talker_config.spk_id rather than a
    hard-coded list, so every variant reports its own set."""

    backend = "qwen3-tts"
    # generate_* returns the whole waveform, so audio is produced in one piece.
    streaming = False

    def __init__(self, model_dir, model_name, hardware):
        import torch
        from qwen_tts import Qwen3TTSModel

        cfg = _load_config(model_dir)
        talker = cfg.get("talker_config") or {}
        self.spk_ids = list((talker.get("spk_id") or {}).keys())
        self.dialects = talker.get("spk_is_dialect") or {}
        self.model_type = str(cfg.get("tts_model_type") or "").lower()
        self.model_name = model_name
        self.sample_rate = 24000

        device = _device(hardware)
        dtype = torch.float32 if device == "cpu" else torch.bfloat16
        self.model = Qwen3TTSModel.from_pretrained(model_dir, device_map=device, dtype=dtype)
        self.voices = [
            {
                "id": self._display(spk),
                "language": self._language_of(spk),
                "label": self._label_of(spk),
            }
            for spk in self.spk_ids
        ]

    @staticmethod
    def _display(spk):
        # config stores lower case ids; the model card presents them capitalised
        # (Uncle_Fu, Ono_Anna), and the model accepts either.
        return "_".join(part.capitalize() for part in spk.split("_"))

    def _language_of(self, spk):
        dialect = self.dialects.get(spk)
        if isinstance(dialect, str) and dialect:
            return "zh-" + dialect.replace("_dialect", "")
        return ""

    def _label_of(self, spk):
        dialect = self.dialects.get(spk)
        if isinstance(dialect, str) and dialect:
            return dialect.replace("_", " ")
        return ""

    def default_voice(self):
        return self._display(self.spk_ids[0]) if self.spk_ids else ""

    def iter_pcm(self, text, voice, speed, instruct=None):
        if self.model_type != "custom_voice" or not self.spk_ids:
            raise ValueError(
                "model %s is a Qwen3-TTS voice-cloning checkpoint, which needs reference "
                "audio rather than a preset voice; use a CustomVoice checkpoint for "
                "/v1/audio/speech" % self.model_name
            )
        voice = voice or self.default_voice()
        if voice.lower() not in {spk.lower() for spk in self.spk_ids}:
            raise ValueError(
                "unknown voice: %s (available: %s)"
                % (voice, ", ".join(self._display(s) for s in self.spk_ids))
            )
        kwargs = {"text": text, "speaker": voice}
        if instruct:
            kwargs["instruct"] = instruct
        wavs, _ = self.model.generate_custom_voice(**kwargs)
        yield _pcm16_bytes(wavs[0])


class TransformersEngine:
    """Models whose whole pipeline, vocoder included, is implemented in
    transformers."""

    backend = "transformers"
    streaming = False

    def __init__(self, model_dir, model_name, hardware, class_name):
        import transformers

        self.model_dir = model_dir
        self.class_name = class_name
        self.device = _device(hardware)
        self.processor = transformers.AutoProcessor.from_pretrained(model_dir)
        self.model = getattr(transformers, class_name).from_pretrained(model_dir).to(self.device).eval()
        self.sample_rate = int(
            getattr(getattr(self.model, "config", None), "sampling_rate", 0)
            or getattr(self.processor, "sampling_rate", 0)
            or DEFAULT_SAMPLE_RATE
        )
        self.voices = []

    def default_voice(self):
        return ""

    def iter_pcm(self, text, voice, speed, instruct=None):
        import torch

        inputs = self.processor(text=text, return_tensors="pt").to(self.device)
        with torch.no_grad():
            output = self.model(**inputs) if self.class_name == "VitsModel" else self.model.generate(**inputs)
        waveform = getattr(output, "waveform", output)
        if hasattr(waveform, "detach"):
            waveform = waveform.detach().cpu().numpy()
        yield _pcm16_bytes(waveform)


def load_engine(model_dir, model_name, hardware):
    cfg = _load_config(model_dir)
    if _is_qwen3_tts_model(model_dir):
        return QwenTTSEngine(model_dir, model_name, hardware)
    if _is_kokoro_model(model_dir, model_name):
        return KokoroEngine(model_dir, model_name, hardware)
    class_name = _transformers_tts_class(cfg)
    if class_name:
        return TransformersEngine(model_dir, model_name, hardware, class_name)
    if _has_audio_span_tokens(model_dir):
        raise RuntimeError(
            "model %s generates audio codec tokens but ships no codec decoder, and the "
            "repository does not record which codec produced them; audio cannot be "
            "reconstructed from its output" % model_name
        )
    raise RuntimeError("unsupported text-to-speech model: %s" % model_name)


def _request_params(payload, engine):
    text = (payload.get("input") or "").strip()
    if not text:
        raise ValueError("input is required")
    fmt = (payload.get("response_format") or "mp3").lower()
    if fmt not in FFMPEG_FORMATS and fmt not in ("wav", "pcm"):
        raise ValueError("unsupported response_format: %s" % fmt)
    speed = payload.get("speed")
    speed = 1.0 if speed in (None, 0) else float(speed)
    if not 0.25 <= speed <= 4.0:
        raise ValueError("speed must be between 0.25 and 4.0")
    rate = int(payload.get("sample_rate") or 0) or engine.sample_rate
    # instructions are style guidance; backends that cannot use them ignore it.
    instruct = (payload.get("instructions") or "").strip() or None
    return text, payload.get("voice") or "", fmt, speed, rate, instruct


@app.get("/health")
async def health():
    if ENGINE is None:
        return JSONResponse({"error": "engine not loaded"}, status_code=503)
    return {
        "sample_rate": ENGINE.sample_rate,
        "streaming": ENGINE.streaming,
        "backend": ENGINE.backend,
        "voices": getattr(ENGINE, "voices", []),
    }


@app.post("/speak")
async def speak(request: Request):
    payload = await request.json()
    try:
        text, voice, fmt, speed, rate, instruct = _request_params(payload, ENGINE)
        pcm = b"".join(ENGINE.iter_pcm(text, voice, speed, instruct))
        data = _encode(pcm, rate, fmt)
    except ValueError as exc:
        return JSONResponse({"error": str(exc)}, status_code=400)
    except Exception as exc:  # surfaced to the client as a 500 with the reason
        return JSONResponse({"error": str(exc)}, status_code=500)
    return {
        "audio": base64.b64encode(data).decode("ascii"),
        "format": fmt,
        "sample_rate": rate,
    }


@app.post("/speak_stream")
async def speak_stream(request: Request):
    payload = await request.json()
    try:
        text, voice, fmt, speed, rate, instruct = _request_params(payload, ENGINE)
    except ValueError as exc:
        return JSONResponse({"error": str(exc)}, status_code=400)

    def frame(audio=b"", done=False, error=None):
        body = {"done": done}
        if audio:
            body["audio"] = base64.b64encode(audio).decode("ascii")
        if error:
            body["error"] = error
        return json.dumps(body) + "\n"

    def generate():
        # Raw containers are emitted as they are produced. Compressed containers
        # are piped through one long-lived ffmpeg so the client receives a single
        # continuous stream rather than concatenated files.
        try:
            if fmt in ("pcm", "wav"):
                first = True
                for pcm in ENGINE.iter_pcm(text, voice, speed, instruct):
                    if fmt == "wav" and first:
                        yield frame(_wav_bytes(pcm, rate))
                        first = False
                    else:
                        yield frame(pcm)
                yield frame(done=True)
                return
            proc = subprocess.Popen(
                _ffmpeg_args(fmt, rate),
                stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            )
            try:
                for pcm in ENGINE.iter_pcm(text, voice, speed, instruct):
                    proc.stdin.write(pcm)
                    proc.stdin.flush()
                proc.stdin.close()
                while True:
                    block = proc.stdout.read(8192)
                    if not block:
                        break
                    yield frame(block)
            finally:
                if proc.stdin and not proc.stdin.closed:
                    proc.stdin.close()
                proc.wait()
            if proc.returncode != 0:
                yield frame(error="ffmpeg failed: %s" % proc.stderr.read().decode("utf-8", "replace")[:400])
                return
            yield frame(done=True)
        except Exception as exc:
            yield frame(error=str(exc))

    return StreamingResponse(generate(), media_type="application/x-ndjson")


def main():
    global ENGINE
    parser = argparse.ArgumentParser()
    parser.add_argument("--model-dir", required=True)
    parser.add_argument("--model-name", required=True)
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--hardware", default="cpu")
    args = parser.parse_args()

    ENGINE = load_engine(args.model_dir, args.model_name, args.hardware)

    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()

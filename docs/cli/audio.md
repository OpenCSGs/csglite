# 语音推理

先运行 `csghub-lite start`。文件转录与语音合成默认使用本地模型。用 `--source` 指定服务端支持该操作的其他来源。

```sh
csghub-lite audio transcribe ASR_MODEL recording.wav --language zh
csghub-lite audio transcribe ASR_MODEL recording.wav --source cloud --json
csghub-lite audio speak TTS_MODEL '你好' --format wav --output hello.wav
```

转录默认向标准输出写入识别文本，`--json` 保留完整响应。合成命令接受标准输入或文本参数，要求输出到新文件。用 `--output -` 可以把音频字节交给管道中的播放器。

## 实时会话

`audio realtime` 通过 WebSocket 收发协议事件，可用于处理预录音频或对接采集程序。标准输入和标准输出均可使用 JSON Lines。下面的示例只更新会话并在收到确认后退出。

```sh
echo '{"type":"session.update","session":{"instructions":"用中文回答"}}' | csghub-lite audio realtime --until session.updated
```

实时转录时，先将录音转成默认格式（24 kHz 单声道、16 位小端 PCM）。下例把一段短录音编码成一条音频事件，提交后等待最终转录。较长的音频应分块发送多条 `input_audio_buffer.append`。

```sh
python3 -c 'import base64,json,sys; print(json.dumps({"type":"input_audio_buffer.append","audio":base64.b64encode(sys.stdin.buffer.read()).decode()})); print(json.dumps({"type":"input_audio_buffer.commit"}))' < clip.pcm > turn.jsonl
csghub-lite audio realtime --transcription --model ASR_MODEL --events turn.jsonl
```

`--session FILE` 接受初始会话配置。默认在第一轮 `response.done` 或转录完成时退出，`--until` 可指定其他终止事件，`--timeout` 默认五分钟。服务端报错或会话超时会返回非零退出码。事件格式见[实时音频 API](../guides/realtime-audio-api.md)。

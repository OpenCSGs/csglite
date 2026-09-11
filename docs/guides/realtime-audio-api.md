# 实时语音 API 设计（ASR + TTS 全双工，兼容 OpenAI Realtime）

- 状态：**P0、P1、P2 已实现**（P3 WebRTC 未实现），对应 issue [#147](https://github.com/OpenCSGs/csglite/issues/147)
- 范围：`/v1/audio/speech`、`/v1/realtime*`、本地 TTS 运行时、以及配套的 `/api/*` 管理面
- 兼容目标：OpenAI Realtime API（WebRTC 与 WebSocket 两种传输）+ OpenAI `audio/speech`、`audio/transcriptions`

## 1. 现状调研

### 1.1 已有能力

| 能力 | 位置 | 说明 |
| --- | --- | --- |
| 文件式 ASR | `internal/server/handlers_audio.go`、`POST /v1/audio/transcriptions` | multipart 上传，支持 `response_format=json/verbose_json/text`，支持 `stream=true` 的**私有** SSE（`{text,response,done}`） |
| 本地 ASR 运行时 | `internal/asr`（`PythonEngine` + 内嵌 `worker/asr_worker.py`） | 独立 Python venv 起 FastAPI worker，暴露 `GET /health`、`POST /transcribe`、`POST /transcribe_stream`；后端覆盖 Whisper（transformers）、FunASR/SenseVoice、Qwen3-ASR、GLM-ASR |
| VAD | ASR worker 内 `fsmn-vad`（`CSGHUB_ASR_VAD_MODEL`） | 目前只用于长音频切段 |
| 运行时安装管理 | `internal/imagegen/runtime.go` + `/api/asr-runtime`、`/api/image-runtime`、`/api/embedding-runtime` | 每类运行时一套 venv/包清单/状态与安装接口 |
| 云端回退 | `handlers_audio.go` 的 `audioTranscriptionCanFallbackToCloud` | 本地引擎起不来时按 `source` 回退 csghub |
| 模型分类 | `internal/server/pipeline_tags.go` | 已有 `text_to_speech` / `speech_recognition` 两个分类 |
| WebSocket 依赖 | `github.com/gorilla/websocket`（已在 `go.mod`） | 已被 `/api/apps/shell/{id}/ws` 使用，可直接复用 |

### 1.2 缺口

本 issue 本质是**新增功能**：除了「文件式 ASR」这一项，其余全部需要从零建设 —— TTS 运行时、
实时会话层、WebRTC 传输三块是全新代码，流式 ASR 需要扩展现有接口。下面第 2 项（TTS 模型被误
路由）不是本 issue 的主线，只是 issue 末尾那句抱怨的成因，其意义在于「在真正的 TTS 运行时做
出来之前，先掐掉 UI 上那个『能跑』的假象」，故列入 P0 而非功能主体。

1. **没有任何 TTS 推理路径**：仓库里 `text-to-speech` 只出现在分类与第三方 provider 规则表（`provider_pipeline_rules_gen.go` 的 `Mode: "audio_speech"`），本地侧完全没有实现。
2. **TTS 模型被误路由到 llama.cpp** —— 这就是 issue 最后一段所指的根因。`internal/localinference/support.go` 的 `FromLocalModel` 依次判断 diffusers → ASR → `unsupportedPipelineTag`，而 `text-to-speech` 既不在 ASR 分支也不在不支持列表里，于是落到 `llamaSupport()`；safetensors + `Qwen3ForCausalLM` 命中 `convert` 模式，被当作普通文本模型转 GGUF 交给 llama.cpp 做文本生成，声码器（vocoder / flow-matching decoder）整段丢失。
3. **`/v1/realtime*` 与 `/v1/audio/speech` 未注册**，且行为不一致（已实测确认）：
   - `POST /v1/audio/speech`、`POST /v1/realtime/calls` → 404（`routes.go` 只注册了 `GET /`）。
   - `GET /v1/realtime`、`GET /v1/audio/transcriptions/realtime` → 命中静态兜底
     `mux.Handle("GET /", staticHandler())`，返回 **`200` + `text/html`** 的 Web UI `index.html`。
     issue 里记的「返回 404」对 GET 端点其实过于乐观：客户端拿到的是一个成功状态码和一页 HTML，
     比 404 更难排查。`handlers_responses.go` 为 `GET /v1/responses` 打过同样的补丁（返回 426），
     说明这是已知坑，新端点必须显式注册。
4. **ASR 引擎接口不支持推流**：`asr.Engine.TranscribeStream` 的入参仍是 `FilePath`，只能对已落盘文件按 VAD 切段，无法接受麦克风连续帧、也没有"部分结果 / 最终结果"事件模型。
5. **没有会话概念**：现有音频接口都是一次性请求，没有 session、没有轮次（turn）状态机、没有打断（barge-in）语义。

### 1.3 硬约束（影响方案选择）

- **必须无 CGO**：`db332be fix(release): build release binaries without cgo`。因此 WebRTC 栈只能用纯 Go 的 `pion/webrtc`；Opus **编码**不能用 `libopus` 的 cgo 绑定。
- **三平台**：macOS / Linux / Windows 都要跑（`docs/agent-guidelines/cross-platform.md`）。
- **API 是用户契约**：`docs/agent-guidelines/api-swagger-sync.md` 要求同任务更新 `openapi/local-api.json`，并保持既有路由/字段/错误形状向后兼容 —— 所以现有 `/v1/audio/transcriptions` 的私有 SSE 格式**必须保留为默认**。
- **运行时文件都在存储根**（默认 `~/.csghub-lite`）。
- **config.json 不允许出现重叠的可写来源**（`docs/agent-guidelines/config-schema.md`）。

## 2. 总体架构

分三层，让传输和能力解耦 —— 这是能同时低成本支持 WebRTC / WebSocket / 纯 HTTP 的关键：

```
┌───────────────────────── 传输适配层 ─────────────────────────┐
│ WebRTC (pion)        WebSocket (gorilla)      HTTP/SSE      │
│ SDP + RTP + oai-events   JSON 帧              一次性请求    │
└───────────────┬─────────────────┬───────────────┬───────────┘
                └────────── 统一事件/会话层 ───────┘
        internal/realtime: Session 状态机、轮次管理、
        server_vad、打断语义、事件编解码（传输无关）
                              │
        ┌─────────────────────┼─────────────────────┐
   internal/asr          internal/vad          internal/tts (新增)
   流式 ASR 引擎         语音起止检测          python-tts worker + 声码器
        └──────── 可选 internal/inference（LLM，用于 ASR→LLM→TTS）
```

要点：

- **事件协议只实现一次**。WebRTC 与 WebSocket 的差异被压缩成"音频走媒体轨还是走 base64 事件"这一个开关。
- **音频编解码归属**：上行 Opus→PCM16 用纯 Go 的 `pion/opus` 解码；下行 PCM16→Opus **在 Python TTS worker 里编码**（worker 侧有 `soundfile`/`imageio-ffmpeg`，可加 `PyAV`/`opuslib` wheel），Go 只做 RTP 打包（`TrackLocalStaticSample` 直接吃已编码的 20ms Opus 帧）。这样绕开"纯 Go 没有可用 Opus 编码器"的问题，且不引入 CGO。
- **csglite 与 OpenAI 的本质差异**：OpenAI 是单个 `gpt-realtime` 模型端到端；csglite 是 **ASR + (可选 LLM) + TTS 三个本地模型的编排**。因此会话对象需要一处能分别指定三个模型 —— 见 §4.2。

## 3. HTTP 端点设计（非 WebRTC 回退，优先落地）

### 3.1 `POST /v1/audio/speech`

OpenAI 兼容的一次性语音合成。

请求（`application/json`）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model` | string，必填 | 本地 TTS 模型 ID（`pipeline_tag=text-to-speech`） |
| `input` | string，必填 | 待合成文本 |
| `voice` | string | 音色；缺省取该模型的默认音色（见 §5.3） |
| `response_format` | enum | `mp3`（默认，兼容 OpenAI）、`wav`、`pcm`、`opus`、`flac`、`aac` |
| `speed` | number | 0.25–4.0，默认 1.0 |
| `instructions` | string | 风格/情感提示，模型不支持时忽略 |
| `stream_format` | enum | `audio`（默认，chunked 二进制边合成边发）、`sse`（OpenAI 的 `speech.audio.delta` / `speech.audio.done` 事件） |
| `sample_rate` | int | csglite 扩展；`pcm`/`wav` 时指定采样率，默认取模型原生（常见 24000） |
| `source` | string | 复用现有路由语义：`local` / `csghub` / provider ID / pool ID |

响应：`200`，`Content-Type` 按 `response_format`（`audio/mpeg`、`audio/wav`、`audio/L16;rate=24000`、`audio/opus` …）。错误沿用 `writeOpenAIError` 形状。

### 3.2 `POST /v1/audio/transcriptions`（向后兼容地扩展）

- 现有 multipart 字段与私有 SSE 格式**保持不变**（默认 `stream_format=csglite`）。
- 新增可选字段 `stream_format=openai`，输出 OpenAI 官方事件：`transcript.text.delta` / `transcript.text.done`，便于 SDK 直接消费。
- 新增可选字段 `timestamp_granularities[]`（`segment` / `word`），`verbose_json` 下填充 `segments`。

### 3.3 显式的"未实现"应答（Phase 0 即可落地）

在实现完成前，为下列路径注册显式 stub，返回 `501` + OpenAI 错误体，避免 GET 请求拿到 Web UI 的 HTML：

```
POST /v1/audio/speech
POST /v1/realtime/calls
GET  /v1/realtime
GET  /v1/realtime/transcription
GET  /v1/audio/transcriptions/realtime
```

## 4. Realtime 会话设计

### 4.1 端点总览

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| `POST` | `/v1/realtime/client_secrets` | 签发短期 ephemeral key（`ek_*`），供浏览器直连，不暴露长期 API key |
| `POST` | `/v1/realtime/calls` | **WebRTC**：提交 SDP offer，返回 SDP answer |
| `DELETE` | `/v1/realtime/calls/{call_id}` | 挂断某路 WebRTC 通话 |
| `GET`(WS) | `/v1/realtime` | **WebSocket**：全双工会话（`?model=`） |
| `GET`(WS) | `/v1/realtime/transcription` | WebSocket，`session.type=transcription`（只做 ASR） |
| `GET`(WS) | `/v1/audio/transcriptions/realtime` | 上一条的别名，兼容 issue 中列出的客户端路径 |
| `GET` | `/api/realtime/sessions` | 管理面：当前活跃会话（模型、传输、时长、轮次数） |
| `DELETE` | `/api/realtime/sessions/{id}` | 管理面：强制关闭会话 |

鉴权沿用 `apiAuthMiddleware`：loopback 免鉴权；远程需 `Authorization: Bearer`。WebSocket 额外接受浏览器无法设置 header 的两种方式：子协议 `openai-insecure-api-key.<key>`，或 ephemeral key 作为 query 参数。

### 4.2 会话对象

以 OpenAI GA 的 session 结构为基线，**只用附加字段**表达 csglite 的多模型编排：

```json
{
  "type": "realtime",
  "model": "Qwen/Qwen3-4B-Instruct",
  "instructions": "你是一个简洁的语音助手。",
  "output_modalities": ["audio"],
  "audio": {
    "input": {
      "format": { "type": "audio/pcm", "rate": 16000 },
      "turn_detection": {
        "type": "server_vad",
        "threshold": 0.5,
        "prefix_padding_ms": 300,
        "silence_duration_ms": 500,
        "create_response": true,
        "interrupt_response": true
      },
      "transcription": {
        "model": "iic/SenseVoiceSmall",
        "language": "zh",
        "prompt": "",
        "hotwords": ["csglite"],
        "itn": true
      }
    },
    "output": {
      "format": { "type": "audio/pcm", "rate": 24000 },
      "model": "FunAudioLLM/CosyVoice2-0.5B",
      "voice": "zh-female-1",
      "speed": 1.0
    }
  },
  "tools": [],
  "tool_choice": "auto",
  "x_csglite": {
    "pipeline": "asr_llm_tts",
    "asr_source": "local",
    "tts_source": "local",
    "vad_model": "fsmn-vad"
  }
}
```

模型解析规则：

- `audio.input.transcription.model` → ASR 模型（csglite 语义扩展：OpenAI 这里只接受固定几个模型名，我们接受任意本地 ASR 模型 ID）。
- `audio.output.model` → TTS 模型（**csglite 附加字段**；OpenAI GA 无此字段，缺省时取"已加载的唯一 TTS 模型"或配置里的默认值）。
- 顶层 `model` → 会话 LLM。**留空即为纯 ASR+TTS 双工模式**（`x_csglite.pipeline` 会被推断为 `asr_tts`），服务端不自作主张生成回复，只按客户端下发的 `response.create` 念文本 —— 这正是 issue 里客户端期望的形态（LLM 在客户端侧）。
- `x_csglite.pipeline` 可显式取 `asr_only` / `asr_tts` / `asr_llm_tts`，与上面的推断冲突时以显式值为准并回 `400`。

### 4.3 事件协议

客户端 → 服务端：

| 事件 | 说明 |
| --- | --- |
| `session.update` | 会话中改配置（切音色、改 VAD 阈值、改语言） |
| `input_audio_buffer.append` | WebSocket 传输下的上行音频（base64 PCM16）；WebRTC 下不用 |
| `input_audio_buffer.commit` | 手动结束一轮（`turn_detection: null` 时必需） |
| `input_audio_buffer.clear` | 丢弃未提交的上行缓冲 |
| `conversation.item.create` | 注入文本消息（如客户端侧 LLM 的结果） |
| `response.create` | 请求生成/合成 |
| `response.cancel` | 取消进行中的 LLM / TTS 生成 |
| `output_audio_buffer.clear` | **WebRTC 专有**：立刻丢弃已排队未发送的音频帧，停止说话 |

服务端 → 客户端：

| 事件 | 传输 | 说明 |
| --- | --- | --- |
| `session.created` / `session.updated` | 全部 | 连接建立、配置生效 |
| `error` | 全部 | 统一错误体（`{type,code,message,event_id}`） |
| `input_audio_buffer.speech_started` / `.speech_stopped` | 全部 | server_vad 判定的语音起止 |
| `input_audio_buffer.committed` / `.cleared` | 全部 | 轮次已提交 / 缓冲已清 |
| `conversation.item.created` | 全部 | 新条目入会话 |
| `conversation.item.input_audio_transcription.delta` | 全部 | **ASR 增量**（issue 第 7 条） |
| `conversation.item.input_audio_transcription.completed` | 全部 | **ASR 最终结果**（issue 第 7 条） |
| `conversation.item.input_audio_transcription.failed` | 全部 | 该轮识别失败，会话继续 |
| `response.created` | 全部 | 一次生成开始 |
| `response.output_item.added` / `.done` | 全部 | 输出条目 |
| `response.output_audio_transcript.delta` / `.done` | 全部 | TTS 文本对齐字幕 |
| `response.output_audio.delta` / `.done` | **仅 WS** | base64 PCM16 音频块；WebRTC 下音频走媒体轨，不发这两个事件 |
| `output_audio_buffer.started` | **仅 WebRTC** | 开始向下行轨推流 |
| `output_audio_buffer.stopped` | **仅 WebRTC** | 音频播完（issue 第 8 条） |
| `output_audio_buffer.cleared` | **仅 WebRTC** | 被 `output_audio_buffer.clear` 打断（issue 第 9 条） |
| `response.done` | 全部 | 一次生成结束，带 usage（issue 第 8 条） |

纯 ASR+TTS 模式下的"念这段文本"：

```json
{ "type": "response.create",
  "response": { "output_modalities": ["audio"], "instructions": "今天天气不错" } }
```

会话没有配置 LLM 时，`response.instructions`（或 `response.input` 中最后一条文本内容）被直接送 TTS；配置了 LLM 时，`instructions` 保持 OpenAI 原义（本轮系统提示），文本由 LLM 生成。

打断语义（必须区分，两者常被混用）：

- `response.cancel` → 停止 LLM 解码与 TTS 合成，回 `response.done`（`status: "cancelled"`）；**已经发出的音频不撤回**。
- `output_audio_buffer.clear` → 只丢弃服务端已排队未发送的音频帧，回 `output_audio_buffer.cleared`。
- `turn_detection.interrupt_response: true` 时，检测到用户抢话（`speech_started`）自动等价于先 `response.cancel` 再 `output_audio_buffer.clear`。

### 4.4 WebRTC 握手：`POST /v1/realtime/calls`

同时支持两种请求形态：

**形态 A（issue 客户端首选，multipart）**

```
POST /v1/realtime/calls
Accept: application/sdp
Content-Type: multipart/form-data; boundary=...

--...
Content-Disposition: form-data; name="sdp"

v=0 ... (SDP offer)
--...
Content-Disposition: form-data; name="session"
Content-Type: application/json

{ "type": "realtime", "audio": { ... } }
--...--
```

**形态 B（OpenAI ephemeral 模式，裸 SDP）**

```
POST /v1/realtime/calls?model=<id>&session=<urlencoded-json>
Authorization: Bearer ek_...
Content-Type: application/sdp
Accept: application/sdp

v=0 ... (SDP offer)
```

响应：

```
200 OK
Content-Type: application/sdp
Location: /v1/realtime/calls/rtc_7f3a...

v=0 ... (SDP answer)
```

媒体与数据通道约定：

- 一条上行音频轨（客户端麦克风 → ASR）+ 一条下行音频轨（TTS → 客户端播放），`sendrecv`。
- 编解码协商顺序：`opus/48000/2`（首选，`useinbandfec=1`、20ms ptime）→ `PCMU/8000` 回退（浏览器普遍支持，纯 Go 编码代价极低，音质降级但保证可用）。
- DataChannel 名称固定 `oai-events`，`ordered: true`，负载为 UTF-8 JSON，一帧一事件。
- ICE：默认只收集本机 host candidate（本地部署无需 STUN/TURN），UDP 端口范围可配；同时启用 ICE-TCP 以应对禁 UDP 的环境。
- `DELETE /v1/realtime/calls/{call_id}` 或 DataChannel 关闭 → 关闭 PeerConnection、释放 ASR/TTS 会话资源。

### 4.5 WebSocket 传输

`GET /v1/realtime?model=<id>` 升级为 WebSocket，事件与 §4.3 完全一致，差异只有：

- 上行音频用 `input_audio_buffer.append`（base64 PCM16，建议 20–100ms 一帧）。
- 下行音频用 `response.output_audio.delta`。
- 不产生 `output_audio_buffer.*` 事件；打断用 `response.cancel` + `input_audio_buffer.clear`。

`GET /v1/realtime/transcription`（及别名 `/v1/audio/transcriptions/realtime`）等价于 `session.type=transcription`：只做流式 ASR，不接受 `response.create`。

## 5. 能力层设计

### 5.1 流式 ASR：扩展 `asr.Engine`

现有接口只能吃文件，需要附加一个流式接口（保持 `Engine` 不变，避免破坏现有调用）：

```go
// internal/asr
type StreamingEngine interface {
	Engine
	OpenStream(ctx context.Context, cfg StreamConfig) (Stream, error)
}

type StreamConfig struct {
	SampleRate int    // 16000
	Language   string
	Hotwords   []string
	ITN        bool
	Partials   bool  // 是否发 delta
}

type Stream interface {
	Write(pcm16 []byte) error       // 连续帧，单声道
	Commit() error                  // 强制切轮，产出 completed
	Events() <-chan StreamEvent     // {Type: delta|completed|failed, Text, ...}
	Close() error
}
```

worker 侧新增 `WS /transcribe_live`（uvicorn 原生支持 WebSocket），协议为二进制 PCM 帧 + JSON 控制帧。

**必须如实说明的能力边界**：真正的增量解码只有流式模型（如 `paraformer-streaming`）才有。对 SenseVoice / Whisper / Qwen3-ASR 这类非流式模型，采用 **`fsmn-vad` 切段 + 段末出 `completed` + 段内定时重解码出 `delta`** 的近似方案；`delta` 语义是"当前假设"而非严格追加，客户端应以 `completed` 为准。这一点要写进 API 文档，否则接入方会误判。

### 5.2 TTS 运行时：新增 `internal/tts`

完全对齐 `internal/asr` 的形状（同样的 venv 复用、free port、`/health` 就绪探测、空闲回收）：

```
internal/tts/
  types.go        // Engine 接口：Speak / SpeakStream / Cancel / Close / ModelName
  worker.go       // PythonEngine：起进程、健康检查、HTTP 调用
  worker/tts_worker.py  // 内嵌，FastAPI
```

worker HTTP 协议：

| 端点 | 说明 |
| --- | --- |
| `GET /health` | 就绪 + 模型元信息（原生采样率、可用音色列表、是否支持流式） |
| `POST /speak` | 一次性合成，返回 wav/pcm |
| `POST /speak_stream` | 流式：NDJSON，每行 `{"seq":n,"pcm":"<base64>"}` 或 `{"seq":n,"opus":"<base64>"}`；`opus` 由 worker 编码，供 WebRTC 直接打包 |
| `POST /cancel` | 中止进行中的合成（barge-in） |

后端候选（按"有官方声码器/推理库、许可可用、中文效果"排序）：CosyVoice2、Qwen3-TTS、IndexTTS、F5-TTS、Kokoro。新增 `ttsPythonPackages` 包清单与 `EnsureTTSReady` / `InstallTTSWithProgressOptions` / `TTSInstallCommand`，配 `GET /api/tts-runtime`、`POST /api/tts-runtime/install`。

**关键：模型必须整包在官方推理库里跑（LM + flow/duration predictor + 声码器）。CosyVoice2 / Qwen3-TTS 的 LM 部分虽然是 Qwen 系架构，也绝不能只转 GGUF 交给 llama.cpp。**

### 5.3 路由修正（issue 最后一段的根因）

`internal/localinference/support.go`：

```go
// FromLocalModel / FromMarketplaceModel 中，ASR 判断之后、llamaSupport 之前
if support := ttsSupportFromPipelineTag(pipelineTag); support.Supported {
	return support  // {Runtime: "python-tts", Mode: "tts"}
}
```

并在 `FromMarketplaceModel` 的 `switch` 里加 `case "text-to-speech"`，同时按模型名兜底（`model.IsTTSModelFamily`，覆盖 `cosyvoice` / `-tts` / `sensevoice`(ASR) 之类命名）。副作用：`text-to-speech` 模型不再出现在 GGUF 转换入口，Run 对话框改为展示音色/语速而非 `num_ctx`。

### 5.4 音色列表

OpenAI 的音色是固定枚举，本地多模型必须可枚举，放在管理面而不是污染 `/v1`：

```
GET /api/models/{model}/voices
→ { "model": "...", "sample_rate": 24000, "streaming": true,
    "voices": [ { "id": "zh-female-1", "label": "…", "language": "zh", "preview": null } ] }
```

## 6. 配置与持久化

按 `docs/agent-guidelines/config-schema.md`，先复用已有结构，只补一个不重叠的新节：

- **每模型默认值（音色、语速、采样率）** → 复用已有的**每模型配置**（`GET/PUT /api/models/{model}/config`，服务端持久化），并在既有 Run 对话框里展示，不新增配置入口。
- **全局 realtime 设置** → `config.json` 新增 `realtime` 节（唯一可写来源）：

```json
{
  "realtime": {
    "default_asr_model": "iic/SenseVoiceSmall",
    "default_tts_model": "FunAudioLLM/CosyVoice2-0.5B",
    "max_sessions": 4,
    "ice_udp_port_range": [50000, 50100],
    "ice_extra_host_ips": [],
    "ice_servers": []
  }
}
```

- 会话临时音频、worker 临时文件一律落在存储根（默认 `~/.csghub-lite`）下，复用 `liteTempDir()`。

## 7. 与 issue 诉求的逐条对照

| issue 条目 | 本设计 |
| --- | --- |
| 1. `POST /v1/realtime/calls` | §4.4 |
| 2. multipart 含 `sdp` + `session` | §4.4 形态 A（同时支持裸 SDP 形态 B） |
| 3. `Accept: application/sdp` | §4.4，响应 `Content-Type: application/sdp` |
| 4. 返回 SDP Answer | §4.4，200 + `Location: /v1/realtime/calls/{id}` |
| 5. 双向音频轨（上行 ASR / 下行 TTS） | §4.4，Opus 首选 / PCMU 回退 |
| 6. DataChannel `oai-events` | §4.4 |
| 7. ASR `...transcription.delta` / `.completed` | §4.3，能力边界见 §5.1 |
| 8. `response.create` → 下行音频 + `response.done` + `output_audio_buffer.stopped` | §4.3；无 LLM 时 `instructions` 直读 |
| 9. `response.cancel` / `output_audio_buffer.clear` | §4.3，两者语义严格区分 |
| 非 WebRTC 回退：`/v1/audio/transcriptions` | §3.2，兼容扩展 |
| 非 WebRTC 回退：`/v1/audio/speech` 直接返回音频 | §3.1 |
| ASR/TTS 用真正的音频运行时和声码器 | §5.2 + §5.3（路由修正是前置条件） |

### 7.1 issue 所列接口的标准性核对

issue 的清单**绝大部分是 OpenAI GA 的原样路径与事件名**，可以直接作为兼容目标；有两处例外，
以及一处重要的不完整：

| issue 条目 | 标准性 | 处理 |
| --- | --- | --- |
| `POST /v1/realtime/calls`、multipart `sdp`+`session`、SDP answer、双向音轨、`oai-events`、`conversation.item.input_audio_transcription.delta/.completed`、`response.create/.done/.cancel`、`GET/WS /v1/realtime`、`POST /v1/audio/speech` | 标准 | 按原样实现 |
| `Accept: application/sdp` | 非规范（官方 multipart 示例不带此头） | **接受但不要求**；响应固定 `Content-Type: application/sdp` |
| `GET/WS /v1/audio/transcriptions/realtime` | **上游不存在**。纯转录是会话类型 `session.type="transcription"`，连接仍走 `/v1/realtime`（beta 期为 `?intent=transcription`） | 以 `/v1/realtime` + `session.type` 为准；该路径仅作**兼容别名**保留（§4.1） |
| `output_audio_buffer.stopped` / `output_audio_buffer.clear` | 真实行为，但**未列入官方 server-events 参考**，WebRTC 专有且上游有改动迹象 | 实现，但**不作为契约**：真正的完成/取消信号是 `response.done` 的 `status`，`output_audio_buffer.*` 只作 WebRTC 下的补充信号 |

**清单不完整**：issue 是从「客户端发了什么、要收到什么」倒推的，缺少握手必需的
`session.created` / `session.update` / `input_audio_buffer.speech_started` / `.speech_stopped` /
`.committed` / `error` / `conversation.item.create` / `POST /v1/realtime/client_secrets`。
只实现所列 9 条，标准 SDK 无法完成握手。

**因此验收标准定为：OpenAI 官方 Realtime SDK 与示例应用不修改代码即可连接本地服务并完成一轮
对话**，而不是逐条勾选 issue 中列出的事件。

## 8. 分期实施

| 阶段 | 内容 | 价值 |
| --- | --- | --- |
| **P0** | `support.go` 路由修正（TTS 不再走 GGUF 转换）+ 5 个 stub 返回 501 + OpenAPI 同步 | 消除"把 TTS 当文本模型跑"和"GET 拿到 HTML"两个错误行为，改动极小 |
| **P1** | `internal/tts` + `tts_worker.py` + `/api/tts-runtime` + `POST /v1/audio/speech` + `/api/models/{model}/voices` | 端到端可用的本地 TTS，且不依赖 WebRTC |
| **P2** | `internal/realtime` 事件/会话状态机 + `asr.StreamingEngine` + `server_vad` + WebSocket 传输（`/v1/realtime`、`/v1/realtime/transcription`） | 全双工能力落地，事件协议一次做对 |
| **P3** | `pion/webrtc` 传输 + `POST /v1/realtime/calls` + `output_audio_buffer.*` + Opus 打包 | 满足 issue 首选协议 |
| **P4** | `POST /v1/realtime/client_secrets`、`/api/realtime/sessions` 观测、Web UI 语音对话入口、第三方 provider 的 realtime 透传 | 生产化 |

## 8.1 P0 实现记录（已完成）

| 改动 | 位置 |
| --- | --- |
| 新增 TTS 识别（架构表、模型族名、`model_type`、ModelScope `task`） | `internal/model/manifest.go`：`IsTTSArchitecture` / `IsTTSModelFamily` / `isTTSModelType`，并接入 `DetectPipelineTag` 与 `detectModelScopePipelineTag` |
| `text-to-speech` 不再落到 llama 转换路径 | `internal/localinference/support.go`：加入 `unsupportedPipelineTag`，并在架构/模型族两级兜底 |
| 每条推理入口统一拒绝 TTS 模型 | `internal/server/server.go` 的 `getOrLoadEngineFullMode`（chat / generate / load 都汇聚到此），配 `isTTSPipelineTag` / `modelUsesTTSEngine` |
| 5 个端点显式返回 501 | `internal/server/handlers_realtime.go` + `routes.go` |
| OpenAPI 同步 | `openapi/local-api.json`（5 个新 operation，`openapi_sync_test.go` 校验） |
| 回归测试 | `internal/model/manifest_test.go`、`internal/localinference/support_test.go`、`internal/server/handlers_realtime_test.go` |

P0 过程中额外发现并修掉的一处问题：`localinference.FromLocalModel` 会用**探测到的** pipeline tag
无条件覆盖 manifest 里的 tag，而 `DetectPipelineTag` 在找不到更具体的类型时会回落到
`text-generation`。因此一个 manifest 明确标了 `text-to-speech`、但 `config.json` 里只是
`Qwen3ForCausalLM` 的模型，依然会被判成可转换的文本模型。现在「没有本地运行时」的 manifest tag
会被单独尊重。这一处同样适用于 `image-to-video` / `text-to-video` / `video-text-to-text`。

**P0 之后的行为**：TTS 模型仍可搜索和下载，但本地推理显示「暂不支持」，`/api/load` 与 chat 会返回
明确错误，不再静默转成 GGUF；语音端点返回带 `unsupported_error` 的 501。P1 落地时把
`text-to-speech` 从 `unsupportedPipelineTag` 移出，改为 `{Runtime: "python-tts", Mode: "tts"}`，
并给前端 `web/src/utils/localInference.ts` 的 mode 联合类型和 i18n 补上 `tts`。

## 8.2 P1 实现记录（已完成）

ASR 与 TTS **都走 Python 推理运行时，不经 llama.cpp**——把模型输出变成波形的声码器/codec 解码器只
存在于模型自己的推理栈里。TTS 完整镜像了 ASR 既有的那套框架：

| 改动 | 位置 |
| --- | --- |
| 独立 venv 的运行时管理 | `imagegen`：`NewTTSRuntimeManager` / `TTSStatus` / `EnsureTTSReady` / `InstallTTSWithProgressOptions` / `TTSInstallCommand`，落在 `~/.csghub-lite/tts-runtime` |
| 按模型族的按需依赖 | `EnsureModelTTSPackages`（Kokoro 需要 `kokoro` 与 `misaki[zh]`，后者是中文 G2P，缺了会在合成时报缺模块）|
| 引擎接口与进程管理 | `internal/tts`：`Engine` 接口、`PythonEngine`（空闲端口、健康探测、空闲回收）|
| 内嵌 worker | `internal/tts/worker/tts_worker.py`：Kokoro 与 transformers 原生 TTS 两种后端；PCM 经 ffmpeg 转 mp3/opus/flac/aac，流式时用一个长驻 ffmpeg 保证客户端收到连续流而不是拼接文件 |
| 服务端接线 | `getOrLoadTTSEngine`、`POST /v1/audio/speech`、`GET /api/tts-runtime`、`POST /api/tts-runtime/install`、`GET /api/tts-voices?model=` |
| 路由修正 | `text-to-speech` 从 `unsupportedPipelineTag` 移出，改为 `{Runtime: "python-tts", Mode: "tts"}`；前端 mode 联合类型与 i18n 补 `tts` |

**音色发现用查询参数而不是路径参数**：带源前缀的模型 id（如 `modelscope/hexgrad/Kokoro-82M`）有三段，
`{model}` 和 `{namespace}/{name}` 都匹配不了，而未匹配的 GET 会落到静态兜底、返回 200 的 Web UI。

**worker 启动失败的原因会透出到 API**：`tailBuffer` 保留 worker stderr 的尾部，取 traceback 最后一行
作为错误原因，否则调用方只能看到 `exit status 1`。

### 实测结果（Kokoro-82M，真实模型）

| 反馈要求 | 结果 |
| --- | --- |
| `Content-Type: audio/mpeg` | ✅ |
| MP3 流式 | ✅ `Transfer-Encoding: chunked` |
| `voice` / `input` / `response_format` / `instructions` | ✅（`instructions` 被接受，Kokoro 自身忽略）|
| 中文 | ✅ 8 个中文音色，`zf_xiaoxiao` 实测 3.79s、mean -21.1 dB |
| Bearer Authorization | ✅ |
| 出错返回 JSON 而非 HTML | ✅ |

其它容器同样实测通过：`wav`（audio/wav）、`pcm`（`audio/L16; rate=24000; channels=1`）、`opus`、`flac`。

### 官方 Qwen3-TTS（已实测跑通）

`Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice` 通过 csglite 的 modelscope 源拉取（2.3 GB），**codec 随仓库自带**
（`speech_tokenizer/`，682293092 字节，与官方独立仓库 `Qwen3-TTS-Tokenizer-12Hz` 的同名文件字节数一致，
即同一份产物发两处），所以一次 pull 就齐，不需要第二次下载。

映射关系正好对上 OpenAI 接口：`voice` → `speaker`、`instructions` → `instruct`。预设音色不硬编码，
从 `config.talker_config.spk_id` 读取，因此任何变体（含 1.7B）都会报告自己那一套；`spk_is_dialect`
额外给出方言信息（Eric 四川话、Dylan 北京话）。

实测（反馈者原始 payload，仅替换 model id）：`status=200`、`Content-Type: audio/mpeg`、
`Transfer-Encoding: chunked`、2.38 秒、mean −21.9 dB。Vivian/Dylan/Ryan 与 Kokoro 回归均通过。

Base 变体是声音克隆（需要参考音频），没有预设音色，因此 `/v1/audio/speech` 会返回一条说明并建议改用
CustomVoice。

### transformers 版本策略

**只声明 `>=` 下界，不写 `==` 定版**，与 Diffusers 运行时既有的 `transformers>=4.48.0,<5.0` 同形。
多个后端共用一个 venv，定版会让「最后装的那个」决定所有后端的版本，装一个就把另一个的 transformers
拽走，来回震荡。

TTS 运行时声明 `transformers>=4.57.3,<5.0`。上界是实测出来的而非猜的：`qwen-tts` 在 5.x 上 import 即失败
（`TypeError: check_model_inputs() missing 1 required positional argument: 'func'`），而 Kokoro 与
transformers 原生后端在 4.57.3–4.57.6 全区间正常。另外 `qwen-tts` 自身 `pyproject.toml` 硬钉
`transformers==4.57.3`，所以 `EnsureModelTTSPackages` 装完按模型的额外依赖后会**重新声明一次约束**，
保证版本只前进、不被第三方包拽回去。

### 已支持的后端（均实测出音频）

| 模型 | 体积 | backend | 音色 | 备注 |
| --- | --- | --- | --- | --- |
| `hexgrad/Kokoro-82M` | 327 MB | `kokoro` | 54（8 中文） | 需 `misaki[zh]` 才能念中文 |
| `facebook/mms-tts-eng` | 145 MB | `transformers` | 0（单说话人） | 同架构覆盖 MMS 上千语言 |
| `Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice` | 2.3 GB | `qwen3-tts` | 9（5 中文，含京/川方言） | codec 随仓库自带 |
| `openbmb/VoxCPM2` | 4.6 GB | `voxcpm` | 0（参考音频克隆） | 原生流式；`audiovae.pth` 随仓库自带 |

`transformers` 后端还覆盖 SpeechT5、Bark、ParlerTTS、CSM、Dia、FastSpeech2；其中只有 VITS/MMS 一路
经过实测，其余同属一个代码路径但未逐个验证。

选后端的判据（这几轮踩出来的）：**上游必须用 `>=` 声明依赖**。VoxCPM 的 `voxcpm` 包声明
`torch>=2.5.0` / `transformers>=4.36.2`，装进共享 venv 后 numpy / torch / torchaudio /
transformers / protobuf **一个都没动**，所以不需要 overlay——这是「尽可能复用」的理想情况。

### 没有纳入的后端与原因

| 模型 | 卡点 |
| --- | --- |
| fish-speech / OpenAudio S1 | 推理栈的 `descript-audiotools`（最新 0.7.2）仍调用 `torchaudio.list_audio_backends`，该 API 在 torchaudio 2.x 已移除。全部 import 都能成功，运行时挂掉——**上游停更**，不是版本约束问题。相关代码已移除，等上游修复后再加。 |
| fish-speech S2 / 2.0 | 只有 `fishaudio/s2-pro`（11 GB，HF，ModelScope 无镜像），且 PyPI 的 `fish-speech` 0.1.0 对应的是 S1 一代，没有 S2 的包。 |
| CosyVoice 2 / 3 | 官方仓库没有 `setup.py`/`pyproject.toml`，只有 `requirements.txt` + `third_party/Matcha-TTS` 子模块，必须 vendoring；且 `requirements.txt` 31 行几乎全是 `==` 硬钉，其中 `numpy==1.26.4` 会把共享 numpy 从 2.x 拽回 1.x。numpy 是 torch/scipy/librosa 的二进制底座，无法像 protobuf 那样用 overlay 覆盖。最小 4.62 GB。 |
| `Vikhrmodels/Qwen3-0.6B-TTS` | codec 是 BigCodec，权重只在 HF（`Alethia/BigCodec`），且该模型只支持 en/ru/uk，念不出中文。 |

### 依赖隔离（overlay）

为「某个后端的包与其它后端冲突」准备的机制：`--target` 装进私有目录，通过 `PYTHONPATH` 置于共享
venv 的 site-packages 之前。实测有效——私有 protobuf 3.19.6 生效的同时，torch / numpy 仍来自共享，
私有目录仅 780 KB 而不是复制一个 3 GB 的 venv。

当前 `ttsOverlayPackages` 为空：四个已支持的后端都能共处一个 venv。机制保留，因为放任某个后端的
pin 改写共享环境会弄坏其它后端。

### 流式合成的首包延迟

`stream=true` 曾经只是 HTTP chunked：首字节几乎与总耗时同时到达（实测三个后端的差值都只有 7 毫秒），
客户端看起来在流、实际上要等整段生成完。两个原因：

1. **worker 把编码串行化了**。它先把全部 PCM 写进 ffmpeg、关掉 stdin，然后才开始读 stdout，于是编码
   后的字节不可能比最后一段 PCM 更早出来。现在用一个线程喂 stdin、主协程用 `read1` 边读边发。
2. **引擎大多一次性产出整段音频**。`qwen-tts` 的 docstring 明确写了它没有流式生成（`non_streaming_mode`
   只模拟流式文本*输入*）；Kokoro 虽然按段 yield，但实测五句话的文本它只切出一段。

所以流式路径改为**按句切分**：`_split_for_streaming` 在句末标点处切开，逐句合成、每句出来就发。下界取
6 个字符，因为一句中文约 8 字，取大了会把几句并成一次调用。

实测（五句中文，引擎已预热，CPU）：

| 模型 | 修复前首字节 | 修复后首字节 | 总耗时 |
| --- | --- | --- | --- |
| Kokoro-82M | ≈ 总耗时 | **1.47s** | 3.47s |
| Qwen3-TTS-0.6B-CustomVoice | ≈ 总耗时 | **5.84s** | 14.57s |

切分只作用于流式路径。普通请求仍整段合成，因为逐句调用无法还原跨句韵律。

## 8.3 P2 实现记录（已完成）

WebSocket 传输、流式 ASR 与实时事件协议已落地。

| 改动 | 位置 |
| --- | --- |
| 事件协议与会话对象 | `internal/realtime/events.go`、`session.go` |
| 会话状态机（打断、取消、缓冲清空、序号） | `internal/realtime/session_runtime.go` |
| 流式 ASR worker 端点 | `internal/asr/worker/asr_worker.py` 的 `LiveSession` + `WS /transcribe_live` |
| 流式 ASR 客户端 | `internal/asr/live.go`：`StreamingEngine` / `LiveStream` |
| WebSocket 端点 | `internal/server/handlers_realtime_ws.go`：`GET /v1/realtime`、`/v1/realtime/transcription`、`/v1/audio/transcriptions/realtime` |

**会话层与传输解耦**是刻意的：WebRTC 与 WebSocket 只差「音频走媒体轨还是走 base64 事件」，
`realtime.Sender` 把这点抽象掉，所以 P3 只需实现一个新的 Sender，会话逻辑一行不用改。

**流式 ASR 的实现方式**：已加载的后端只能转写完整片段，所以切分放在 worker 里——用 fsmn-vad
增量跑（`cache` + `is_final=False`）找出语音起止，每个结束的片段转写为 `completed`，并按间隔对
当前累积音频重新转写产生 `delta`。因此 **`delta` 是「当前片段的最佳假设」而不是只增前缀**，客户端
应以 `completed` 为准；这一点已写进 OpenAPI 描述。没有 VAD 时退化为由客户端 `commit` 切分。

### 实测（Kokoro，CPU，引擎预热后）

```
流式：音频帧 5 个（一句一帧）、首帧 0.22s、总耗时 1.13s
打断：收到首帧立即 response.cancel → output_audio_buffer.stopped → response.done status=cancelled
事件顺序：session.created → response.created → output_audio_buffer.started
          → response.output_audio.delta × N → response.output_audio.done
          → output_audio_buffer.stopped → response.done
```

首帧 0.22s 落在反馈要求的 300–800ms 之内。

### 一个连带修掉的不一致

句子切分最初只加在压缩格式那条分支，而 realtime 用的是 `pcm`——于是整段音频作为一帧到达（首帧
7.49s），取消也因为生成早已结束而报成 `completed`。两条分支现在共用同一份 `segments`。

### 仍未完成

WebRTC 传输（`POST /v1/realtime/calls`）仍返回 501，对应 P3。它需要 SDP 协商、ICE、Opus 编解码
与媒体轨，而会话与事件层已经就绪、可直接复用。

### chat 界面尚未接入 TTS

`Chat.tsx` 已有语音**输入**（`MediaRecorder` + `/v1/audio/transcriptions`），但没有任何朗读/播放代码
（`new Audio(` / `speechSynthesis` / `/v1/audio/speech` 均为 0 处引用）。本轮只落地了 API 层，加「朗读」
按钮可照抄现有的 ASR 接线方式。

### `Vikhrmodels/Qwen3-0.6B-TTS` 仍然无法合成

该仓库声明 `vocab_size: 160887`，而三个 tokenizer 文件已知的最大 id 只到 151670 ——
**9216 个 embedding 行没有任何 tokenizer 条目**，仓库里也没有 codec 解码器权重。

codec 后来查明了：它属于 [VikhrModels/Salt](https://github.com/VikhrModels/Salt) 家族，用的是
**BigCodec**（作者 model card 原话「BigCodec tokenizer」）。按 Salt 的 `get_start_tokens`（顺序
wav→bigcodec→speech，且 `wav.n_new_tokens = 0`）可推出 BigCodec 占 **151671–159862**（8192 个）、
SpeechTokenizer 占 159863–160886（1024 个），合计正是 9216；用同族的 `ksych/salt-bigcodec`
（仅含 bigcodec，`vocab_size 159859`）反推基数可交叉验证。解码路径也对得上：PyPI 的 `bigcodec` 包里
`BigCodec.decode()` 与 Salt 的 `decode_audio_bigcodec` 逐行一致，输出 16 kHz。

仍未接入的原因有两条，都不是技术障碍：BigCodec 权重只在 HuggingFace（`Alethia/BigCodec/bigcodec.pt`，
ModelScope 无镜像，且是裸 checkpoint 需手工凑构造参数）；更重要的是该模型语言为 **en / ru / uk**
（训练集 librispeech + 俄语书 + common voice，作者自报 PESQ 1.11），**念不出中文**。需要中文的场景应使用
官方 Qwen3-TTS。

## 9. 风险与待确认

1. **Opus 编码归属**：把编码放进 Python worker 是为了守住 `CGO_ENABLED=0`。若 worker 侧 `PyAV`/`opuslib` 在 Windows 上装不上，退路是协商 `PCMU/8000`（音质降级）或在 Go 侧实现纯 Go Opus 编码器（成本高）。**P1 阶段就要在三平台验证 wheel 可装性。**
2. **端到端延迟预算**：目标首字音 < 800ms。VAD 尾静音 500ms + ASR 段解码 + LLM 首 token + TTS 首包，任一环节都可能吃掉预算；P2 需要按环节埋点（复用 `internal/observability`）。
3. **非流式 ASR 的 `delta` 语义**是近似值（§5.1），需要在 OpenAPI 描述里写明，避免接入方按"严格追加"实现。
4. **`audio.output.model` 是 csglite 扩展**，OpenAI SDK 的强类型 session 对象可能拒绝该字段；因此必须同时支持"由 `x_csglite` 或全局默认推断 TTS 模型"，保证只用标准字段的客户端也能跑通。
5. **资源占用**：ASR + LLM + TTS 三个模型常驻的显存/内存开销可能超出单机预算；`realtime.max_sessions` 与空闲回收策略要在 P2 定下来。
6. **第三方 provider 的 realtime**：`provider_pipeline_rules_gen.go` 已有 `audio_speech` 模式，但 provider pool 目前对音频返回 501。是否代理上游 realtime（需要重新协商 SDP，成本高）建议留到 P4 再定。

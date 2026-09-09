# 实时语音 API 设计（ASR + TTS 全双工，兼容 OpenAI Realtime）

- 状态：设计草案（待评审），**P0 已实现**，对应 issue [#147](https://github.com/OpenCSGs/csglite/issues/147)
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

## 9. 风险与待确认

1. **Opus 编码归属**：把编码放进 Python worker 是为了守住 `CGO_ENABLED=0`。若 worker 侧 `PyAV`/`opuslib` 在 Windows 上装不上，退路是协商 `PCMU/8000`（音质降级）或在 Go 侧实现纯 Go Opus 编码器（成本高）。**P1 阶段就要在三平台验证 wheel 可装性。**
2. **端到端延迟预算**：目标首字音 < 800ms。VAD 尾静音 500ms + ASR 段解码 + LLM 首 token + TTS 首包，任一环节都可能吃掉预算；P2 需要按环节埋点（复用 `internal/observability`）。
3. **非流式 ASR 的 `delta` 语义**是近似值（§5.1），需要在 OpenAPI 描述里写明，避免接入方按"严格追加"实现。
4. **`audio.output.model` 是 csglite 扩展**，OpenAI SDK 的强类型 session 对象可能拒绝该字段；因此必须同时支持"由 `x_csglite` 或全局默认推断 TTS 模型"，保证只用标准字段的客户端也能跑通。
5. **资源占用**：ASR + LLM + TTS 三个模型常驻的显存/内存开销可能超出单机预算；`realtime.max_sessions` 与空闲回收策略要在 P2 定下来。
6. **第三方 provider 的 realtime**：`provider_pipeline_rules_gen.go` 已有 `audio_speech` 模式，但 provider pool 目前对音频返回 501。是否代理上游 realtime（需要重新协商 SDP，成本高）建议留到 P4 再定。

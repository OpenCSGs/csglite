# 模型推理与诊断

## 单次推理

先用 `csghub-lite start` 启动本机服务。本地模型可先用 `pull` 下载。脚本可以直接读取 `generate` 输出的回答或 `embed` 输出的 OpenAI JSON。推理失败时命令以非零状态退出，原因写入标准错误。

```sh
csghub-lite generate Qwen/Qwen3-0.6B-GGUF "用一句话介绍北京"
csghub-lite generate MODEL "给这段文字起个标题" --source cloud --json
csghub-lite embed BAAI/bge-m3 "第一段文本" "第二段文本"
```

省略文本时，两个命令都将标准输入的全部内容作为一条文本。批量求向量时，每个参数对应一条文本。`--source` 默认是 `local`，也可指定 `cloud`、`cluster`、`node:UUID` 或已配置的提供方 ID。模型 ID 必须属于所选来源。云端调用使用本机服务已配置的账号。

## 加载与配置

```sh
csghub-lite load Qwen/Qwen-Image-2.1 --keep-alive 5m
csghub-lite model-config Qwen/Qwen3-0.6B-GGUF
csghub-lite model-config Qwen/Qwen3-0.6B-GGUF --num-ctx 8192 --num-parallel 2
csghub-lite model-config Qwen/Qwen3-0.6B-GGUF --num-ctx 0
```

`load` 等待服务返回就绪状态，可用 `ps` 查看模型。单次加载可用 `--keep-alive -1` 常驻。

`model-config` 无参数选项时读取当前设置。带选项时只更新指定字段。`--num-ctx 0`、`--num-parallel 0` 可清除对应覆盖值，`--keep-alive=`、`--dtype=`、`--backend=` 可恢复对应默认设置。命令接受本地公开模型 ID，也接受完整存储 ID，例如 `huggingface/org/name`。

## 查看模型日志

```sh
csghub-lite logs Qwen/Qwen-Image-2.1
csghub-lite logs Qwen/Qwen-Image-2.1 -n 20 -f
csghub-lite logs -n 0 -f
```

默认显示本机 `~/.csghub-lite/logs/csghub-lite.log` 中最近 50 条匹配行，按时间从早到晚输出。服务停止后仍能读取已有文件。省略模型可查看所有服务日志。`-n 0 -f` 只跟踪新增行，按 Ctrl+C 退出。

指定模型时会匹配含该模型 ID 的日志行，包括加载进度和带模型名的生图任务错误。已安装模型还会匹配其本地存储 ID 和公开 ID。各推理后端写入独立文件的原始标准输出请在同一日志目录查看，例如 `diffusers-worker.log` 和 `llama-server.log`。

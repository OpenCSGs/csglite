# csghub-lite runtime

查看或升级本地 Python 运行时：image（Diffusers）、ASR（语音识别）、TTS（语音合成）。

## 用法

```bash
csghub-lite runtime status [image|asr|tts]
csghub-lite runtime upgrade [image|asr|tts] [--pipeline 类名]
```

## 说明

三类模型分别运行在各自的 Python 虚拟环境中，位于 csghub-lite 存储根目录下（默认 `~/.csghub-lite`）：

- `status` 不带参数时显示全部三个运行时的就绪状态；image 运行时还会显示已安装和最新可用的 diffusers 版本
- `upgrade` 不带参数时升级 image（Diffusers）运行时；运行时缺失时也会执行完整安装
- `image` 可用别名 `diffusers`；升级 ASR 或 TTS 运行时会同时以最新版本重装 PyTorch，下载量可能较大
- `--pipeline` 只适用于 image 运行时：升级后验证已安装的 diffusers 是否提供该 Pipeline 类（必须是 DiffusionPipeline 子类，例如 `QwenImage21Pipeline`），缺失时命令以非零退出并提示

新模型可能需要更新版本的 diffusers（例如 Qwen-Image-2.1 需要 diffusers 0.41.0 及以上，旧版本会报 `module diffusers has no attribute QwenImage21Pipeline`）。这类新版本的 diffusers 依赖较新的 huggingface_hub，与 transformers 4.x 不兼容，因此升级会把 transformers 一并升到 5.x；全新安装（首次安装或后台服务自动安装）解析依赖时会采用索引提供的最新版本（通常是 transformers 5.x）。升级是否解决报错取决于配置的包索引实际提供的版本：如果镜像尚未同步最新版本，升级后版本不变，此时可检查 `CSGHUB_LITE_PACKAGE_MIRROR` 或 `CSGHUB_LITE_PYPI_INDEX_URL` 指向的索引，或改用官方索引。建议加 `--pipeline 类名`，让命令直接验证升级结果。

如果后台服务正在运行且已加载模型（图像、ASR、TTS），请先卸载模型（例如 `csghub-lite stop model MODEL`）再升级；正在运行的 worker 会继续使用启动时的包版本。升级过程被中断（例如 Ctrl-C）时可能留下不完整的虚拟环境，重新执行一次 `runtime upgrade` 即可修复。

## 示例

```bash
# 查看全部运行时状态
csghub-lite runtime status

# 查看 image (Diffusers) 运行时状态
csghub-lite runtime status image

# 升级 image (Diffusers) 运行时（默认）
csghub-lite runtime upgrade

# 升级并验证 Qwen-Image-2.1 所需的 Pipeline 类
csghub-lite runtime upgrade --pipeline QwenImage21Pipeline

# 升级 ASR 运行时
csghub-lite runtime upgrade asr
```

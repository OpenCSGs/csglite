# 模型格式

CSGLite 支持下载和管理多种格式的模型，但仅 GGUF 格式可直接用于本地推理。

## 格式对照

| 格式 | 下载 | 推理 | 说明 |
|------|------|------|------|
| GGUF | 支持 | 支持 | llama.cpp 原生格式，量化后体积小、推理快 |
| SafeTensors | 支持 | 不支持（需转换） | PyTorch 生态常用格式 |

## GGUF 格式

GGUF（General GGML Universal Format）是 llama.cpp 项目的模型格式。特点：

- **单文件**: 模型权重、词表、配置合并在一个 `.gguf` 文件中
- **量化**: 支持多种量化精度（Q4_0、Q4_K_M、Q5_K_M、Q8_0、F16 等）
- **即用**: CSGLite 可直接加载推理

### 量化级别参考

| 量化 | 说明 | 体积 | 质量 |
|------|------|------|------|
| Q4_0 | 4-bit 基础量化 | 最小 | 一般 |
| Q4_K_M | 4-bit K-quant medium | 小 | 较好 |
| Q5_K_M | 5-bit K-quant medium | 中等 | 好 |
| Q8_0 | 8-bit 量化 | 较大 | 很好 |
| F16 | 16-bit 半精度 | 大 | 原始精度 |

选择建议：

- 内存有限（8GB）: Q4_K_M
- 内存充裕（16GB+）: Q8_0
- 追求精度: F16

## SafeTensors 格式

SafeTensors 是 Hugging Face 推出的模型存储格式。CSGLite 支持下载但不支持直接推理。

### 自动转换（内置脚本）

CSGLite 将固定 llama.cpp 版本的 `convert_hf_to_gguf.py`、`conversion`
和完整 `gguf-py` **一起打包进二进制**。首次推理时，这些文件会释放到
`~/.csghub-lite/tools/` 后调用本机 Python；不需要安装 Git，也不会在
运行时 clone 或下载 llama.cpp 源码。升级 CSGLite 后若内置工具更新，会随
**`bundledConverterRevision`** 自动使用新的版本化缓存。

- 转换始终通过 `PYTHONPATH` 使用与内置 converter 同 tag 的 `gguf-py`，不会使用系统或 PyPI 中可能不匹配的 `gguf`。
- 如果检测到本机 `transformers` 版本过旧，CSGLite 会自动尝试执行 `python -m pip install -U transformers`，然后重试一次转换。
- 使用镜像上的脚本：设置环境变量 **`CSGHUB_LITE_CONVERTER_URL`** 为 raw 地址（下载一次后按 URL 缓存）。
- 维护者升级内置工具时，先在
  [`OpenCSGs/llama-cpp-assets`](https://github.com/OpenCSGs/llama-cpp-assets)
  发布匹配 llama.cpp tag 的依赖版本，再更新 CSGLite 的 `go.mod` 和
  `llama-server` 安装版本。
- 如需控制自动转换输出类型，可在 `run` / `chat` 时加 `--dtype`，例如：`csghub-lite run Qwen/Qwen3-0.6B --dtype q8_0`。支持的值与内置 llama.cpp 转换器 `--outtype` 对齐：`f32`、`f16`、`bf16`、`q8_0`、`tq1_0`、`tq2_0`、`auto`。如果模型包含视觉投影器，`mmproj` 也会按相同 `dtype` 一起转换。

### 转换为 GGUF（手动）

使用 llama.cpp 提供的转换工具：

```bash
# 克隆 llama.cpp（如未安装）
git clone https://github.com/ggml-org/llama.cpp
cd llama.cpp

# 安装 Python 依赖
pip install -r requirements.txt

# 转换（默认 f16 精度）
python convert_hf_to_gguf.py /path/to/safetensors/model

# 进一步量化（可选）
./llama-quantize /path/to/model-f16.gguf /path/to/model-q4_k_m.gguf q4_k_m
```

转换完成后，将 `.gguf` 文件放入模型目录即可：

```bash
cp model-q4_k_m.gguf ~/.csghub-lite/models/namespace/name/
```

## 如何选择模型

在 CSGHub 上搜索模型时：

```bash
# 搜索可直接推理的 GGUF 模型
csghub-lite search "Qwen GGUF"

# 搜索 SafeTensors 模型（需要转换）
csghub-lite search "Qwen"
```

模型名中带有 `-GGUF` 后缀的通常提供 GGUF 格式文件。

## 投机解码加速

本地文本生成模型（GGUF，以及会自动转换为 GGUF 的 SafeTensors/PyTorch）可在运行参数窗口启用投机解码，也可通过 CLI 配置：

```bash
# 无需 draft 模型（原生 GGUF 或未转换的 SafeTensors 均可）
csghub-lite run Qwen/Qwen3-0.6B --spec-type ngram-mod
csghub-lite run Qwen/Qwen3-0.6B-GGUF --spec-type ngram-mod

# 模型自带或同目录包含 MTP head
csghub-lite run Qwen/Qwen3-Next-GGUF --spec-type draft-mtp

# 独立 draft 模型，可同时叠加 n-gram
csghub-lite run target/model \
  --spec-type draft-eagle3,ngram-mod \
  --spec-draft-model draft/model \
  --spec-draft-n-max 16
```

- `n-gram-*` 不要求专用模型，适合输入或输出中有重复模式的场景。
- llama.cpp b10326 没有独立的 `suffix` 类型；其上下文后缀复用场景由
  `ngram-simple` 等 N-gram 方法覆盖，CSGLite 不会伪造一个运行时不支持的参数。
- `draft-simple` 要求小模型与目标模型使用兼容 tokenizer。
- `draft-eagle3`、`draft-dflash`、`draft-dspark` 要求对应方法训练得到的专用 draft 权重，不能用任意小模型替代。
- `draft-mtp` 仅适用于带 MTP/NextN head 的模型；CSGLite 会优先自动配对同目录的 MTP GGUF companion。
- llama.cpp 支持一个 `draft-*` 方法与一个或多个 `ngram-*` 方法同时启用，但不能同时启用多个 `draft-*` 方法。

高级参数 `--spec-draft-n-max` 和 `--spec-draft-p-min` 分别控制最大候选
token 数与最小接受概率。加速效果依赖模型、提示内容和硬件；若接受率过低，
关闭投机解码通常更快。

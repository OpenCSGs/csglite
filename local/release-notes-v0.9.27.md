## What's New

- Enabled ROCm unified memory by default on AMD APU hosts (#72): llama-server now allocates model weights from system RAM on integrated GPUs such as the Radeon 780M, so large models load instead of failing with `unable to allocate ROCm0 buffer`. Set `CSGHUB_LITE_ROCM_UNIFIED_MEMORY=0` to opt out; discrete ROCm GPUs are unaffected.
- `GET /api/providers?source=third_party` now returns each configured provider's stored `api_key` so local clients can reuse the configuration.

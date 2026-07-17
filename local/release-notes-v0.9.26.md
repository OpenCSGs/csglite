## What's New

- Fixed large-model load failures on small-VRAM GPUs such as AMD APUs (#72): when GPU layers are not set, csghub-lite no longer forces `-ngl 9999` and lets llama-server auto-fit GPU offload to the free device memory.
- Setting GPU layers to 0 now reliably disables GPU offload; any explicit value is passed to llama-server unchanged.

## What's New

- Added runtime Web UI navigation customization: set `CSGHUB_LITE_HIDDEN_NAV_ITEMS` to hide selected sidebar entries without disabling their page URLs.
- Improved model startup visibility (#73): the Dashboard and Library now show localized loading, runtime installation, and conversion steps, including progress when available.
- Fixed GGUF repositories with dot-separated quantization names (#75), so CSGLite recognizes available variants and downloads the selected files instead of the entire repository.

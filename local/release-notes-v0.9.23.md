## What's New

- Fixed image generation with third-party provider models: image models such as `gpt-image-2` configured through an OpenAI-compatible provider now route to the provider's images API instead of failing with "model not found locally" (#60).
- Fixed CSGLite overwriting the CSGClaw sandbox launch mode: a user-configured Docker sandbox provider is now preserved when launching CSGClaw from CSGLite, instead of being reset to BoxLite (#61).

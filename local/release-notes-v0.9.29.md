## What's New

- Fixed chat model selection when opening conversation history (#70): each conversation now restores its recorded model, while new conversations and histories without a model continue using the user's saved preference instead of inheriting a model from another chat.
- Improved image generation safeguards (#71): applying a parameter preset now asks before replacing a hand-written prompt, invalid width or height values show an inline error and disable generation, and large local image sizes display a GPU out-of-memory warning.

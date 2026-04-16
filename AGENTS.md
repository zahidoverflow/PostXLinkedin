# Codex Agent Instructions (PostXLinkedin)

These instructions apply to any Codex / coding agent working in this repo.

## Goal

Maintain and extend a Go-based Telegram bot that posts a photo + caption to:

- X (Twitter) via X API
- LinkedIn via LinkedIn API

The bot supports:

- Direct mode (default): bot posts to X + LinkedIn itself
- Optional "agent webhook" to rewrite/format captions before posting
- Telegram chat setup wizard (`/start`, `/setup`) that persists config to disk

## Repo Layout

- `PostXLinkedInbot/`: Go module (the bot)

Key bot packages:

- `PostXLinkedInbot/internal/bot`: main runtime + command/message handling
- `PostXLinkedInbot/internal/setup`: Telegram setup wizard state machine
- `PostXLinkedInbot/internal/store`: persisted JSON config storage
- `PostXLinkedInbot/internal/x`: X API client + verification
- `PostXLinkedInbot/internal/linkedin`: LinkedIn API client + verification
- `PostXLinkedInbot/internal/agent`: optional caption-rewrite webhook client

## Non-Negotiables

- Never commit secrets:
  - `.env`
  - `data/config.json` (or any `CONFIG_PATH` output)
  - access tokens, refresh tokens, client secrets, webhook secrets
- Do not log secrets. Redact tokens if printing status.
- Preserve the bot's "simple UX" requirement:
  - Setup must be doable from Telegram chat with minimal steps.
  - Error messages must be short and actionable.
- Keep the bot safe-by-default:
  - encourage locking to a single chat id
  - default config files should be `0600` and dirs `0700` on Linux

## Local Dev Commands

From `PostXLinkedInbot/`:

```bash
gofmt -w .
go test ./...
go mod tidy
```

If you add new features, add at least:

- one smoke testable path (unit test, or a small deterministic function)
- a clear README update (minimal, not verbose)

## Run Modes (Behavior Contract)

1. Direct mode:
   - X: upload media, then create post (truncate to fit if needed)
   - LinkedIn: initialize image upload, upload bytes, create image post
2. Agent webhook (optional):
   - Bot calls the agent before posting
   - Request: `{ "caption": "...", "targets": ["x","linkedin"] }`
   - Response: `{ "ok": true, "caption": "..." }`
   - If agent fails, bot posts the original caption

Do not silently change these payload contracts without updating docs.

## Coding Style

- Prefer small, explicit functions.
- Prefer standard library over heavy dependencies.
- Keep Telegram handler logic readable:
  - no deeply nested branching
  - early returns
- Avoid over-engineering:
  - no complex framework layers
  - no unnecessary generics

## When You Change Setup Wizard

- Keep steps minimal.
- Keyboard choices must be obvious and consistent.
- Always support `/cancel`.
- After successful setup:
  - confirm success
  - remind user to delete token messages from chat

## Security Notes (Important)

- Tokens pasted to Telegram can be read in chat history.
  - Always prompt the user to delete messages that contain tokens.
- Assume the bot could be added to other chats:
  - strongly prefer chat lock (`AllowedChatID`)

## PR/Change Checklist

- `go test ./...` passes
- No secrets in git diff
- README updated if the user-facing behavior changed
- Any new env vars are documented in `PostXLinkedInbot/.env.example`


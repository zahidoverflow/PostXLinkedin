# PostXLinkedInbot (Go) + n8n

You send the Telegram bot a **photo** with a **caption**.

Two supported modes:

- **Direct mode (default):** Go bot posts to **X** and **LinkedIn** directly via their APIs.
- **n8n mode (optional):** Go bot calls an **n8n webhook**, and n8n posts to X + LinkedIn.

## Repo Layout

- `PostXLinkedInbot/`: Go Telegram bot
- `n8n/`: docker-compose + workflow template

## Quick Start (VPS)

### Fast Install (systemd)

From `PostXLinkedInbot/` on your VPS:

```bash
sudo ./scripts/vps/install.sh
sudo nano /opt/PostXLinkedin/PostXLinkedInbot/.env
sudo systemctl start postxlinkedinbot.service
sudo journalctl -u postxlinkedinbot.service -f
```

### 1) Run n8n

From `n8n/`:

```bash
docker compose up -d
```

Open n8n, import the workflow JSON from `n8n/workflows/`, then set credentials/variables in the workflow:

- X OAuth2 access (needs `tweet.write`, `users.read`, `offline.access`, and `media.write` if you use media upload)
- LinkedIn OAuth2 access (needs permissions for posting + image upload)
- `POSTXLINKEDIN_SECRET` (must match bot `N8N_SHARED_SECRET`)
- `LINKEDIN_AUTHOR_URN` (your `urn:li:person:...` or org urn)

### 2) Create a Telegram bot

Create a bot with BotFather and set `TELEGRAM_BOT_TOKEN`.

Optionally lock it down with `ALLOWED_CHAT_ID` (your chat id).

### 3) Run the Go bot

From `PostXLinkedInbot/`:

```bash
cp .env.example .env
# edit .env

go run ./cmd/postxlinkedinbot
```

Or build:

```bash
go build -o postxlinkedinbot.exe ./cmd/postxlinkedinbot
```

## Environment Variables (Bot)

See `PostXLinkedInbot/.env.example`.

## Notes

- The bot only posts when you send a **photo** with a **caption**.
- The default `MAX_IMAGE_BYTES` is `5,000,000` to stay under common X image limits. Adjust if you want.
- Setup can be done inside Telegram: `/start` will launch a wizard and save to `CONFIG_PATH`.

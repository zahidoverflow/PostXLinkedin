<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white" />
  <img src="https://img.shields.io/badge/Telegram-Bot%20API%20v5-26A5E4?logo=telegram&logoColor=white" />
  <img src="https://img.shields.io/badge/X%20API-v2-000000?logo=x&logoColor=white" />
  <img src="https://img.shields.io/badge/LinkedIn%20API-v202601-0A66C2?logo=linkedin&logoColor=white" />
  <img src="https://img.shields.io/badge/license-MIT-green" />
</p>

<h1 align="center">PostXLinkedInBot</h1>

<p align="center">
  A self-hosted Telegram bot that cross-posts to <strong>X (Twitter)</strong> and <strong>LinkedIn</strong> simultaneously — image or text-only — with an interactive setup wizard, automatic token refresh, and optional AI caption rewriting.
</p>

---

## Highlights

- **Zero-config files** — the entire setup runs inside Telegram via an interactive wizard
- **Dual platform posting** — X + LinkedIn from a single photo or text message
- **Text & image posts** — send a photo with caption *or* plain text
- **OAuth 2.0 PKCE (S256)** — secure X auth flow handled by the bot itself
- **Auto token refresh** — X tokens are refreshed transparently before every post
- **AI Agent webhook** — optional hook to rewrite captions (hashtags, formatting, tone) via n8n / LangChain / any HTTP endpoint
- **n8n mode** — delegate the actual posting to an n8n workflow instead of calling APIs directly
- **Per-platform feedback** — real-time progress and individual success/error status in chat
- **VPS-ready** — systemd service file + install/update scripts included
- **Single binary** — compiles to one Go executable, no runtime dependencies

---

## 🚀 Beginner's Quick Start (VPS / Linux Mint)

If you have a VPS or a Linux machine (like Mint or Ubuntu), follow these simple steps to get the bot running permanently in the background.

### Step 1: Get your Telegram Token
1. Message [@BotFather](https://t.me/BotFather) on Telegram.
2. Send `/newbot` and follow the instructions.
3. **Copy the API Token** it gives you (looks like `123456:ABC-DEF...`).

### Step 2: One-Click Setup
Open your terminal and paste these commands one by one:

```bash
# 1. Download the code
git clone https://github.com/zahidoverflow/PostXLinkedin.git
cd PostXLinkedin/PostXLinkedInbot

# 2. Run the automatic installer
sudo ./scripts/vps/install.sh
```

### Step 3: Add your Token
The installer created a configuration file for you. Now you need to add your Telegram token to it:
1. Open the file: `sudo nano /opt/PostXLinkedin/PostXLinkedInbot/.env`
2. Find the line `TELEGRAM_BOT_TOKEN=...` and paste your token there.
3. Press `Ctrl + O`, then `Enter` to save, and `Ctrl + X` to exit.

### Step 4: Start the Bot
Run this command to start your bot and make sure it stays running even if your computer restarts:
```bash
sudo systemctl start postxlinkedinbot
```

### Step 5: Finalize in Telegram
Go to your bot in Telegram and send `/start`. The interactive wizard will walk you through connecting your X (Twitter) and LinkedIn accounts!

---

## 🛠 Managing your Bot (Useful Commands)

Since the bot is installed as a **systemd service** (a program that runs silently in the background), you use these commands to manage it:

| Task | Command |
|------|---------|
| **Check if it's running** | `sudo systemctl status postxlinkedinbot` |
| **Stop the bot** | `sudo systemctl stop postxlinkedinbot` |
| **Restart the bot** | `sudo systemctl restart postxlinkedinbot` |
| **See live activity/logs** | `sudo journalctl -u postxlinkedinbot -f` |

---

## Architecture

```
Telegram ──▶ PostXLinkedInBot ──┬──▶ X API v2        (OAuth 2.0 PKCE S256)
                                ├──▶ LinkedIn API     (REST, version 202601)
                                └──▶ Agent webhook    (optional caption rewrite)

                    ── OR ──

Telegram ──▶ PostXLinkedInBot ──▶ n8n webhook  ──▶ X + LinkedIn
```

```
cmd/postxlinkedinbot/main.go     Entrypoint, graceful shutdown
internal/
  bot/       config.go            Env-based config (caarlos0/env)
             run.go               Update loop, command handlers, posting logic
  telegram/  telegram.go          Telegram client wrapper, photo download, MIME detection
  x/         auth.go              OAuth 2.0 PKCE S256 flow + token refresh
             client.go            /2/media/upload, /2/tweets
             verify.go            /2/users/me
  linkedin/  client.go            Posts API, image upload, text posts
             me.go                /v2/userinfo (OIDC) for person URN
             verify.go            Token verification
  setup/     wizard.go            Multi-step setup wizard (788 lines)
  store/     store.go             JSON persistence (data/config.json)
  agent/     client.go            Agent webhook for caption rewriting
  n8n/       client.go            n8n webhook client
```

---

## Quick Start

### Prerequisites

- **Go 1.21+** (built with 1.25)
- A **Telegram Bot Token** from [@BotFather](https://t.me/BotFather)

### Build & Run

```bash
git clone https://github.com/zahidoverflow/PostXLinkedin.git
cd PostXLinkedin/PostXLinkedInbot

# Build
go build -o postxlinkedinbot ./cmd/postxlinkedinbot

# Configure (only TELEGRAM_BOT_TOKEN is required to start)
cp .env.example .env
nano .env   # set TELEGRAM_BOT_TOKEN

# Run
./postxlinkedinbot
```

Open your bot in Telegram and send `/start` — the wizard takes care of the rest.

### Run with systemd (VPS)

```bash
sudo ./scripts/vps/install.sh
sudo nano /opt/PostXLinkedin/PostXLinkedInbot/.env   # set TELEGRAM_BOT_TOKEN
sudo systemctl start postxlinkedinbot
sudo journalctl -u postxlinkedinbot -f
```

Update after pulling new code:

```bash
sudo ./scripts/vps/update.sh
```

---

## Setup Wizard

Send `/start` (or `/setup`) in Telegram. The wizard walks you through:

| Step | What it does |
|------|-------------|
| **Lock to chat** | Restrict the bot to your private chat |
| **Choose mode** | Direct API calls or n8n webhook |
| **Pick platforms** | X + LinkedIn, X only, or LinkedIn only |
| **X auth** | OAuth 2.0 PKCE flow (bot generates the URL) or paste a token directly |
| **LinkedIn auth** | Paste your access token; the bot auto-detects your profile URN |
| **Agent** | Optionally connect an AI webhook for caption rewriting |
| **Done** | Config is saved to `data/config.json` — start posting! |

All tokens are validated in real-time during setup. You'll see your account name/username as confirmation.

---

## Getting API Tokens

### X (Twitter) — OAuth 2.0

1. [Sign up for X Developer](https://developer.x.com/en/portal/petition/essential/basic-info) (Free tier)
2. [Create a Project + App](https://developer.x.com/en/portal/dashboard)
3. App Settings → **User authentication settings** → Set up OAuth 2.0:
   - Permissions: **Read and write**
   - Type: **Confidential client** (Web App)
   - Callback URL: `https://127.0.0.1/callback`
4. Copy your **Client ID** and **Client Secret**
5. During `/setup`, choose **"OAuth flow"** — the bot generates an auth URL, handles PKCE S256, and exchanges the code for tokens automatically

> The bot auto-refreshes X tokens before every post using the stored refresh token.

📖 [X OAuth 2.0 PKCE Docs](https://docs.x.com/resources/fundamentals/authentication/oauth-2-0/authorization-code)

### LinkedIn — Access Token

1. [Create a LinkedIn App](https://www.linkedin.com/developers/apps/new) (needs a Company Page — [create one](https://www.linkedin.com/company/setup/new/) if needed)
2. **Products** tab → Request **"Share on LinkedIn"** + **"Sign In with LinkedIn using OpenID Connect"**
3. [LinkedIn Token Generator](https://www.linkedin.com/developers/tools/oauth/token-generator) → Select your app → Check `openid`, `profile`, `w_member_social` → **Request access token**
4. Paste the token during `/setup` — the bot verifies it and detects your profile URN automatically

> ⚠️ LinkedIn tokens expire in ~60 days. Run `/setup` again to refresh.

📖 [LinkedIn Posts API](https://learn.microsoft.com/en-us/linkedin/marketing/community-management/shares/posts-api) · [API Versioning](https://learn.microsoft.com/en-us/linkedin/marketing/versioning)

---

## Bot Commands

| Command | Description |
|---------|-------------|
| `/start` | Welcome message + auto-starts setup if unconfigured |
| `/setup` | Launch the setup wizard (reconfigure anytime) |
| `/status` | Show current config summary (tokens redacted) |
| `/guide` | Step-by-step token instructions with links |
| `/cancel` | Cancel an in-progress setup wizard |
| `/ping` | Health check |
| `/version` | Show bot & API versions |
| `/help` | List all commands |

### Posting

- **Image post** — Send a photo with a caption
- **Text post** — Send a plain text message

The bot posts to all enabled platforms and reports individual results:

```
✅ Posted successfully!

✅ X: posted (ID: 1234567890)
✅ LinkedIn: posted (ID: urn:li:share:987654321)
```

---

## Environment Variables

| Variable | Required | Default | Description |
|----------|:--------:|---------|-------------|
| `TELEGRAM_BOT_TOKEN` | ✅ | — | Bot token from [@BotFather](https://t.me/BotFather) |
| `CONFIG_PATH` | | `data/config.json` | Path to persisted wizard config |
| `ALLOWED_CHAT_ID` | | `0` (any) | Restrict bot to one chat ID |
| `MAX_IMAGE_BYTES` | | `5000000` | Max image upload size |
| `DEBUG` | | `false` | Verbose logging |
| `LINKEDIN_VERSION` | | `202601` | LinkedIn API version header |
| `ENABLE_X` | | `true` | Enable X posting (direct mode) |
| `ENABLE_LINKEDIN` | | `true` | Enable LinkedIn posting (direct mode) |
| `X_API_BASE_URL` | | `https://api.x.com` | X API base URL |
| `N8N_WEBHOOK_URL` | | — | n8n webhook (enables n8n mode) |
| `N8N_SHARED_SECRET` | | — | Shared secret for n8n webhook auth |
| `AGENT_WEBHOOK_URL` | | — | AI agent webhook for caption rewriting |
| `AGENT_SHARED_SECRET` | | — | Shared secret for agent webhook |

> **Tip:** All token-related settings (X bearer, LinkedIn token, author URN, etc.) are managed by the Telegram setup wizard and persisted to `CONFIG_PATH`. You don't need to set them as env vars.

---

## Optional: AI Agent Webhook

Connect any HTTP endpoint to rewrite captions before posting. The bot sends:

```json
POST <AGENT_WEBHOOK_URL>
{
  "caption": "original caption text",
  "targets": ["x", "linkedin"]
}
```

Expected response:

```json
{
  "ok": true,
  "caption": "rewritten caption with #hashtags"
}
```

Use this with n8n, LangChain, OpenAI, or any custom service.

---

## Optional: n8n Mode

Instead of calling X/LinkedIn APIs directly, the bot can forward everything to an n8n webhook. Set `N8N_WEBHOOK_URL` in your `.env` or choose "n8n" during setup.

The bot sends a JSON payload with the image (base64), caption, and Telegram metadata. Your n8n workflow handles the actual posting.

---

## Project Structure

```
PostXLinkedInbot/
├── cmd/postxlinkedinbot/       # Entrypoint
│   └── main.go
├── internal/
│   ├── agent/                  # AI caption rewrite webhook client
│   ├── bot/                    # Config, update loop, command handlers
│   ├── linkedin/               # LinkedIn REST API client
│   ├── n8n/                    # n8n webhook client
│   ├── setup/                  # Interactive Telegram setup wizard
│   ├── store/                  # JSON config persistence
│   ├── telegram/               # Telegram Bot API wrapper
│   └── x/                      # X (Twitter) API v2 client
├── scripts/
│   ├── systemd/                # systemd service file
│   └── vps/                    # Install & update scripts
├── data/                       # Runtime data (gitignored)
├── .env.example                # Template env file
├── go.mod
└── README.md
```

---

## Security Notes

- **Tokens are stored in `data/config.json`** with `0600` permissions — this file is gitignored
- **`.env` is gitignored** — never commit credentials
- **Bot locks to your chat** during setup (recommended) so no one else can use it
- **Delete token messages** in Telegram after setup for extra safety
- The systemd service runs with `NoNewPrivileges`, `ProtectSystem=strict`, and `ProtectHome=true`

---

## License

MIT

---

<p align="center">
  Built by <a href="https://github.com/zahidoverflow">@zahidoverflow</a>
</p>

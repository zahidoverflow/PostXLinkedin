<p align="center">
  <img src="PostXLinkedInbot-logo.png" width="200" />
</p>

<h1 align="center">PostXLinkedInBot</h1>

<p align="center">
  <strong>Cross-post to X and LinkedIn from Telegram with ease.</strong>
</p>

---

## Highlights

- **Zero-config files** — The entire setup runs inside Telegram via an interactive wizard.
- **Dual platform posting** — X + LinkedIn from a single photo or text message.
- **OAuth 2.0 PKCE (S256)** — Secure X auth flow handled by the bot itself.
- **Auto token refresh** — X tokens are refreshed transparently before every post.
- **AI Agent webhook** — Optional hook to rewrite captions (hashtags, formatting, tone) via any HTTP endpoint.
- **VPS-ready** — systemd service file + install/update scripts included.

---

## 🚀 Beginner's Quick Start (VPS / Linux)

### Step 1: Get your Telegram Token
1. Message [@BotFather](https://t.me/BotFather) on Telegram.
2. Send `/newbot` and follow the instructions.
3. **Copy the API Token** it gives you.

### Step 2: One-Click Setup
Open your terminal and run:

```bash
git clone https://github.com/zahidoverflow/PostXLinkedin.git
cd PostXLinkedin/PostXLinkedInbot
sudo ./scripts/vps/install.sh
```

### Step 3: Configure
1. Edit your `.env` file: `sudo nano /opt/PostXLinkedin/PostXLinkedInbot/.env`
2. Paste your `TELEGRAM_BOT_TOKEN`.
3. Start the bot: `sudo systemctl start postxlinkedinbot`
4. Send `/start` to your bot in Telegram to begin the interactive setup.

---

## 🛠 Management Commands

| Task | Command |
|------|---------|
| **Check Status** | `sudo systemctl status postxlinkedinbot` |
| **Stop Bot** | `sudo systemctl stop postxlinkedinbot` |
| **Restart Bot** | `sudo systemctl restart postxlinkedinbot` |
| **View Logs** | `sudo journalctl -u postxlinkedinbot -f` |

---

## Setup Wizard Steps

The bot guides you through the following configuration steps:

1. **Lock to chat**: Restrict the bot to your private chat for security.
2. **Pick platforms**: Choose X + LinkedIn, X only, or LinkedIn only.
3. **X Auth**: Link your X account via the secure OAuth 2.0 flow.
4. **LinkedIn Auth**: Paste your access token (the bot auto-detects your URN).
5. **AI Agent**: (Optional) Connect a webhook for automated caption rewriting.

---

## Architecture

```
Telegram ──▶ PostXLinkedInBot ──┬──▶ X API v2        (OAuth 2.0 PKCE S256)
                                └──▶ LinkedIn API     (REST, version 202601)
                                     └──▶ Agent webhook    (optional caption rewrite)
```

---

## Security

- **Chat Locking**: We strongly recommend locking the bot to your specific Telegram `ChatID` during setup.
- **Token Redaction**: Tokens are never logged in plain text and are redacted in `/status` reports.
- **Local Storage**: All credentials are stored locally in `data/config.json` with `0600` permissions.

---

## License

MIT

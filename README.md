# PostXLinkedIn

A self-hosted Telegram bot that cross-posts to **X (Twitter)** and **LinkedIn** simultaneously.

[![Go Version](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Telegram](https://img.shields.io/badge/Telegram-Bot%20API-26A5E4?logo=telegram&logoColor=white)](https://core.telegram.org/bots/api)
[![X API](https://img.shields.io/badge/X%20API-v2-000000?logo=x&logoColor=white)](https://developer.x.com/)
[![LinkedIn API](https://img.shields.io/badge/LinkedIn%20API-v202601-0A66C2?logo=linkedin&logoColor=white)](https://learn.microsoft.com/en-us/linkedin/marketing/)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)

---

## Overview

PostXLinkedIn allows you to maintain your social presence on X and LinkedIn from a single Telegram interface. Send a photo with a caption or a plain text message, and the bot handles the rest — including OAuth 2.0 PKCE authentication for X and automated token refreshing.

## Features

- **Interactive Setup** — Configure everything (tokens, platforms, IDs) via a step-by-step wizard in Telegram.
- **Dual Posting** — Support for both X (Twitter) and LinkedIn from one message.
- **Image & Text** — Support for high-quality image uploads or text-only posts.
- **Secure Auth** — Implements OAuth 2.0 PKCE (S256) for X; no manual token refreshing needed.
- **AI Integration** — Optional "Agent" webhook to rewrite/optimize captions (hashtags, formatting) before posting.
- **VPS Ready** — Includes systemd service files and automated install/update scripts.

---

## Repository Structure

- `PostXLinkedInbot/` — The main Go application.
- `scripts/` — Installation and maintenance scripts for VPS deployment.

## Quick Start

1. **Telegram**: Create a bot with [@BotFather](https://t.me/BotFather) and get your token.
2. **Deploy**: Follow the instructions in [PostXLinkedInbot/README.md](PostXLinkedInbot/README.md) to install on your VPS or run locally.
3. **Configure**: Send `/start` to your bot in Telegram and follow the setup wizard.

---

## License

This project is licensed under the MIT License.

Built with ❤️ by [Zahid](https://github.com/zahidoverflow).

# PostXLinkedin

Telegram bot you can run on your VPS:

- You message it a **photo** with a **caption**
- It posts to your **X** and **LinkedIn**

The bot is written in Go (`PostXLinkedInbot/`). An optional `n8n/` folder is included if you prefer to route posting through n8n.

## Run (Direct Mode)

1. Set up a Telegram bot token with BotFather
2. Create API tokens for:
   - X: OAuth2 user token with scopes like `tweet.write` and `media.write`
   - LinkedIn: OAuth2 access token and your author URN (`urn:li:person:...` or org urn)
3. Configure `PostXLinkedInbot/.env` and run it

## Run (n8n Mode)

Set `N8N_WEBHOOK_URL` and implement/import an n8n workflow that accepts the webhook payload described in `n8n/README.md`.


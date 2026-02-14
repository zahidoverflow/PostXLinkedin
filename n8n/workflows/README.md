# Workflows

This repo includes the Go bot and a minimal n8n docker-compose.

If you want n8n to do the posting (instead of direct mode), create or import a workflow that:

1. Receives the webhook JSON described in `n8n/README.md`
2. Verifies the `X-PostXLinkedIn-Secret` header (if you set `N8N_SHARED_SECRET` in the bot)
3. Posts the image + caption to X and LinkedIn
4. Returns JSON:

```json
{ "ok": true }
```


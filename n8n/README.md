# n8n

This folder contains a minimal self-hosted n8n setup plus a workflow template.

## Run

```bash
docker compose up -d
```

## Import Workflow

- Import `n8n/workflows/postxlinkedin.json`
- Update the Webhook node path if you want (defaults to `/postxlinkedin`)
- Create credentials for:
  - X (OAuth2) used by the HTTP Request nodes
  - LinkedIn (OAuth2) used by the HTTP Request nodes
- Set variables inside the workflow:
  - `POSTXLINKEDIN_SECRET` must match the bot `N8N_SHARED_SECRET`
  - `LINKEDIN_AUTHOR_URN` e.g. `urn:li:person:YOUR_ID`

## Webhook Payload

The Go bot sends JSON like:

```json
{
  "caption": "hello",
  "image_base64": "...",
  "image_mime": "image/jpeg",
  "image_filename": "photo.jpg",
  "telegram": { "chat_id": 123, "message_id": 456, "from": { "id": 1 } }
}
```


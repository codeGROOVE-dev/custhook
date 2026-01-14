# custhook

GitHub Marketplace webhook handler for reviewGOOSE. Receives `marketplace_purchase` events and sends email notifications.

## Requirements

- Go 1.21+
- [ko](https://ko.build/) for deployment

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `WEBHOOK_SECRET` | Yes | GitHub webhook secret for HMAC-SHA256 verification |
| `BREVO_API_KEY` | No | Brevo API key (uses mock logger if unset) |
| `PORT` | No | Server port (default: 8080) |
| `MAIL_FROM` | No | Sender email (default: noreply@reviewGOOSE.dev) |
| `MAIL_NAME` | No | Sender name (default: reviewGOOSE) |

## Endpoints

- `GET /` - Health check
- `POST /webhook` - GitHub webhook receiver

## Development

```bash
make test    # Run tests with race detection
make lint    # Run linters
make build   # Build binary
```

## Deployment

```bash
make deploy  # Deploy to Cloud Run via ko
```

Requires `gcloud` CLI configured with appropriate project permissions.

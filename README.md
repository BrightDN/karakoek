# KaraKoek
-# Disclaimer: This project is heavily created through an AI-prompt. It works exactly as intended however I cannot take the credits for it. (Prompted using a tech doc)
-# This is just as a Proof of Concept.

Phone-first karaoke request site. Go + PostgreSQL + WebSockets, run with Docker Compose.

## Run

```sh
cp .env.example .env
# 1. cookie keys
openssl rand -base64 64   # -> SESSION_HASH_KEY
openssl rand -base64 32   # -> SESSION_BLOCK_KEY
# 2. admin password hash and table codes (needs Go, or run inside the built image)
go run ./cmd/server --hash-password 'your-password'   # -> ADMIN_PASSWORD_HASH (escape $ as $$ in .env for Compose)
go run ./cmd/server --gen-codes 20                     # -> TABLE_CODES
# 3. edit BASE_URL (must match the host you browse to), COOKIE_SECURE, DB password
docker compose up --build
```

Quick start without hashing: set `ADMIN_PASSWORD=...` (8+ chars) instead of `ADMIN_PASSWORD_HASH`.

## Pages

| URL | Who |
|---|---|
| `/?code=<tablecode>` | Guests (put this URL in each table's QR code: `BASE_URL/?code=...`) |
| `/admin/login` | Host login (not linked anywhere) |
| `/admin/queue` | Queue + remote control (phone/laptop) |
| `/admin/player` | Full-screen player on the venue display (open in a second tab/display) |

Click once on the player page after opening it; browsers may otherwise block scripted playback.
Nothing ever autoplays or auto-advances: the host presses Play on a row (cues the video), then Play on the control panel.

## Notes
- `BASE_URL` host must equal the host you use in the browser (Origin checks on POST/DELETE and WebSockets).
- Put Caddy/Traefik in front for HTTPS; set `TRUST_PROXY=true` so login rate limiting uses `X-Forwarded-For`.
- `/healthz` returns 503 when the database is unreachable.
- Queue state lives only in PostgreSQL; after a crash the queue is rebuilt and the current song returns cued and paused.

# PopcornVault

PopcornVault ingests M3U/IPTV playlists into PostgreSQL. It fetches a playlist URL, parses channels and groups (including EXTINF attributes and EXTVLCOPT headers), and stores them as **sources**, **groups**, and **channels**. You can run it as a **one-shot CLI** (ingest and exit) or as a **long-lived HTTP server** with an API to list, search, and refresh content.

## Prerequisites

- **Go** 1.24+
- **PostgreSQL** (migrations create the schema)

## Setup

1. **Database**

   Create a database and set its URL:

   ```bash
   # Required
   export DATABASE_URL="postgres://user:password@localhost:5432/popcornvault?sslmode=disable"
   ```

   If `DATABASE_URL` is not set, the app will try to load `.env.local` and `.env` from the current directory (see [Configuration](#configuration)).

2. **Migrations**

   Migrations run automatically on startup (server mode) or before each ingest (CLI). They are loaded from the `migrations` directory (relative to the current working directory, or next to the binary if not found). Ensure the `migrations` folder is present when you run the app.

3. **Build**

   ```bash
   go build -o popcornvault ./cmd/popcornvault
   ```

## Usage

### Server mode (24/7 API)

Run the HTTP server and keep it running:

```bash
./popcornvault -serve
```

Listens on port 8080 by default (set `SERVER_PORT` to change). Use the API to add sources, list/search channels, and refresh playlists. See [API](#api).

### One-shot ingest (CLI)

Ingest a single playlist and exit:

```bash
# Ingest (required: M3U URL)
./popcornvault "https://example.com/playlist.m3u"

# Optional source name (default: "m3u")
./popcornvault -name "My IPTV" "https://example.com/playlist.m3u"

# Use a config file instead of env
./popcornvault -config config.yaml "https://example.com/playlist.m3u"
```

Re-running with the same source name updates that source’s URL and re-ingests: existing channels and groups for that source are wiped and replaced.

### Flags

| Flag       | Description                                      |
|------------|--------------------------------------------------|
| `-serve`   | Run as HTTP server (no M3U URL required).        |
| `-name`    | Source name for this playlist (default: `m3u`).  |
| `-config`  | Path to YAML config file (overrides env).        |

## API

When running with `-serve`, the following endpoints are available.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Liveness check. Returns `{"status":"ok"}`. |
| GET | `/sources` | List all sources. |
| POST | `/sources` | Add and ingest a new source. Body: `{"name":"...", "url":"..."}`. |
| POST | `/sources/{id}/refresh` | Re-fetch the source’s M3U and replace all its channels. |
| GET | `/channels` | List/search channels. Query: `search`, `source_id`, `group_id`, `limit` (default 50, max 200), `offset`. |
| GET | `/groups` | List groups. Query: optional `source_id`. |

### Examples

```bash
# Health check
curl http://localhost:8080/health

# Add a source (fetches and ingests the M3U)
curl -X POST http://localhost:8080/sources \
  -H "Content-Type: application/json" \
  -d '{"name":"My IPTV","url":"https://example.com/playlist.m3u"}'

# List sources
curl http://localhost:8080/sources

# Refresh a source (re-fetch, update all channels)
curl -X POST http://localhost:8080/sources/1/refresh

# List channels (paginated)
curl "http://localhost:8080/channels?limit=50&offset=0"

# Search channels by name
curl "http://localhost:8080/channels?search=batman"

# Filter by source and group
curl "http://localhost:8080/channels?source_id=1&group_id=3"

# List groups (optionally for one source)
curl "http://localhost:8080/groups?source_id=1"
```

Channels response shape: `{"channels": [...], "total": N, "limit": 50, "offset": 0}`.

## Configuration

### Environment variables

| Variable              | Required | Description                          |
|-----------------------|----------|--------------------------------------|
| `DATABASE_URL`        | Yes      | PostgreSQL connection string.        |
| `SERVER_PORT`         | No       | HTTP server port (default: `8080`). |
| `FETCHER_USER_AGENT`  | No       | User-Agent for HTTP fetch (default: `PopcornVault/1.0`). |
| `FETCHER_TIMEOUT`     | No       | HTTP timeout, e.g. `30s` (default: `30s`). |

Copy `.env.example` to `.env.local` and adjust:

```bash
cp .env.example .env.local
# Edit .env.local
```

### Config file (YAML)

When using `-config`, the file must contain at least `database_url`. Example:

```yaml
database_url: "postgres://user:password@localhost:5432/popcornvault?sslmode=disable"
server_port: "8080"
user_agent: "PopcornVault/1.0"
timeout: "30s"
```

See `config.example.yaml` in the repo. Config file values override environment variables for that run.

## Schema (overview)

- **sources** — One per M3U URL (name, url, user_agent, last_updated, etc.).
- **groups** — Categories per source (e.g. `group-title` from M3U).
- **channels** — One per stream (name, url, media_type, group_id, source_id, favorite).
- **channel_http_headers** — Optional HTTP headers per channel (from EXTVLCOPT: referrer, user-agent, origin).

Migrations are in `migrations/`. They run automatically on server start or before each CLI ingest.

## License

See the repository for license information.

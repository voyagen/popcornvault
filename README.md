# PopcornVault

PopcornVault ingests M3U/IPTV playlists into PostgreSQL. It fetches a playlist URL, parses channels and groups (including EXTINF attributes and EXTVLCOPT headers), and stores them as **sources**, **groups**, and **channels** for later use.

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

   If `DATABASE_URL` is not set, the CLI will try to load `.env.local` and `.env` from the current directory (see [Configuration](#configuration)).

2. **Migrations**

   Migrations run automatically on each ingest. They are loaded from the `migrations` directory (relative to the current working directory, or next to the binary if not found). Ensure the `migrations` folder is present when you run the app.

3. **Build**

   ```bash
   go build -o popcornvault ./cmd/popcornvault
   ```

## Usage

```bash
# Ingest a playlist (required: M3U URL)
popcornvault "https://example.com/playlist.m3u"

# Optional source name (default: "m3u")
popcornvault -name "My IPTV" "https://example.com/playlist.m3u"

# Use a config file instead of env
popcornvault -config config.yaml "https://example.com/playlist.m3u"
```

**Flags**

| Flag       | Description                                      |
|------------|--------------------------------------------------|
| `-name`    | Source name for this playlist (default: `m3u`).  |
| `-config`  | Path to YAML config file (overrides env).        |

Re-running with the same source name updates that source’s URL and re-ingests: existing channels and groups for that source are wiped and replaced.

## Configuration

### Environment variables

| Variable              | Required | Description                          |
|-----------------------|----------|--------------------------------------|
| `DATABASE_URL`        | Yes      | PostgreSQL connection string.        |
| `FETCHER_USER_AGENT`  | No       | User-Agent for HTTP fetch (default: `PopcornVault/1.0`). |
| `FETCHER_TIMEOUT`     | No       | HTTP timeout, e.g. `30s` (default: `30s`).       |

Copy `.env.example` to `.env.local` and adjust:

```bash
cp .env.example .env.local
# Edit .env.local
```

### Config file (YAML)

When using `-config`, the file must contain at least `database_url`. Example:

```yaml
database_url: "postgres://user:password@localhost:5432/popcornvault?sslmode=disable"
user_agent: "PopcornVault/1.0"
timeout: "30s"
```

See `config.example.yaml` in the repo. Config file values override environment variables for that run.

## Schema (overview)

- **sources** — One per M3U URL (name, url, user_agent, last_updated, etc.).
- **groups** — Categories per source (e.g. `group-title` from M3U).
- **channels** — One per stream (name, url, media_type, group_id, source_id, favorite).
- **channel_http_headers** — Optional HTTP headers per channel (from EXTVLCOPT: referrer, user-agent, origin).

Migrations are in `migrations/` (e.g. `000001_init_schema.up.sql`). They run automatically before each ingest.

## License

See the repository for license information.

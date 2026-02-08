# PopcornVault

PopcornVault is a local backend for managing M3U/IPTV playlists. It provides a full REST API to add, update, search, and refresh IPTV sources and channels, backed by PostgreSQL. All playlist management happens through the API (or the built-in Swagger UI).

## Prerequisites

**With Docker (recommended):**

- **Docker** and **Docker Compose**

**Without Docker:**

- **Go** 1.24+
- **PostgreSQL** (migrations create the schema)

## Quick Start (Docker)

The fastest way to get running. Docker Compose starts both PostgreSQL (with pgvector) and the API server automatically.

1. **Create a `.env` file** (optional, for semantic search):

   ```bash
   cp .env.example .env
   # Edit .env and add your VOYAGE_API_KEY if you want semantic search
   ```

   You can skip this step -- the app runs fine without it.

2. **Start everything:**

   ```bash
   docker compose up --build
   ```

   This builds the Go binary inside a container, starts PostgreSQL with pgvector, runs migrations automatically, and starts the API server.

3. **Access the API:**

   - Health check: [http://localhost:8080/api/health](http://localhost:8080/api/health)
   - Swagger UI: [http://localhost:8080/api/docs](http://localhost:8080/api/docs)

To stop:

```bash
docker compose down
```

To stop and remove the database volume:

```bash
docker compose down -v
```

## Manual Setup

1. **Database**

   Create a database and set its URL:

   ```bash
   # Required
   export DATABASE_URL="postgres://user:password@localhost:5432/popcornvault?sslmode=disable"
   ```

   If `DATABASE_URL` is not set, the app will try to load `.env.local` and `.env` from the current directory (see [Configuration](#configuration)).

2. **Migrations**

   Migrations run automatically on startup. They are loaded from the `migrations` directory (relative to the current working directory, or next to the binary if not found). Ensure the `migrations` folder is present when you run the app.

3. **Build**

   ```bash
   go build -o popcornvault ./cmd/popcornvault
   ```

## Usage

Start the server:

```bash
./popcornvault
```

Or with a YAML config file:

```bash
./popcornvault -config config.yaml
```

Listens on port 8080 by default (set `SERVER_PORT` to change). All API routes are under the `/api` prefix. Interactive API documentation is available at [http://localhost:8080/api/docs](http://localhost:8080/api/docs) (Swagger UI).

### Flags

| Flag       | Description                                      |
|------------|--------------------------------------------------|
| `-config`  | Path to YAML config file (overrides env).        |

## API

All endpoints are prefixed with `/api`.

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/health` | Liveness check. Returns `{"status":"ok"}`. |

### Sources

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/sources` | List all sources. |
| POST | `/api/sources` | Add and ingest a new source. Body: `{"name":"...", "url":"..."}`. |
| GET | `/api/sources/{id}` | Get a single source by ID. |
| PATCH | `/api/sources/{id}` | Update source fields. Body (all optional): `{"name":"...", "url":"...", "user_agent":"...", "enabled":true}`. |
| DELETE | `/api/sources/{id}` | Delete a source and cascade-remove its channels and groups. Returns `204`. |
| POST | `/api/sources/{id}/refresh` | Re-fetch the source's M3U and replace all its channels. |

### Channels

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/channels` | List/search channels. Query params: `search`, `source_id`, `group_id`, `media_type` (0=Live, 1=Movie, 2=Serie), `favorite` (true/false), `limit` (default 50, max 200), `offset`. |
| GET | `/api/channels/{id}` | Get a single channel by ID. |
| PATCH | `/api/channels/{id}/favorite` | Set or unset a channel as favorite. Body: `{"favorite": true}`. |

### Groups

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/groups` | List groups. Query param: optional `source_id`. |

### Docs

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/docs` | Interactive Swagger UI for the API. |
| GET | `/api/docs/openapi.yaml` | Raw OpenAPI 3.0 specification. |

### Error responses

All errors return a consistent JSON envelope:

```json
{
  "status": 400,
  "error": "Bad Request",
  "detail": "invalid source_id: abc"
}
```

| Field    | Type   | Description                    |
|----------|--------|--------------------------------|
| `status` | int    | HTTP status code.              |
| `error`  | string | HTTP status text.              |
| `detail` | string | Human-readable error message.  |

### Examples

```bash
# Health check
curl http://localhost:8080/api/health

# Add a source (fetches and ingests the M3U)
curl -X POST http://localhost:8080/api/sources \
  -H "Content-Type: application/json" \
  -d '{"name":"My IPTV","url":"https://example.com/playlist.m3u"}'

# List sources
curl http://localhost:8080/api/sources

# Get a single source
curl http://localhost:8080/api/sources/1

# Update a source
curl -X PATCH http://localhost:8080/api/sources/1 \
  -H "Content-Type: application/json" \
  -d '{"name":"Renamed Source","enabled":false}'

# Delete a source
curl -X DELETE http://localhost:8080/api/sources/1

# Refresh a source (re-fetch, update all channels)
curl -X POST http://localhost:8080/api/sources/1/refresh

# List channels (paginated)
curl "http://localhost:8080/api/channels?limit=50&offset=0"

# Search channels by name
curl "http://localhost:8080/api/channels?search=batman"

# Filter by source and group
curl "http://localhost:8080/api/channels?source_id=1&group_id=3"

# Filter by media type (0=Live, 1=Movie, 2=Serie)
curl "http://localhost:8080/api/channels?media_type=1"

# List favorites only
curl "http://localhost:8080/api/channels?favorite=true"

# Get a single channel
curl http://localhost:8080/api/channels/42

# Favorite a channel
curl -X PATCH http://localhost:8080/api/channels/42/favorite \
  -H "Content-Type: application/json" \
  -d '{"favorite":true}'

# List groups (optionally for one source)
curl "http://localhost:8080/api/groups?source_id=1"
```

Channels response shape:

```json
{
  "channels": [],
  "total": 0,
  "limit": 50,
  "offset": 0
}
```

## Configuration

### Environment variables

| Variable              | Required | Description                          |
|-----------------------|----------|--------------------------------------|
| `DATABASE_URL`        | Yes      | PostgreSQL connection string.        |
| `SERVER_PORT`         | No       | HTTP server port (default: `8080`). |
| `FETCHER_USER_AGENT`  | No       | User-Agent for HTTP fetch (default: `PopcornVault/1.0`). |
| `FETCHER_TIMEOUT`     | No       | HTTP fetch timeout, e.g. `5m` (default: `5m`). |
| `VOYAGE_API_KEY`      | No       | VoyageAI API key for semantic search. Omit to disable. |
| `VOYAGE_MODEL`        | No       | VoyageAI model name (default: `voyage-3-lite`). |

**Local development:** copy `.env.example` to `.env.local` and adjust:

```bash
cp .env.example .env.local
# Edit .env.local
```

**Docker:** copy `.env.example` to `.env` and adjust. `DATABASE_URL` is set automatically by `docker-compose.yml`, so you can omit it from your `.env` file:

```bash
cp .env.example .env
# Edit .env — only VOYAGE_API_KEY and other optional vars are needed
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

- **sources** -- One per M3U URL (name, url, user_agent, last_updated, etc.).
- **groups** -- Categories per source (e.g. `group-title` from M3U).
- **channels** -- One per stream (name, url, media_type, group_id, source_id, favorite).
- **channel_http_headers** -- Optional HTTP headers per channel (from EXTVLCOPT: referrer, user-agent, origin).

Migrations are in `migrations/`. They run automatically on server start.

## Project structure

```
cmd/popcornvault/     Entry point (server startup)
internal/
  config/             Configuration loading (env, YAML, .env files)
  fetcher/            M3U fetching and parsing
  models/             Domain types (Source, Channel, Group, etc.)
  server/             HTTP server, route handlers, Swagger UI
  service/            Business logic (ingest orchestration)
  store/              Database interface and Postgres implementation
api/
  openapi.yaml        OpenAPI 3.0 specification
  embed.go            Embeds the spec into the binary
migrations/           SQL migration files (auto-applied on startup)
```

## License

See the repository for license information.

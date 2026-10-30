# Plexodus

A fast, self-hosted media server with a cinematic web UI, built for very large libraries on slow, cloud-backed storage (for example Google Drive mounted with rclone).

- **One binary.** Go server with the web UI embedded, plus SQLite. No external database.
- **Scans that don't touch your files.** Libraries are discovered by listing folders (many in parallel) and comparing each file's size and modified time with the database. Media files are never opened during a scan.
- **Direct play only.** Files are streamed byte-for-byte with range requests, never transcoded. When a browser can't decode a file, the player offers VLC, Infuse, a `.m3u` playlist or a copyable stream URL instead.
- **Instant browsing.** Everything you browse comes from indexed SQLite queries. Artwork is cached locally after first view.
- Movies and TV (show → season → episode), TMDB metadata with logos and episode stills, full-text search, watch state, Continue Watching and Up Next, Fix Match, multiple users.

## Quick start

```sh
make build            # builds web/dist and the ./plexodus binary (needs Go 1.24+ and Node 20+)
./plexodus -data ./data
```

Or with Docker:

```sh
docker build -t plexodus .
docker run -d --name plexodus -p 8484:8484 \
  -v /srv/plexodus:/data \
  -v /mnt/gdrive:/mnt/gdrive:ro \
  -e TMDB_API_KEY=... \
  plexodus
```

Open `http://<server>:8484`, create the admin account, then add libraries in **Settings**. Point each one at a folder such as `/mnt/gdrive/Movies`.

## rclone mount settings (important)

Plexodus finds new content by rescanning, every 15 minutes by default. That's cheap only if rclone answers folder listings from memory instead of calling the Drive API each time. Mount with a long directory cache, and let rclone's change polling keep that cache fresh:

```sh
rclone mount gdrive: /mnt/gdrive \
  --dir-cache-time 1000h \
  --poll-interval 15s \
  --vfs-cache-mode off \
  --read-only \
  --allow-other
```

With these flags, a rescan of tens of thousands of folders is served from rclone's memory and finishes in seconds to minutes. New files someone adds directly on Drive show up within one scan interval.

For playback, `--vfs-read-chunk-size 64M --vfs-read-chunk-size-limit 2G --buffer-size 64M` gives smooth seeking on high-bitrate files.

## Configuration

Flags override environment variables.

| Flag | Env | Default | |
|---|---|---|---|
| `-addr` | `PLEXODUS_ADDR` | `:8484` | Listen address |
| `-data` | `PLEXODUS_DATA` | `./data` | Database and artwork cache |
| `-tmdb-key` | `TMDB_API_KEY` | | TMDB v3 key or v4 read token (can also be set in Settings) |
| `-scan-interval` | `PLEXODUS_SCAN_INTERVAL` | `15m` | Periodic rescan of all libraries, one at a time (`0` disables) |
| `-scan-workers` | `PLEXODUS_SCAN_WORKERS` | `16` | Folders listed concurrently during a scan |
| `-probe-workers` | `PLEXODUS_PROBE_WORKERS` | `0` | Background ffprobe jobs. `0` means a file is only analysed when it's first played, which avoids reading every file on cloud storage |
| `-ffprobe` | `PLEXODUS_FFPROBE` | `ffprobe` | ffprobe binary (used to read codecs for the browser compatibility check) |

### Scanning a single folder

`POST /api/scan?path=<folder>` rescans just that folder, which suits scripts and download tools. Authenticate with an admin session, or with the API key from Settings (`?apikey=` or the `X-Api-Key` header).

### Skip unchanged folders

Each library has an opt-in **Skip unchanged folders** setting. When it's on, a folder is only re-listed if its modified time changed. Only enable it if your filesystem updates folder times when files are added. Local disks do; changes made directly on Google Drive may not. **Full scan** always lists everything.

## Safety

If a library root is missing or suddenly empty (for example the rclone mount dropped), the scan never removes anything under it. Files are only removed from the database when their folder was listed successfully and they're gone.

## Development

```sh
make dev     # Go server on :8484 proxying the Vite dev server (hot reload)
make test
```

Layout:

- `cmd/plexodus`: entry point and scan scheduler
- `internal/scanner`: folder walker and filename parsing
- `internal/metadata`: TMDB matching and artwork cache
- `internal/probe`: ffprobe
- `internal/api`: HTTP API, streaming, auth handlers
- `internal/db`: SQLite schema
- `web/`: React + TypeScript UI (Vite)

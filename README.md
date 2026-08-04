# deletewatchedarr

Automatically deletes media you've already watched from **Jellyfin**, via **Sonarr** and **Radarr** — at **episode** and **movie** granularity.

> Polls Jellyfin for watched items → unmonitors them in Sonarr/Radarr immediately → deletes their files after a configurable number of days.

Most "media cleanup" tools either delete whole shows only, require Plex + Tautulli, or are heavyweight rule-engine apps. `deletewatchedarr` is the boring thing a lot of people actually want: **"I watched it; get it off my disk, but keep the show tracked so it doesn't re-download."**

## What it does

On each poll (default every 60 minutes), for the configured Jellyfin user:

**Movies**
- *On watch* → unmonitor in Radarr (no more upgrade searches)
- *After `movie_retention_days`* → delete the file **and** remove the movie from Radarr

**Episodes**
- *On watch* → unmonitor that episode in Sonarr
- *After `episode_retention_days`* → delete the episode file from disk
- The **series and season stay in Sonarr** (unmonitored) — whole-show cleanup is left to you, since that's rare

Deletions are **idempotent**: items already without a file (or already removed) are skipped, so re-polling is a no-op and there's no database/state file to manage.

It deletes through the Sonarr/Radarr APIs, so it works with **any** downloader (Usenet/SABnzbd, qBittorrent, Deluge, …) — it does not touch your download client.

## Configuration

Settings live in a `config.yaml` (no secrets → safe to commit/share). API keys come from the environment.

`config.yaml`:

```yaml
jellyfin:
  url: http://jellyfin:8096
  username: admin
sonarr:
  url: http://sonarr:8989
radarr:
  url: http://radarr:7878
movie_retention_days: 2
episode_retention_days: 1
unmonitor_on_watch: true
interval_minutes: 60
dry_run: false
log_level: info
```

Environment variables (secrets):

| Variable | Required | Purpose |
|----------|----------|---------|
| `JELLYFIN_API_KEY` | yes | Jellyfin API key (Dashboard → API Keys) |
| `SONARR_API_KEY` | yes | Sonarr API key (Settings → General) |
| `RADARR_API_KEY` | yes | Radarr API key (Settings → General) |

## Run with Docker

```bash
docker run -d \
  --name deletewatchedarr \
  -v ./config.yaml:/config.yaml:ro \
  -e JELLYFIN_API_KEY=... \
  -e SONARR_API_KEY=... \
  -e RADARR_API_KEY=... \
  --restart unless-stopped \
  ghcr.io/honeybyhomo/deletewatchedarr:latest
```

Or with Docker Compose:

```yaml
services:
  deletewatchedarr:
    image: ghcr.io/honeybyhomo/deletewatchedarr:latest
    container_name: deletewatchedarr
    environment:
      - JELLYFIN_API_KEY=...
      - SONARR_API_KEY=...
      - RADARR_API_KEY=...
    volumes:
      - ./config.yaml:/config.yaml:ro
    restart: unless-stopped
```

Put the container on the same Docker network(s) as your Jellyfin/Sonarr/Radarr containers so the `url:` hostnames resolve.

## Safety

- **Start with `dry_run: true`.** It logs every unmonitor and deletion it *would* perform, changing nothing. Flip to `false` once the plan looks right.
- Everything is reversible-ish: Sonarr/Radarr have a recycle bin (enable it for a safety net); unmonitoring is trivially undone.
- The tool only ever acts on items **marked watched** for the configured Jellyfin user — never on unwatched media.

## Why not …?

| Tool | Why this exists instead |
|------|------------------------|
| Sanitarr | Deletes whole series only (no per-episode/per-movie-on-watch) |
| Deleterr | Requires Plex + Tautulli |
| Maintainerr | Capable but a heavyweight rule-engine + DB + web UI |
| OCDarr | Heavyweight beta app |
| Erasarr / Eyebleacharr | Feature-similar but very new / unmaintained |

## Development

```bash
go run . --config config.example.yaml   # needs real URLs/keys to do anything useful
go vet ./...
go test ./...
```

Images are published for `linux/amd64` and `linux/arm64` automatically on every `v*` tag via GitHub Actions.

## License

MIT

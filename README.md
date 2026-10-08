# ⚡ ModelTrack Go

A single static binary that watches **cloud LLM API providers** and announces when
a model appears, changes, or becomes **free** — posted to Discord.

A faithful Go port of [modeltrack](https://github.com/ylnodz/modeltrack). Same
providers, same rules, same SQLite schema, same embeds — no Python, no runtime,
no `uv sync` on boot.

[![CI](https://github.com/ylnodz/modeltrack-go/actions/workflows/ci.yml/badge.svg)](https://github.com/ylnodz/modeltrack-go/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.24+-00ADD8.svg)](https://go.dev/dl/)

---

## 🧲 Why a Go port exists

The Python version is correct and tested. This one exists for **deployment shape**:

| | Python | Go |
| :--- | :--- | :--- |
| Boot | `uv sync` then run | run one binary |
| Artifact | interpreter + venv | 18 MB static ELF |
| Runtime deps | libc, Python 3.11+ | none (`CGO_ENABLED=0`) |
| Cold start | seconds | milliseconds |
| Deploy | clone + sync per restart | `scp` binary, run |

If your host wipes the container on redeploy and re-installs dependencies every
boot, this is the one you want. Otherwise the Python one is fine.

---

## ✨ Behavior (identical to Python v2)

- **No re-spam.** State lives in SQLite with permanent history; a model is
  announced only on a real transition (`new`, `became_free`, `became_paid`,
  `price_change`, `context_change`, `deprecated`).
- **First cycle seeds silently** — an empty channel is never flooded.
- **Deprecation announced once**, never repeated every cycle.
- **Per-provider timeout** so one slow API cannot stall the cycle; providers are
  fetched concurrently with goroutines.
- **Retry with backoff + jitter** on `429`/`5xx`, honoring `Retry-After`.
- **Real per-provider free rules** — never a blanket `is_free: true` without a
  documented policy.

## ⚡ Quick start

```bash
git clone https://github.com/ylnodz/modeltrack-go.git && cd modeltrack-go
go build -o modeltrack ./cmd/modeltrack
cp .env.example .env   # fill in Discord credentials
./modeltrack
```

One-liner before first run, to see it work with no keys at all (OpenRouter's
catalog is public):

```bash
go run ./cmd/smoke
```

---

## ⚙️ Configuration

All settings come from the environment (see [`.env.example`](.env.example)):

| Variable | Required | Description |
| :--- | :---: | :--- |
| `DISCORD_BOT_TOKEN` | ✅ | Bot token from the [Developer Portal](https://discord.com/developers/applications) |
| `DISCORD_CHANNEL_ID` | ✅ | Channel where updates are posted (Snowflake ID) |
| `DISCORD_GUILD_ID` | | Guild id for instant slash-command sync |
| `DB_PATH` | | SQLite path (default `/home/container/modeltrack.db`) |
| `TRACK_INTERVAL_MINUTES` | | Cycle interval (default `30`) |
| `PROVIDER_TIMEOUT_SECONDS` | | Per-provider timeout (default `25`) |
| `ANNOUNCE_MENTION` | | Prefix became-free notices with `@here` |
| `PORT` | | Health endpoint port (default `8000`) |
| provider keys | | `OPENROUTER_API_KEY`, `GROQ_API_KEY`, `GEMINI_API_KEY`, … all optional |

> 💡 OpenRouter's `/models` endpoint is public — its key is optional. Every other
> provider is skipped until its key is present, and shows as `unconfigured`.

---

## 🛠️ Commands

| Command | Description |
| :--- | :--- |
| `/free` | One summary embed: free-model counts per provider |
| `/free provider:<name>` | Free models for one provider, paginated at 10 embeds with Prev/Next buttons |
| `/status` | Last cycle stats and per-provider status |

---

## 📡 Health endpoint

`GET /health`:

```json
{
  "status": "healthy",
  "last_run": 1791482661.26,
  "seconds_since_run": 12,
  "providers": {"openrouter": "ok (469)", "groq": "unconfigured"},
  "unconfigured": ["groq", "gemini"],
  "models_total": 469,
  "models_free": 19,
  "interval_minutes": 30
}
```

---

## 📦 Project structure

```text
cmd/modeltrack/       # entrypoint: config, store, tracker, bot, /health
cmd/smoke/            # live end-to-end check (needs network)
internal/
├── api/              # /health handler
├── bot/              # Discord session, slash commands, send queue, pager
├── config/           # env parsing
├── httpclient/       # retry / backoff / timeout
├── model/            # normalized schema + price / modality parsing
├── provider/         # Provider interface + 14 providers
├── render/           # embed builders
├── state/            # SQLite store + diff logic
└── tracker/          # fetch → diff → notify
```

---

## 🧪 Development

```bash
go build ./...
go vet ./...
go test ./... -race -count=1
gofmt -l .
go run ./cmd/smoke        # live network check
```

---

## 📄 License

MIT.

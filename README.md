# pcast

`pcast` is a local-first command-line podcast manager and player written in Go.

It subscribes to standard RSS/Atom podcast feeds, keeps a durable local episode catalog, reports episodes discovered since the previous check, and streams episodes through an installed media player.

Designed for humans **and** automation: deterministic behavior, stable local IDs, structured JSON, no prompts, documented exit codes, and strict stdout/stderr separation.

## Install

### Release binaries

Download a release archive for your platform from GitHub Releases, verify the SHA-256 checksum, and place `pcast` on your `PATH`.

### go install

```sh
go install github.com/Keldrik/pcast/cmd/pcast@latest
```

Requires Go 1.24+. Builds are CGO-free.

### From source

```sh
git clone <repo-url>
cd pcast-cli
go build -o pcast ./cmd/pcast
```

## Quick start

```sh
pcast add https://example.com/podcast.xml --name example
pcast list
pcast latest
pcast episodes example
pcast episode 42
pcast play 42
pcast mark 42 unplayed
pcast remove example
```

## Commands

| Command | Purpose |
| --- | --- |
| `pcast add <feed-url> [--name <alias>]` | Subscribe and baseline current episodes |
| `pcast remove <podcast>` | Delete a subscription and local episodes |
| `pcast list` | List subscriptions (local only) |
| `pcast latest [podcast]` | Check feeds and show newly discovered episodes |
| `pcast episodes [podcast] [flags]` | Query the cached catalog |
| `pcast episode <id>` | Show one episode in detail |
| `pcast play <id>` | Stream through an external player |
| `pcast mark <id> played\|unplayed` | Correct listening state |
| `pcast doctor` | Local diagnostics |
| `pcast version` | Build metadata |

Global flags:

- `--json` — machine-readable JSON on stdout (errors on stderr)
- `--data-dir <path>` — override storage location
- `--help`, `--version`

### Episode catalog flags

```text
pcast episodes [podcast] [--limit N] [--offset N] [--all]
                 [--played|--unplayed] [--new] [--query TEXT]
```

`--played` and `--unplayed` are mutually exclusive. `--all` cannot be combined with `--limit`/`--offset`.

### Playback flags

```text
pcast play <id> [--player EXE] [--player-arg ARG]... [--no-mark-played]
```

Player resolution order:

1. `--player`
2. `PCAST_PLAYER`
3. first of `mpv`, `vlc`, `ffplay` on `PATH`
4. platform opener (`open` / `xdg-open` / Windows URL handler)

The executable is invoked **directly** (never through a shell). Repeatable `--player-arg` values are passed literally before the enclosure URL.

Examples:

```sh
pcast play 42 --player mpv --player-arg=--audio-pitch-correction=no --player-arg=--speed=1.25
PCAST_PLAYER=vlc pcast play 42
```

## Selectors

Podcast selectors resolve exactly, in order:

1. Digits only → podcast ID
2. Value beginning with `http://` or `https://` → normalized feed URL (submitted or resolved)
3. Anything else → case-insensitive **alias**, then exact **title**
4. Multiple title matches → `ambiguous_selector` with candidate IDs

Aliases are trimmed, case-insensitively unique, non-empty, and cannot be digits-only.

Episode commands always take a numeric episode ID. Podcast and episode IDs are separate namespaces.

## Baseline and `latest` semantics

- On `add`, current feed items become the **baseline**. They appear in `episodes` but are **not** returned by the next `latest`.
- A **new** episode is one first discovered after the baseline, regardless of publication date.
- `latest` has **at-least-once** delivery: episodes are acknowledged only after output is written successfully. An interrupted process may repeat an episode; it should not silently lose one.
- Episodes that disappear from a remote feed are **never** deleted locally.
- If some feeds fail, `latest` still prints successful results, sets `partial: true`, writes diagnostics to stderr, and exits `4`.

## JSON API

Success envelope (stdout):

```json
{
  "schema_version": 1,
  "command": "list",
  "data": { }
}
```

Error envelope (stderr only; stdout empty):

```json
{
  "schema_version": 1,
  "error": {
    "code": "not_found",
    "message": "no podcast matches \"daily\"",
    "details": {}
  }
}
```

Field names are `snake_case`. Timestamps are UTC RFC3339 or `null`. Collections are always arrays.

### Exit statuses

| Status | Meaning |
| ---: | --- |
| 0 | Success (including empty results and idempotent ops) |
| 1 | Unexpected internal error or stdout write failure |
| 2 | Invalid command, argument, flag, or conflicting options |
| 3 | Not found or ambiguous selector |
| 4 | Network/feed failure; also partial `latest` |
| 5 | Storage, migration, lock, or local config failure |
| 6 | Player unavailable or playback failed |

Stable error codes include: `invalid_argument`, `not_found`, `ambiguous_selector`, `feed_unavailable`, `invalid_feed`, `storage_error`, `lock_unavailable`, `player_unavailable`, `player_failed`, `internal_error`.

## Data location

Resolution order:

1. `--data-dir`
2. `PCAST_HOME`
3. Platform default:
   - macOS: `~/Library/Application Support/pcast`
   - Linux: `$XDG_DATA_HOME/pcast` or `~/.local/share/pcast`
   - Windows: `%LOCALAPPDATA%\pcast`

Files:

```text
<data-dir>/pcast.db
<data-dir>/pcast.lock
```

**Backup:** copy `pcast.db` while no mutating command is running (or stop all `pcast` processes).  
**Reset:** delete the data directory.

Tests and automation should always set `--data-dir` or `PCAST_HOME` to a temporary path—never the user's real library.

## Privacy and network use

- Fully local: no accounts, telemetry, or cloud sync.
- Network is used only by `add`, `latest`, and media playback (the external player fetches the enclosure URL).
- Private/authenticated feeds are not supported in v1.
- URL userinfo is rejected. Sensitive-looking query keys are redacted in diagnostics.

## Player setup

Install one of `mpv`, `vlc`, or `ffplay`, or rely on the platform opener. Interactive controls work because foreground players inherit stdin/stderr. Player stdout is kept off `pcast` JSON stdout.

## Agent skill

An [Agent Skills](https://agentskills.io)-compatible skill lives at [`skills/pcast/`](skills/pcast/). It teaches agents how to drive `pcast` with `--json`, selectors, exit codes, and safe workflows.

**The skill never installs `pcast`.** It only runs the CLI when a `pcast` binary is already on `PATH`.

Install the skill into an agent that supports the open skills format (examples):

```sh
# from this repo
npx skills add ./skills/pcast

# or copy/symlink
ln -s "$(pwd)/skills/pcast" ~/.agents/skills/pcast
```

Project-local discovery paths are also linked at `.agents/skills/pcast` and `.cursor/skills/pcast`.

## Development

See [`agents.md`](agents.md) for contributor rules and [`project.md`](project.md) for the full behavior contract.

```sh
gofmt -w $(find . -name '*.go')
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...   # when installed
./scripts/smoke.sh ./pcast
```

Module path: `github.com/Keldrik/pcast`  
License: MIT

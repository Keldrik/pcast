# pcast — Project Specification

## 1. Overview

`pcast` is a local-first command-line podcast manager and player written in Go. It subscribes to standard RSS/Atom podcast feeds, keeps a durable local episode catalog, reports episodes discovered since the previous check, and streams episodes through an installed media player.

The CLI is designed for two equal audiences:

- **Humans:** concise commands, readable tables, useful defaults, and clear errors.
- **AI agents and scripts:** deterministic behavior, stable local IDs, structured JSON, no prompts, documented exit codes, and strict stdout/stderr separation.

A normal workflow is:

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

## 2. Product goals

1. Work entirely on the user's machine without an account, server, or daemon.
2. Make subscription management and listening possible from a terminal.
3. Make automation safe and predictable.
4. Preserve feed and listening state across runs in a transactional local database.
5. Handle ordinary RSS and Atom feeds, redirects, cache validators, and imperfect metadata.
6. Remain easy to install as one cross-platform binary.
7. Keep the first release small enough to implement and test thoroughly.

## 3. Non-goals for v1

The first release will not include:

- A TUI, graphical interface, discovery service, or recommendations.
- User accounts or cloud synchronization.
- An embedded audio decoder or audio-device integration.
- Download management, storage quotas, or offline retention policies.
- Playback-position tracking or cross-player resume support.
- Background polling, notifications, or a long-running daemon.
- Private/authenticated feeds.
- Queues, playlists, ratings, or social features.
- OPML import/export.

These are possible follow-up features, but none should complicate the v1 storage or command contracts prematurely.

## 4. Core concepts

### Podcast

A subscribed feed. Each podcast receives a stable, positive local integer ID. A podcast may also have a user-defined alias. Feed titles can change and do not have to be unique.

### Episode

A playable feed item with a usable HTTP(S) audio enclosure. Each episode receives a stable, positive local integer ID in a namespace separate from podcast IDs.

### Check

A network fetch of one feed. A check is successful after either a valid feed is parsed or the server returns `304 Not Modified`. Failed checks do not advance that podcast's last-successful-check state.

### New episode

An episode first discovered after a podcast's initial baseline was stored. Publication date does not determine newness: a newly discovered item with an old publication date is still new.

### Announced episode

A new episode successfully emitted by `pcast latest`. Pending episodes are acknowledged only after output is written successfully. This gives `latest` at-least-once delivery: a process interrupted at the final acknowledgement boundary can repeat an episode, but it should not silently lose one.

### Played episode

An episode explicitly marked played or successfully handed to/completed by the configured player according to the playback rules below. Users can correct the state with `pcast mark`.

## 5. Command-line contract

### 5.1 General syntax

```text
pcast [global flags] <command> [arguments] [command flags]
```

Global flags:

| Flag | Purpose |
| --- | --- |
| `--json` | Emit the documented machine-readable format. |
| `--data-dir <path>` | Override the directory containing the database and lock. |
| `--help` | Show command help. |
| `--version` | Show the short version. |

`--data-dir` takes precedence over `PCAST_HOME`, which takes precedence over the platform default.

General rules:

- Commands never prompt. A command either succeeds or returns a clear error.
- Command data goes to stdout; diagnostics go to stderr.
- Human output contains no required ANSI formatting. It remains useful when redirected.
- JSON mode never mixes prose into stdout.
- Dates in machine output are UTC RFC3339. Human output uses a consistent UTC representation.
- Empty list or query results are successful.
- Output order is deterministic.
- Network and database operations honor context cancellation and process interrupts.

### 5.2 Podcast selectors

Commands accepting `<podcast>` use exact, deterministic resolution:

1. Digits only: match podcast ID.
2. A value beginning with `http://` or `https://`: normalize it, then match the submitted or resolved feed URL.
3. Any other value: case-insensitively match an alias first, then a feed title.
4. Substring and fuzzy matching are not used.
5. Multiple title matches return `ambiguous_selector` and candidate IDs.

Aliases are trimmed, case-insensitively unique, non-empty, and cannot consist only of digits. These restrictions keep aliases distinct from ID selectors.

Episode-taking commands use an exact episode ID. The CLI always labels podcast and episode IDs clearly.

## 6. Commands

### 6.1 `pcast add <feed-url> [--name <alias>]`

Fetch, validate, and subscribe to a podcast feed.

Behavior:

1. Validate that the URL uses HTTP or HTTPS and has a host.
2. Normalize the URL for comparison without changing meaningful path or query data.
3. Follow a bounded redirect chain and retain the submitted and final resolved URLs.
4. Fetch and parse feed metadata and episodes.
5. Insert the podcast and current episode catalog in one transaction.
6. Treat current episodes as the initial baseline. They are visible in `episodes` but are not returned as new by the next `latest` call.
7. Print the podcast ID, title, canonical feed URL, and imported episode count.

Adding a feed that already matches a stored submitted or resolved URL is idempotent: return the existing podcast with `created: false` and exit `0`. It must not create duplicate subscriptions. If `--name` conflicts with another alias, fail without modifying state.

Examples:

```sh
pcast add https://example.com/feed.xml
pcast add https://example.com/feed.xml --name daily
pcast --json add https://example.com/feed.xml
```

### 6.2 `pcast remove <podcast>`

Remove a subscription and its locally stored episodes.

- Selector resolution is local and performs no network request.
- Podcast and episode rows are deleted transactionally.
- The command does not prompt because it changes local cached state only.
- A missing or ambiguous selector changes nothing and exits with the selector error code.
- Successful output identifies the removed podcast.

### 6.3 `pcast list`

List subscriptions from local state without fetching feeds.

Default human columns:

```text
ID  NAME  TITLE  EPISODES  UNPLAYED  LAST SUCCESS  LAST ERROR
```

JSON also includes submitted/resolved URLs, metadata, exact counts, timestamps, and last fetch status. Podcasts are ordered by ID ascending.

### 6.4 `pcast latest [podcast]`

Check one selected podcast or all subscriptions and return unannounced episodes discovered since prior successful checks.

Fetch behavior:

- Use saved `ETag` and `Last-Modified` values for conditional requests.
- Treat `304 Not Modified` as a successful check with no inserted episodes.
- With no selector, fetch feeds with bounded concurrency; the initial default is four workers.
- Update metadata for known podcasts and episodes without reporting metadata edits as new episodes.
- Never delete a local episode merely because it disappears from a remote feed.
- Save each feed's attempt time, success time, HTTP status, validators, and concise last error.
- A failed feed does not discard successful results from other feeds.

Announcement behavior:

1. Newly inserted post-baseline episodes start as pending.
2. `latest` includes all pending episodes in scope, including any left pending by an earlier output failure.
3. Prepare and write the complete response.
4. Only after successful output, transactionally mark returned episodes announced.
5. Serialize mutating commands with an application lock so concurrent `latest` processes cannot both claim the same pending rows during ordinary operation.

Order:

1. Podcast ID ascending.
2. Publication time descending, with unknown dates last.
3. Episode ID ascending as a final tie-breaker.

An empty result exits `0`. If some feeds fail, stdout still contains successful results and a `partial: true` marker; failures are included in the response, a concise diagnostic is sent to stderr, and the command exits `4`.

### 6.5 `pcast episodes [podcast] [flags]`

Query the cached episode catalog without a network request.

Flags:

| Flag | Behavior |
| --- | --- |
| `--limit <n>` | Maximum rows; default `20`. |
| `--offset <n>` | Rows to skip; default `0`. |
| `--all` | Return all matching rows; mutually exclusive with explicit pagination. |
| `--played` | Include only played episodes. |
| `--unplayed` | Include only unplayed episodes. |
| `--new` | Include only pending/unannounced episodes. |
| `--query <text>` | Case-insensitive title/description search over cached metadata. |

`--played` and `--unplayed` are mutually exclusive. With no podcast selector, search all subscriptions. Results are ordered by publication time descending, unknown dates last, then episode ID descending.

Human output includes episode ID, podcast, publication date, duration, played state, and title. JSON includes complete stored metadata and the enclosure URL.

Examples:

```sh
pcast episodes daily --unplayed
pcast episodes --query "compiler" --limit 10
pcast --json episodes --new --all
```

### 6.6 `pcast episode <episode-id>`

Show one cached episode in detail without fetching the feed.

Human output includes the podcast, title, publication date, duration, played state, description, and media URL. JSON returns all stored episode fields. A missing ID exits `3`.

### 6.7 `pcast play <episode-id> [flags]`

Stream an episode through an external player.

Flags:

| Flag | Behavior |
| --- | --- |
| `--player <executable>` | Override player discovery. |
| `--player-arg <arg>` | Pass one literal argument to the player; repeatable. |
| `--no-mark-played` | Do not update played state after successful playback/hand-off. |

Player resolution order:

1. `--player`.
2. `PCAST_PLAYER`.
3. The first installed executable among `mpv`, `vlc`, and `ffplay`.
4. A platform URL opener, where practical, as a convenience fallback.

The executable is invoked directly, never through a shell. Repeated `--player-arg` values are passed literally before the enclosure URL. A real player runs in the foreground with inherited stdin and stderr so interactive controls work. Player diagnostics must not contaminate `pcast` JSON stdout.

For a foreground player, an exit status of zero marks the episode played unless `--no-mark-played` was supplied. For a platform opener, successful hand-off marks it played even though completion cannot be observed. Failure or no available player exits `6` and leaves played state unchanged.

Player-specific controls such as speed belong in player arguments rather than `pcast` configuration.

### 6.8 `pcast mark <episode-id> <played|unplayed>`

Correct listening state without starting playback.

- `played` sets `played_at` to the current time.
- `unplayed` clears `played_at`.
- Manual changes do not alter historical `play_count`.
- Repeating the current state is idempotent and exits `0`.

### 6.9 `pcast doctor`

Run local, non-destructive checks useful for setup and automation diagnostics:

- Resolve and report the data directory.
- Verify that it can be created/read/written.
- Open the database and report schema version.
- Verify SQLite foreign-key configuration.
- Report which player would be selected.

Doctor must not fetch feeds or modify subscription/episode data. Expected missing optional players are warnings; unusable storage is an error. JSON exposes each check separately.

### 6.10 `pcast version`

Report semantic version, commit, build date, Go version, operating system, and architecture. Release builds inject version metadata with linker flags. JSON returns separate fields.

Cobra-provided help is part of v1. Generated shell completion may be exposed if it adds no custom maintenance burden.

## 7. Output and automation API

### 7.1 JSON success envelope

Every successful JSON response has a stable envelope:

```json
{
  "schema_version": 1,
  "command": "list",
  "data": {
    "podcasts": []
  }
}
```

Conventions:

- Field names use `snake_case`.
- IDs and durations are JSON numbers.
- Timestamps are UTC RFC3339 strings or `null` when unknown.
- Collections are arrays even when empty or containing one item.
- Command-specific content lives under `data`.
- New optional fields may be added compatibly. Breaking changes require a new `schema_version`.

A partial `latest` still emits a success-shaped result so callers can consume successful feeds:

```json
{
  "schema_version": 1,
  "command": "latest",
  "data": {
    "partial": true,
    "checks": [
      {"podcast_id": 1, "status": "ok", "http_status": 304, "new_count": 0},
      {"podcast_id": 2, "status": "failed", "new_count": 0}
    ],
    "episodes": [],
    "failures": [
      {"podcast_id": 2, "code": "feed_unavailable", "message": "request timed out"}
    ]
  }
}
```

### 7.2 JSON errors

If a command cannot produce its normal result, stdout remains empty and stderr receives one JSON document in JSON mode:

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

Messages help humans; `code`, exit status, and structured `details` are the automation contract. Errors are typed internally so transport, storage, parsing, and selector failures are not inferred from text.

### 7.3 Exit statuses

| Status | Meaning |
| ---: | --- |
| `0` | Success, including empty results and idempotent operations. |
| `1` | Unexpected internal error or stdout write failure. |
| `2` | Invalid command, argument, flag, or conflicting options. |
| `3` | Podcast/episode not found or selector ambiguous. |
| `4` | Network, HTTP, or feed parsing failure; also partial `latest`. |
| `5` | Storage, migration, lock, or local configuration failure. |
| `6` | Player unavailable or playback failed. |

Stable symbolic error codes include `invalid_argument`, `not_found`, `ambiguous_selector`, `feed_unavailable`, `invalid_feed`, `storage_error`, `lock_unavailable`, `player_unavailable`, `player_failed`, and `internal_error`.

## 8. Local configuration and storage

### 8.1 Data location

Resolution order:

1. Global `--data-dir`.
2. `PCAST_HOME`.
3. Platform default:
   - macOS: `~/Library/Application Support/pcast`
   - Linux/Unix: `$XDG_DATA_HOME/pcast`, else `~/.local/share/pcast`
   - Windows: `%LOCALAPPDATA%\pcast`

Files:

```text
<data-dir>/pcast.db
<data-dir>/pcast.lock
```

The directory is created with owner-only permissions where supported. Tests always use a temporary data directory.

### 8.2 SQLite

Use `database/sql` with `modernc.org/sqlite` so release builds remain CGO-free.

Database rules:

- Enable foreign keys on every connection.
- Use WAL mode and a bounded busy timeout.
- Use explicit transactions for multi-row state changes.
- Use monotonic embedded migrations tracked by `PRAGMA user_version`.
- Never modify a migration after release.
- Use `AUTOINCREMENT` IDs so deleted IDs are not silently reused.
- Use a small cross-platform application lock (for example `github.com/gofrs/flock`) around `add`, `remove`, `latest`, and state-changing episode commands. Read-only commands should not need the file lock.

### 8.3 Proposed schema

#### `podcasts`

| Column | Notes |
| --- | --- |
| `id` | Stable local integer primary key. |
| `feed_url` | Normalized submitted URL; unique. |
| `resolved_url` | Final normalized URL after redirects; indexed. |
| `alias` | Optional unique case-insensitive alias. |
| `title` | Current feed title. |
| `author` | Nullable feed author. |
| `description` | Nullable feed description. |
| `etag` | Nullable HTTP cache validator. |
| `last_modified` | Nullable HTTP cache validator. |
| `last_attempt_at` | Most recent attempted check. |
| `last_success_at` | Most recent successful parse or 304. |
| `last_http_status` | Nullable most recent HTTP status. |
| `last_error` | Nullable concise failure message. |
| `created_at` | Subscription creation time. |
| `updated_at` | Metadata update time. |

Do not require titles to be unique. Duplicate detection considers both submitted and resolved URLs at the application and transaction boundaries.

#### `episodes`

| Column | Notes |
| --- | --- |
| `id` | Stable local integer primary key. |
| `podcast_id` | Foreign key with cascade delete. |
| `identity_key` | Deterministic identity; unique per podcast. |
| `guid` | Nullable feed GUID. |
| `title` | Episode title. |
| `description` | Nullable summary/content. |
| `published_at` | Nullable publication time. |
| `duration_seconds` | Nullable parsed duration. |
| `enclosure_url` | HTTP(S) stream URL. |
| `media_type` | Nullable enclosure MIME type. |
| `media_length` | Nullable byte length. |
| `first_seen_at` | First successful discovery time. |
| `updated_at` | Most recent metadata update. |
| `announced_at` | Null until emitted by `latest`; baseline rows start non-null. |
| `played_at` | Null when unplayed. |
| `play_count` | Count of successful player launches/completions. |
| `last_played_at` | Most recent successful player result. |

Required indexes include `(podcast_id, identity_key)` unique, `(podcast_id, published_at)`, `announced_at`, and `played_at`.

Episode identity precedence:

1. A non-empty feed GUID.
2. The normalized enclosure URL.
3. A hash of stable available fields such as title, publication time, and enclosure metadata.

During upsert, also attempt to match existing rows by GUID or enclosure URL before insertion. Feed identities are imperfect; this behavior must be isolated and fixture-tested.

## 9. Feed fetching and parsing

Use `github.com/mmcdole/gofeed` unless implementation research finds a concrete blocker.

HTTP policy:

- Permit only HTTP and HTTPS feed/enclosure URLs.
- Reject malformed URLs and URLs containing embedded credentials.
- Allow localhost and private-network feeds because this is a user-operated local tool.
- Use an approximately 30-second request timeout.
- Follow at most five redirects.
- Limit the decompressed feed body to approximately 10 MiB.
- Send a `pcast/<version>` user agent.
- Respect standard proxy environment variables.
- Send `If-None-Match` and `If-Modified-Since` when saved values exist.
- Save cache validators only after successful handling.
- Record an attempt and concise error on HTTP or parse failure without replacing the previous successful state.

Parsing policy:

- Support common RSS 2.0 and Atom feeds.
- Require a usable feed title; allow a valid feed with zero episodes.
- Prefer `audio/*` enclosures.
- Accept a missing or generic MIME type when the enclosure URL is otherwise usable.
- Fall back to another enclosure when feeds are mislabeled.
- Ignore entries without a usable HTTP(S) enclosure.
- Normalize parsed timestamps to UTC while retaining unknown values as null.
- Parse common podcast duration formats into seconds; retain unknown duration as null.
- Update existing metadata but do not overwrite useful values with absent feed fields.

## 10. Playback adapter

`pcast` deliberately delegates decoding, audio output, and player controls to existing software. The player package is responsible only for:

- Resolving an executable safely.
- Constructing an argument vector without shell interpolation.
- Starting and waiting for the process with context cancellation.
- Keeping the player's output away from command-data stdout.
- Returning a typed success/failure result to the application layer.

This keeps the binary portable and avoids codec licensing, device APIs, and large dependencies. Progress/resume is not promised because behavior differs across external players.

## 11. Architecture

Recommended repository layout:

```text
.
├── cmd/pcast/main.go              # composition and process exit boundary
├── internal/
│   ├── app/                       # use cases, interfaces, typed errors
│   ├── cli/                       # Cobra commands and renderers
│   ├── feed/                      # HTTP, normalization, feed parsing
│   ├── model/                     # domain values and result types
│   ├── player/                    # executable discovery/process adapter
│   ├── platform/                  # data paths and application lock
│   └── store/
│       ├── sqlite.go              # database adapter
│       ├── migrations.go          # migration runner
│       └── migrations/            # embedded SQL migrations
├── testdata/                      # RSS/Atom and output fixtures
├── project.md
├── todo.md
├── agents.md
├── README.md
├── go.mod
└── go.sum
```

Dependency direction:

```text
cmd/pcast
  -> internal/cli
      -> internal/app
          -> internal/model + narrow ports
              <- store, feed, player, and platform adapters
```

Responsibilities:

- `main.go` wires concrete dependencies and is the only place that translates the final error to process exit.
- CLI handlers parse arguments, call one application use case, and render its result. They contain no SQL or feed logic.
- Application services own sequencing, lock scope, transaction intent, and cross-adapter behavior.
- The store owns SQL and transaction implementation.
- Feed and player packages are replaceable adapters for tests.
- `context.Context` crosses network, database, lock, and process boundaries.
- Inject the clock where discovery, announcement, and played timestamps affect behavior.
- Define interfaces only at real substitution boundaries; do not create a generic repository framework.

Expected direct dependencies:

- `github.com/spf13/cobra`
- `github.com/mmcdole/gofeed`
- `modernc.org/sqlite`
- `github.com/gofrs/flock` or an equally small cross-platform lock

Avoid an ORM, Viper, a logging framework, and an embedded media library in v1.

## 12. Reliability, safety, and privacy

- Never invoke a shell with a feed URL, title, player name, or player argument.
- Use parameterized SQL exclusively.
- Bound network duration, redirect count, response size, and all-feed concurrency.
- Preserve local history when remote items vanish.
- Use transactions for add/remove/upsert/acknowledgement operations.
- A broken feed cannot corrupt or roll back successful checks for unrelated feeds.
- Interruptions should leave the database consistent and pending announcements recoverable.
- Reject URL user-info. Avoid echoing potential secret query values in diagnostics unless the user explicitly requested stored data.
- Do not send telemetry.
- Do not make hidden network requests: only `add`, `latest`, and playback media access use the network.

## 13. Testing strategy

### Unit tests

- URL normalization and rejection.
- Selector precedence and ambiguity.
- Episode identity and enclosure selection.
- Duration and timestamp parsing.
- Deterministic sorting and pagination.
- Typed error-to-exit-status mapping.
- Human renderers and JSON schema behavior.
- Player resolution and literal argument construction.

### Store integration tests

Use a fresh temporary database/data directory for every test. Cover:

- Empty database migration and migration idempotency.
- Foreign keys, unique constraints, and cascade deletion.
- Duplicate feed prevention through original and resolved URLs.
- Episode upsert, metadata updates, and persistence after reopening.
- Baseline versus pending announcements.
- Output acknowledgement transactions.
- Played/unplayed state and play counters.
- Lock timeout and concurrent writer behavior.

### Feed/HTTP integration tests

Use `httptest.Server`, never public internet access. Cover:

- RSS and Atom feeds.
- Redirects and resolved URLs.
- ETag/Last-Modified requests and `304`.
- One newly added item after a baseline.
- A newly discovered item with an old publication date.
- Missing GUID, changed metadata, and mislabeled enclosures.
- Disappearing remote entries without local deletion.
- Timeouts, redirect limits, oversized bodies, malformed XML, and non-2xx responses.
- Partial failure when checking multiple feeds.

### CLI/end-to-end tests

Construct the root command in-process with injected streams and dependencies. Use a temporary real SQLite database, fixture HTTP server, fake clock, and helper subprocess for playback. Verify:

- Command syntax and argument validation.
- Human output golden files.
- Parsed JSON contracts rather than only string snapshots.
- Strict stdout/stderr separation.
- Stable exit statuses.
- No prompt on removal.
- Baseline-on-add and one-time `latest` output.
- Failed output leaves episodes pending.
- Partial latest preserves successful output.
- Playback never launches a real desktop player in tests.

Quality gates:

```sh
gofmt -w <changed-go-files>
go test ./...
go test -race ./...
go vet ./...
staticcheck ./...   # when installed/CI
```

## 14. Packaging and release

Before implementation, choose the canonical repository/module path, license, and minimum supported Go version. CI should test the minimum and current stable Go versions.

Release requirements:

- CGO-free Linux, macOS, and Windows binaries for `amd64` and `arm64` where supported.
- Version, commit, and build date injected by linker flags.
- GoReleaser configuration for archives and SHA-256 checksums.
- GitHub Actions (or equivalent) for tests, vet/static analysis, and tagged releases.
- Installation from a release archive and with `go install <module-path>/cmd/pcast@latest`.
- Every release archive includes README and license.
- A clean-data-directory smoke test runs the complete documented workflow before release.

Package-manager formulas can follow after the hosting URL and release cadence are stable.

## 15. Definition of v1 done

v1 is complete when a clean release binary can:

1. Add and baseline a real RSS or Atom feed.
2. List and exactly select subscriptions.
3. Detect each newly discovered episode without silently losing it.
4. Browse and search the cached catalog.
5. Inspect, play, and manually mark an episode.
6. Remove a subscription safely.
7. Produce stable human and JSON output with documented exit codes.
8. Recover cleanly from feed failures, output failures, and interrupted writes.
9. Pass unit, integration, race, vet, static-analysis, and cross-platform build checks.
10. Install and run without CGO or a service process.

## 16. Post-v1 candidates

Prioritize only after observing real usage:

- OPML import/export.
- Episode downloads with resumable transfers and retention rules.
- Queue and playlist commands.
- Shell completion packages.
- Authenticated/private feeds with secure credential handling.
- Background refresh and desktop notifications.
- Player-specific progress/resume integration.
- Transcript indexing and full-text search.
- Data export/backup and synchronization.
- Optional TUI built on the same application layer.

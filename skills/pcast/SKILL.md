---
name: pcast
description: >-
  Operate the local-first pcast podcast CLI for humans and automation. Use when
  the user wants to subscribe to RSS/Atom feeds, list podcasts, check for new
  episodes (latest), browse/search the episode catalog, inspect an episode,
  play audio, mark played/unplayed, remove a subscription, or run pcast doctor.
  Triggers include podcast, pcast, RSS feed subscribe, new episodes, play
  episode, unplayed, mark played. Requires an already-installed `pcast` binary
  on PATH — never install, upgrade, or download pcast.
license: MIT
compatibility: >-
  Requires the pcast CLI already installed and on PATH (or resolvable as
  `pcast`). Network only for add/latest/play media. Do not install pcast.
metadata:
  author: twiegold
  version: "1.0.0"
  cli: pcast
allowed-tools: Bash(pcast:*) Bash(which:pcast) Bash(command:pcast)
---

# pcast

Local-first podcast manager CLI. Stable IDs, JSON automation, no prompts.

## Hard rules

1. **Never install pcast.** Do not run `go install`, `brew install`, `curl | sh`, download releases, build from source, or otherwise provision the binary.
2. **Gate on presence.** Before any `pcast` command, verify the binary exists. If missing, stop and tell the user to install it themselves; do not work around with ad-hoc feed parsers unless they explicitly ask.
3. **Prefer `--json`** for every agent invocation. Parse stdout JSON on success; on failure stdout is empty and stderr is one JSON error object.
4. **Never prompt.** Commands never ask for confirmation — especially `remove`.
5. **Do not touch the user's real library** unless they asked to manage *their* podcasts. For experiments use `--data-dir <temp>` or `PCAST_HOME=<temp>`.
6. **No shell interpolation of URLs or titles** into eval/shell strings. Pass args as discrete argv (quoted).
7. **Podcast IDs ≠ episode IDs.** Always label which kind you mean.

## Prerequisite check (required first step)

```bash
command -v pcast >/dev/null 2>&1 && pcast --json version
```

- Exit `0` + JSON `command: version` → proceed.
- Not found → report that `pcast` is not installed/on PATH and stop. Offer only high-level self-install pointers from project docs if useful; **do not run install commands**.

Optional health check (local only, no feed fetch):

```bash
pcast --json doctor
```

## Invocation contract

```text
pcast [global flags] <command> [args] [command flags]
```

| Global flag | Purpose |
| --- | --- |
| `--json` | Machine JSON (use always as an agent) |
| `--data-dir <path>` | Override DB/lock directory (wins over env) |
| `--version` / `--help` | Short version / help |

Env: `PCAST_HOME` (data dir if no `--data-dir`), `PCAST_PLAYER` (player executable).

**Streams:** command data → stdout; diagnostics → stderr.  
**Empty lists succeed** (exit 0).  
**Dates in JSON:** UTC RFC3339 or `null`.

### Success envelope (stdout)

```json
{
  "schema_version": 1,
  "command": "list",
  "data": {}
}
```

### Error envelope (stderr only; stdout empty)

```json
{
  "schema_version": 1,
  "error": {
    "code": "not_found",
    "message": "…",
    "details": {}
  }
}
```

### Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | Success (incl. empty / idempotent) |
| 1 | Internal / stdout write failure |
| 2 | Invalid args/flags |
| 3 | Not found or ambiguous selector |
| 4 | Network/feed failure **or** partial `latest` |
| 5 | Storage / lock / config |
| 6 | Player missing or failed |

Stable `error.code` values: `invalid_argument`, `not_found`, `ambiguous_selector`, `feed_unavailable`, `invalid_feed`, `storage_error`, `lock_unavailable`, `player_unavailable`, `player_failed`, `internal_error`.

**Partial `latest`:** exit `4`, but stdout still has a success-shaped envelope with `data.partial: true`, `data.episodes`, `data.checks`, `data.failures`. Consume stdout; do not treat as total failure.

## Podcast selectors

Exact resolution only (no fuzzy/substring):

1. Digits only → podcast **ID**
2. `http://` / `https://` → normalized feed URL (submitted or resolved)
3. Else → case-insensitive **alias**, then exact **title**
4. Multiple titles → `ambiguous_selector` + candidate IDs in `details`

Aliases: non-empty, trimmed, case-insensitively unique, **not** digits-only.

Episode commands take a numeric **episode** ID only.

## Commands (JSON form)

### Subscribe

```bash
pcast --json add <feed-url>
pcast --json add <feed-url> --name <alias>
```

- Baselines current items: they appear in `episodes` but **not** in the next `latest`.
- Duplicate URL (submitted or resolved) is idempotent: `created: false`, exit 0.
- Alias conflict → error, no mutation.

### List subscriptions (local, no network)

```bash
pcast --json list
```

### Check for new episodes (network)

```bash
pcast --json latest
pcast --json latest <podcast>
```

Semantics:

- **New** = first discovered after baseline (publication date irrelevant).
- At-least-once: pending rows acknowledged only after successful output.
- Never deletes local episodes missing from the remote feed.
- `304 Not Modified` = successful empty check.
- Multi-feed failures → partial result + exit 4 (see above).

### Catalog (local, no network)

```bash
pcast --json episodes
pcast --json episodes <podcast>
pcast --json episodes --unplayed --limit 20
pcast --json episodes --query "compiler" --limit 10
pcast --json episodes --new --all
pcast --json episode <episode-id>
```

Flags: `--limit` (default 20), `--offset`, `--all` (exclusive with limit/offset), `--played` | `--unplayed` (exclusive), `--new` (pending/unannounced), `--query <text>` (case-insensitive title/description; wildcards are literal).

### Listening state

```bash
pcast --json mark <episode-id> played
pcast --json mark <episode-id> unplayed
```

Idempotent. Does **not** change `play_count`.

### Play

```bash
pcast --json play <episode-id>
pcast --json play <episode-id> --player mpv --player-arg=--speed=1.25
pcast --json play <episode-id> --no-mark-played
```

- Player order: `--player` → `PCAST_PLAYER` → `mpv`/`vlc`/`ffplay` → platform opener.
- Direct exec only (no shell). Blocks until foreground player exits.
- Success marks played unless `--no-mark-played`.
- Exit 6 on missing/failed player; state unchanged on failure.
- **Agents:** only run `play` when the user clearly wants audio now. Prefer listing/marking for headless automation. Player chatter must not be mixed into your reasoning as JSON.

### Remove (local, destructive to cache only)

```bash
pcast --json remove <podcast>
```

No prompt. Cascades local episodes. Confirm intent with the user if ambiguous.

### Diagnostics / version

```bash
pcast --json doctor
pcast --json version
```

`doctor` does not fetch feeds or mutate subscriptions.

## Recommended agent workflows

### “What’s new?”

```bash
pcast --json latest
```

Summarize `data.episodes` (id, podcast, title, published_at). Mention `partial` / `failures` if present.

### “Subscribe to this feed”

```bash
pcast --json add '<url>' --name '<alias-if-requested>'
```

Report podcast `id`, title, `created`, `episode_count`. Remind that current episodes are baselined (not “new”).

### “Find something to play”

```bash
pcast --json list
pcast --json episodes --unplayed --limit 10
# or
pcast --json episodes --query '<keywords>' --limit 10
```

Present episode **IDs** clearly; play only on explicit request.

### “Mark done / fix state”

```bash
pcast --json mark <id> played   # or unplayed
```

### Safe experimentation

```bash
pcast --data-dir /tmp/pcast-agent --json doctor
pcast --data-dir /tmp/pcast-agent --json add 'https://…'
```

## Parsing tips

- Read **stdout** for success and partial-latest payloads.
- Read **stderr** only when exit ≠ 0 **and** stdout is empty (hard errors). For exit 4 with latest JSON on stdout, prefer stdout.
- Use `data` under the envelope; ignore unknown fields (forward-compatible).
- Collections are always arrays (possibly empty).

## Network & privacy

| Command | Network? |
| --- | --- |
| add, latest | Yes (feeds) |
| play | Player fetches enclosure |
| list, episodes, episode, mark, remove, doctor, version | No |

No accounts, telemetry, or cloud sync. Rejected: credentialed URLs (`user:pass@`). Private feeds are out of scope for v1.

## Do not

- Install or build `pcast`
- Use human (non-JSON) output for automation decisions
- Fuzzy-match podcast names — resolve via `list` then ID/alias
- Delete remote-side data (remove is local cache only) without user intent
- Run `play` in CI/headless contexts unless requested and a player exists
- Point tests at the user's default data directory

## If something fails

| Symptom | Action |
| --- | --- |
| `pcast` not found | Stop; user must install; do not install for them |
| exit 3 / `not_found` | `list` or `episodes` and retry with ID |
| exit 3 / `ambiguous_selector` | Show `details.candidates`; ask which ID |
| exit 4 hard error (empty stdout) | Report `error.code` + message |
| exit 4 partial latest | Show successful episodes + failures |
| exit 5 | `doctor`; check `--data-dir` / permissions |
| exit 6 | `doctor` player check; suggest `--player` only if user wants playback |
| exit 2 | Fix flags (e.g. `--played` with `--unplayed`, `--all` with `--limit`) |

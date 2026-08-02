# pcast Build Plan

Work in phase order. A phase is complete only when its implementation, tests, documentation, and completion gate are satisfied. Do not begin dependent features on an unstable public or storage contract.

## Phase 0 — Confirm project decisions

- [x] Choose the canonical source-hosting URL and Go module path.
- [x] Choose and add a license.
- [x] Initialize Git and add a Go-appropriate `.gitignore`.
- [x] Choose the minimum supported Go version after checking core dependencies.
- [x] Review and approve `project.md` command semantics, JSON envelope, and exit statuses.
- [x] Keep post-v1 features outside the active implementation scope.

**Completion gate:** public behavior and project identity are settled before source code is initialized.

## Phase 1 — Scaffold the Go CLI

- [x] Run `go mod init <module-path>`.
- [x] Add Cobra and create `cmd/pcast/main.go`.
- [x] Create the package layout described in `project.md`.
- [x] Build a root command with injected stdin, stdout, stderr, environment, and clock.
- [x] Add global `--json` and `--data-dir` flags.
- [x] Implement root help, `--version`, and `pcast version` with development metadata.
- [x] Define typed application errors, stable symbolic codes, and exit-status mapping.
- [x] Ensure only `main.go` calls `os.Exit`.
- [x] Add Makefile or task commands only if they remain thin wrappers around Go tools.
- [x] Add CI for format checks, `go test ./...`, and `go vet ./...`.

Tests:

- [x] Root/help invocation.
- [x] Unknown command, missing argument, and invalid flag exit `2`.
- [x] Human and JSON error rendering.
- [x] Version output in human and JSON modes.
- [x] stdout and stderr separation.

**Completion gate:** the empty CLI builds on supported platforms and every failure follows the output/exit contract.

## Phase 2 — Define domain and application contracts

- [x] Add podcast, episode, feed-check, playback, and command-result types.
- [x] Represent optional times, duration, lengths, and HTTP values without ambiguous zero values.
- [x] Define narrow store, feed client, lock, player, and clock interfaces where tests require substitution.
- [x] Define selector and pagination/filter input types.
- [x] Centralize UTC timestamp and deterministic sorting rules.
- [x] Add representative RSS, Atom, malformed, and evolving-feed fixtures under `testdata/`.

Tests:

- [x] Result sorting with equal and missing publication times.
- [x] Validation of pagination and mutually exclusive filters.
- [x] Error wrapping preserves typed codes.

**Completion gate:** adapters can be implemented against stable use-case-oriented types rather than CLI-specific structs.

## Phase 3 — Implement paths, locking, and SQLite

- [x] Resolve data paths in order: `--data-dir`, `PCAST_HOME`, platform default.
- [x] Create the data directory with safe permissions where supported.
- [x] Add a cross-platform application lock with a bounded wait and context cancellation.
- [x] Open SQLite through `database/sql` and `modernc.org/sqlite`.
- [x] Enable foreign keys, WAL, and a bounded busy timeout correctly for every connection.
- [x] Embed and run monotonic migrations using `PRAGMA user_version`.
- [x] Add the initial `podcasts` and `episodes` schema, constraints, and indexes.
- [x] Implement database close and transaction helpers.
- [x] Implement podcast create/get/list/delete and duplicate URL lookup.
- [x] Implement exact selector queries for ID, alias, title, submitted URL, and resolved URL.
- [x] Implement episode baseline insert, upsert, query, pending selection, and acknowledgement.
- [x] Implement episode detail and played-state updates.
- [x] Implement persistence of fetch attempts, successful checks, validators, and errors.

Tests:

- [x] Every path precedence/default branch.
- [x] Empty database migration, reopen, and migration idempotency.
- [x] Foreign-key enforcement and cascade deletion.
- [x] Case-insensitive alias uniqueness and URL uniqueness.
- [x] Transaction rollback on partial failure.
- [x] Baseline and pending announcement round trips.
- [x] Announcement acknowledgement is atomic.
- [x] Played/unplayed and play-count behavior.
- [x] Lock contention, timeout, and cancellation.

**Completion gate:** all domain state round-trips through a temporary database and survives process restart without invariant violations.

## Phase 4 — Implement feed fetching and parsing

- [x] Validate and normalize submitted HTTP(S) feed URLs.
- [x] Reject unsupported schemes, missing hosts, fragments where inappropriate, and embedded credentials.
- [x] Configure proxy-aware HTTP with request timeout, redirect limit, body-size limit, and user agent.
- [x] Capture the final resolved URL.
- [x] Send stored ETag and Last-Modified validators.
- [x] Distinguish valid `304`, successful body, HTTP failure, timeout, and cancellation.
- [x] Parse RSS and Atom with `gofeed`.
- [x] Map feed metadata into domain types.
- [x] Select a usable audio enclosure with documented fallbacks.
- [x] Parse common podcast duration formats and normalize timestamps to UTC.
- [x] Generate stable episode identity keys.
- [x] Match by identity key, GUID, or enclosure URL before inserting a new row.
- [x] Preserve useful stored metadata when a later feed omits a field.

Tests with fixtures and `httptest.Server`:

- [x] URL normalization table tests.
- [x] RSS, Atom, zero-item, and missing-optional-field feeds.
- [x] Redirect and resolved URL behavior.
- [x] ETag, Last-Modified, and `304` behavior.
- [x] Preferred, generic, missing, and multiple enclosures.
- [x] GUID fallback and identity stability across metadata edits.
- [x] Old-dated item introduced in a later response.
- [x] Timeout, cancellation, oversized body, redirect loop, malformed XML, and non-2xx response.

**Completion gate:** an evolving local HTTP feed produces a baseline followed by exactly one genuine new episode.

## Phase 5 — Build selectors and subscription commands

- [x] Implement shared podcast selector parsing and exact resolution precedence.
- [x] Reject digit-only and conflicting aliases.
- [x] Implement `pcast add <feed-url> [--name]`.
- [x] Lock the complete add mutation and store podcast plus baseline in one transaction.
- [x] Make duplicate submitted/resolved URL adds idempotent.
- [x] Mark baseline episodes announced at add time.
- [x] Implement `pcast list` without network access.
- [x] Implement `pcast remove <podcast>` with transactional cascade deletion and no prompt.
- [x] Add human renderers with deterministic columns and order.
- [x] Add versioned JSON renderers for all three commands.

Tests:

- [x] ID, alias, exact title, submitted URL, and resolved URL selectors.
- [x] Alias precedence and ambiguous titles with candidate IDs.
- [x] Add a valid feed and a valid empty feed.
- [x] Invalid feed leaves no partial podcast row.
- [x] Duplicate direct and redirected URLs return `created: false`.
- [x] Alias conflict leaves state unchanged.
- [x] List counts/order and human golden output.
- [x] Remove cascade, missing selector, and ambiguous selector.
- [x] JSON output parses and contains no human prose.

**Completion gate:** a user can add, inspect, and remove subscriptions against a real temporary database with stable human and JSON behavior.

## Phase 6 — Build `latest`

- [x] Resolve one selected podcast or list all subscriptions.
- [x] Fetch all feeds with a configurable-in-code bounded worker count (default four).
- [x] Keep result ordering independent of goroutine completion order.
- [x] Persist each attempt, success, HTTP status, validators, metadata, and concise error correctly.
- [x] Insert newly discovered episodes as pending.
- [x] Update known episode metadata without re-announcing it.
- [x] Never delete episodes absent from the current feed.
- [x] Treat `304` as a successful empty check.
- [x] Query all pending episodes in scope after checks complete.
- [x] Build and write the complete human/JSON response before acknowledgement.
- [x] Acknowledge exactly the emitted episodes in one transaction after successful output.
- [x] Hold the application lock across claim/output/acknowledgement to prevent ordinary duplicate claims.
- [x] Return successful feed data when other feeds fail.
- [x] Implement `partial: true`, per-feed failures, stderr diagnostics, and exit `4`.

Tests:

- [x] `latest` immediately after `add` is empty.
- [x] One appended item is returned once and only once.
- [x] Newly discovered old-dated item is returned.
- [x] Metadata edits do not create or announce a new row.
- [x] `304` advances successful-check state without episodes.
- [x] Disappearing items remain in local history.
- [x] Partial multi-feed failure preserves and acknowledges successful output.
- [x] Total feed failure does not corrupt prior state.
- [x] A failed stdout writer leaves announcements pending.
- [x] Simulated failure near acknowledgement permits at-least-once retry.
- [x] Concurrent completion still yields deterministic order.
- [x] Lock contention prevents simultaneous pending claims.

**Completion gate:** repeated checks obey baseline, discovery, partial-failure, deterministic-output, and at-least-once announcement guarantees.

## Phase 7 — Build catalog, detail, search, and mark commands

- [x] Implement cached episode queries with podcast scope, limit, offset, and `--all`.
- [x] Implement played, unplayed, and pending filters with conflict validation.
- [x] Implement parameterized case-insensitive title/description search.
- [x] Implement `pcast episodes [podcast]` and its human/JSON renderers.
- [x] Implement `pcast episode <episode-id>` detail output.
- [x] Implement idempotent `pcast mark <episode-id> played|unplayed`.
- [x] Ensure read commands never fetch feeds or acquire the mutation lock unnecessarily.

Tests:

- [x] All filter combinations and invalid combinations.
- [x] Pagination boundaries, `--all`, empty results, and stable order.
- [x] Query text containing SQL wildcard and quote characters.
- [x] Cross-podcast versus selected-podcast results.
- [x] Missing episode IDs.
- [x] Manual state changes do not alter `play_count`.
- [x] Human output golden files and parsed JSON contracts.

**Completion gate:** users and agents can find a stable episode ID and inspect or correct its cached state without network access.

## Phase 8 — Build playback

- [x] Resolve players in documented flag/environment/default/opener order.
- [x] Validate explicit executable configuration and return typed errors.
- [x] Build literal argument vectors from repeated `--player-arg` values.
- [x] Start the player directly without a shell.
- [x] Run foreground players with context cancellation and inherited interactive input/diagnostics.
- [x] Keep player output away from command-data stdout.
- [x] Implement platform opener fallback behind a small adapter.
- [x] Update `played_at`, `last_played_at`, and `play_count` only after documented success.
- [x] Implement `--no-mark-played`.
- [x] Return exit `6` for missing or failed players.

Tests:

- [x] Player resolution precedence on each supported platform abstraction.
- [x] Exact argument vector, including spaces and hostile-looking URL/argument characters.
- [x] Successful, failed, and cancelled helper subprocesses.
- [x] State update after success and no update after failure.
- [x] Platform opener hand-off semantics.
- [x] JSON stdout remains valid and uncontaminated.
- [x] Tests never launch a real media player.

**Completion gate:** an episode selected from `episodes` can be safely streamed with one command and listening state follows documented outcomes.

## Phase 9 — Add diagnostics and harden the application

- [x] Implement non-destructive `pcast doctor` checks and JSON results.
- [x] Audit context cancellation across HTTP, locks, SQL, and player processes.
- [x] Audit every multi-row mutation and failure path for transaction safety.
- [x] Audit all SQL for parameterization.
- [x] Audit subprocesses to prove no shell invocation is possible.
- [x] Audit feed timeout, redirect, response-size, and concurrency bounds.
- [x] Redact sensitive URL components from diagnostics where appropriate.
- [x] Verify no unexpected command makes a network request.
- [x] Verify every command and typed error maps to the documented status.
- [x] Run `go test -race ./...` and resolve all races.
- [x] Add `staticcheck ./...` to CI and resolve findings.
- [x] Test macOS, Linux, and Windows builds in CI.

**Completion gate:** all quality checks pass, interruption tests preserve state, and security/reliability rules have explicit regression coverage.

## Phase 10 — Write user documentation

- [x] Add `README.md` with purpose, install methods, quick start, and command examples.
- [x] Document selector rules and the difference between podcast and episode IDs.
- [x] Document baseline-on-add and at-least-once `latest` semantics.
- [x] Document JSON envelopes, symbolic errors, and exit statuses.
- [x] Document player setup and player-argument examples.
- [x] Document data paths, `--data-dir`, `PCAST_HOME`, backup, and reset.
- [x] Document privacy, local-only state, and which commands use the network.
- [x] Ensure command help and README agree with `project.md`.
- [x] Add a contributor section linking to `agents.md` and the quality commands.

**Completion gate:** a new user can install and complete the primary workflow without reading source code.

## Phase 11 — Package and release v1

- [x] Add build variables for semantic version, commit, and build date.
- [x] Add GoReleaser configuration for CGO-free supported targets.
- [x] Add tagged-release CI and archive checksum generation.
- [x] Include README and license in archives.
- [x] Verify `go install <module-path>/cmd/pcast@latest` layout.
- [x] Build Linux, macOS, and Windows `amd64`/`arm64` binaries where supported.
- [x] Start each built binary and verify version output.
- [x] Run a clean-data-directory smoke test using local fixture services:
  - [x] `add`
  - [x] `list`
  - [x] `latest` before and after one feed change
  - [x] `episodes` search/filter
  - [x] `episode`
  - [x] `mark`
  - [x] `play` through a test player
  - [x] `doctor`
  - [x] `remove`
- [x] Repeat key smoke assertions with `--json`.
- [ ] Tag and publish v1 with checksums and release notes.

**Completion gate:** fresh release binaries complete the documented workflow without development tooling, CGO, a service, or access to user data.

## Post-v1 backlog (not part of the v1 completion gate)

- [ ] OPML import/export.
- [ ] Offline downloads, resume, verification, and retention policies.
- [ ] Queue and playlist support.
- [ ] Background checking and notifications.
- [ ] Authenticated/private feeds.
- [ ] Playback progress/resume integration.
- [ ] Transcript indexing.
- [ ] Backup/synchronization tooling.
- [ ] Optional TUI.

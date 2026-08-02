# Agent and Contributor Rules

- For *using* the installed CLI (subscribe, latest, play, etc.), load the portable skill at `skills/pcast/SKILL.md` (Agent Skills format). That skill must never install `pcast`; it only runs an existing binary.
- Treat `project.md` as the behavior contract and follow `todo.md` in phase order. Do not add post-v1 scope opportunistically.
- Run `gofmt`; prefer clear standard-library code and only the dependencies named in `project.md` unless a change is justified.
- Keep Cobra handlers thin. Put orchestration in `internal/app`, SQL in `internal/store`, HTTP/parsing in `internal/feed`, and processes in `internal/player`.
- Pass `context.Context` through network, database, lock, and process boundaries. Wrap errors with `%w`; never panic for expected failures. Only `cmd/pcast/main.go` may exit the process.
- Use small interfaces only at real adapter/test boundaries; avoid ORMs, generic repositories, globals, and hidden I/O.
- Preserve the CLI contract: stdout is data, stderr is diagnostics, JSON contains no prose, ordering is deterministic, empty results succeed, and commands never prompt.
- Preserve stable JSON fields, selectors, and exit codes. Invoke players directly—never through a shell.
- Use parameterized SQL, UTC timestamps, and transactions for multi-row changes. Never edit a released migration; add a new one.
- Never delete episodes because a feed dropped them. Acknowledge `latest` episodes only after successful output.
- Add tests for every behavior change. Use `httptest`, fixtures, temporary data directories, fake clocks, and helper player processes—never public feeds, the user's `PCAST_HOME`, or a real player.
- Before finishing, run `go test ./...`, `go test -race ./...`, `go vet ./...`, and `staticcheck ./...` when available. Update docs/help/output tests when public behavior changes.

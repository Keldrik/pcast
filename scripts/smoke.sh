#!/usr/bin/env bash
# Clean-data-directory smoke test against local fixture HTTP servers.
set -euo pipefail

PCAST_BIN="${1:-./pcast}"
if [[ ! -x "$PCAST_BIN" ]]; then
	echo "usage: $0 /path/to/pcast" >&2
	exit 2
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DATA="$(mktemp -d "${TMPDIR:-/tmp}/pcast-smoke.XXXXXX")"
trap 'kill ${SERVER_PID:-} 2>/dev/null || true; wait ${SERVER_PID:-} 2>/dev/null || true; rm -rf "$DATA"' EXIT

FEED_DIR="$DATA/feeds"
mkdir -p "$FEED_DIR"
cp "$ROOT/testdata/rss_basic.xml" "$FEED_DIR/feed.xml"

# Fixture server without conditional GET so content swaps are always visible.
PORT_FILE="$DATA/port"
python3 - "$FEED_DIR" "$PORT_FILE" <<'PY' &
import http.server, socketserver, sys, pathlib, os
root = pathlib.Path(sys.argv[1])
port_file = pathlib.Path(sys.argv[2])

class H(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *a, **k):
        super().__init__(*a, directory=str(root), **k)
    def log_message(self, *args):
        pass
    def send_head(self):
        # Bypass If-Modified-Since / 304 handling so smoke feed swaps always download.
        path = self.translate_path(self.path)
        try:
            f = open(path, "rb")
        except OSError:
            self.send_error(404, "File not found")
            return None
        fs = os.fstat(f.fileno())
        ctype = self.guess_type(path)
        self.send_response(200)
        self.send_header("Content-type", ctype)
        self.send_header("Content-Length", str(fs.st_size))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        return f

with socketserver.TCPServer(("127.0.0.1", 0), H) as httpd:
    port = httpd.server_address[1]
    port_file.write_text(str(port))
    httpd.serve_forever()
PY
SERVER_PID=$!

for _ in $(seq 1 50); do
	[[ -f "$PORT_FILE" ]] && break
	sleep 0.05
done
PORT="$(cat "$PORT_FILE")"
URL="http://127.0.0.1:${PORT}/feed.xml"

run() {
	"$PCAST_BIN" --data-dir "$DATA" "$@"
}

contains() {
	# Avoid SIGPIPE under pipefail from `cmd | grep -q`.
	local haystack="$1"
	local needle="$2"
	[[ "$haystack" == *"$needle"* ]]
}

echo "== version =="
run version
run --json version >/dev/null

echo "== doctor =="
run doctor

echo "== add =="
out="$(run add "$URL" --name smoke)"
echo "$out"
contains "$out" "created" || {
	echo "add missing created"
	exit 1
}
out="$(run --json add "$URL")"
contains "$out" '"created":false' || {
	echo "idempotent add failed: $out"
	exit 1
}

echo "== list =="
out="$(run list)"
contains "$out" "smoke" || {
	echo "list missing smoke: $out"
	exit 1
}

echo "== latest baseline empty =="
out="$(run latest)"
contains "$(echo "$out" | tr '[:upper:]' '[:lower:]')" "no new" || {
	echo "expected empty latest: $out"
	exit 1
}

echo "== evolve feed =="
cp "$ROOT/testdata/rss_one_more.xml" "$FEED_DIR/feed.xml"
out="$(run --json latest smoke)"
contains "$out" "Episode Three" || {
	echo "missing Episode Three: $out"
	exit 1
}

echo "== episodes =="
out="$(run episodes smoke --all)"
contains "$out" "Episode" || {
	echo "episodes: $out"
	exit 1
}
EP="$(run --json episodes smoke --limit 1 | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["episodes"][0]["id"])')"

echo "== episode $EP =="
out="$(run episode "$EP")"
contains "$out" "Episode" || {
	echo "episode: $out"
	exit 1
}

echo "== mark =="
run mark "$EP" played >/dev/null
run mark "$EP" unplayed >/dev/null

echo "== play (true as player) =="
if command -v true >/dev/null 2>&1; then
	run play "$EP" --player true
else
	echo "skip play (no true)"
fi

echo "== latest again empty =="
out="$(run latest smoke)"
contains "$(echo "$out" | tr '[:upper:]' '[:lower:]')" "no new" || {
	echo "expected empty latest: $out"
	exit 1
}

echo "== remove =="
run remove smoke >/dev/null
out="$(run list)"
contains "$(echo "$out" | tr '[:upper:]' '[:lower:]')" "no subscription" || {
	echo "list after remove: $out"
	exit 1
}

echo "SMOKE OK"

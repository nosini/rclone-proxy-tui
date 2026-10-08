#!/usr/bin/env bash
# End-to-end test: runs the daemon against a throwaway rclone config with a
# local remote and a crypt remote on top, then connects as a client over
# every protocol with rclone itself. Requires rclone and go on PATH.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
WORK=$(mktemp -d)
BIN="$WORK/rclone-proxy-tui"
DIR="$WORK/state"
CONF="$WORK/rclone.conf"
DATA="$WORK/data"
CLIENT_CONF="$WORK/client.conf"
BASE=$((20000 + RANDOM % 20000))
DAEMON_PID=""
export RCLONE_CACHE_DIR="$WORK/cache"
cleanup() {
	if [[ -n "$DAEMON_PID" ]]; then
		kill "$DAEMON_PID" 2>/dev/null || true
		wait "$DAEMON_PID" 2>/dev/null || true
	fi
	rm -rf "$WORK"
}
trap cleanup EXIT

pass() { echo "  ok   $*"; }
fail() { echo "  FAIL $*"; echo "--- daemon log ---"; cat "$DIR/daemon.log" || true; exit 1; }

(cd "$ROOT" && go build -o "$BIN" .)
mkdir -p "$DATA" "$WORK/plain" "$DIR"

# Upstream remotes: a plain one and a crypt wrapping it.
export RCLONE_CONFIG="$CONF"
rclone config create files alias remote="$DATA" --non-interactive >/dev/null
rclone config create "secret stuff" crypt remote=files:enc password=correct-horse --obscure --non-interactive >/dev/null
echo "top secret" >"$WORK/plain/s.txt"
rclone copy "$WORK/plain" "secret stuff:docs"
echo "hello" >"$DATA/hello.txt"
unset RCLONE_CONFIG

cat >"$DIR/state.json" <<EOF
{
  "version": 1,
  "rclone_binary": "$(command -v rclone)",
  "rclone_config": "$CONF",
  "bind_address": "127.0.0.1",
  "vfs_cache_mode": "writes",
  "protocols": {
    "webdav": {"enabled": true, "port": $BASE, "extra_flags": "--vfs-write-back 8s"},
    "sftp":   {"enabled": true, "port": $((BASE+1))},
    "s3":     {"enabled": true, "port": $((BASE+2))},
    "ftp":    {"enabled": true, "port": $((BASE+3))},
    "http":   {"enabled": true, "port": $((BASE+4))}
  },
  "clients": [
    {"id": "1", "name": "alice", "username": "alice", "password": "alicepw",
     "shares": [{"remote": "files"}, {"remote": "secret stuff"}]},
    {"id": "2", "name": "bob", "username": "bob", "password": "bobpw",
     "shares": [{"remote": "secret stuff", "path": "docs"}]}
  ]
}
EOF

"$BIN" --dir "$DIR" daemon >/dev/null 2>&1 &
DAEMON_PID=$!
for _ in $(seq 1 50); do
	if "$BIN" --dir "$DIR" status 2>/dev/null | grep -q "HTTP.*running"; then break; fi
	sleep 0.2
done
"$BIN" --dir "$DIR" status

ob() { rclone obscure "$1"; }
cat >"$CLIENT_CONF" <<EOF
[webdav]
type = webdav
url = http://127.0.0.1:$BASE/
vendor = rclone
user = alice
pass = $(ob alicepw)

[webdav-bob]
type = webdav
url = http://127.0.0.1:$BASE/
vendor = rclone
user = bob
pass = $(ob bobpw)

[webdav-bad]
type = webdav
url = http://127.0.0.1:$BASE/
user = alice
pass = $(ob wrong)

[sftp]
type = sftp
host = 127.0.0.1
port = $((BASE+1))
user = alice
pass = $(ob alicepw)
shell_type = none
known_hosts_file = none

[s3]
type = s3
provider = Rclone
endpoint = http://127.0.0.1:$((BASE+2))
access_key_id = alice
secret_access_key = alicepw

[s3-bad]
type = s3
provider = Rclone
endpoint = http://127.0.0.1:$((BASE+2))
access_key_id = alice
secret_access_key = nope

[ftp]
type = ftp
host = 127.0.0.1
port = $((BASE+3))
user = alice
pass = $(ob alicepw)

[http]
type = http
url = http://alice:alicepw@127.0.0.1:$((BASE+4))/
EOF
rc() { rclone --config "$CLIENT_CONF" --retries 1 --low-level-retries 1 --contimeout 5s "$@"; }

echo "Protocols:"
for r in webdav sftp s3 ftp http; do
	got=$(rc cat "$r:secret stuff/docs/s.txt" 2>"$WORK/err") || fail "$r: $(cat "$WORK/err")"
	[[ "$got" == "top secret" ]] || fail "$r read crypt: $got"
	got=$(rc cat "$r:files/hello.txt" 2>"$WORK/err") || fail "$r: $(cat "$WORK/err")"
	[[ "$got" == "hello" ]] || fail "$r read plain: $got"
	pass "$r reads plain and decrypted crypt files"
done
echo "upload" >"$WORK/up.txt"
rc copyto "$WORK/up.txt" "webdav:secret stuff/docs/up.txt" || fail "webdav upload"
[[ -z "$(ls "$DATA/enc" | grep -v -E '^[a-z0-9]+$' || true)" ]] || fail "upload not encrypted"
rc copyto "$WORK/up.txt" "sftp:files/up-sftp.txt" || fail "sftp upload"
sleep 7 # VFS write-back delay
[[ -f "$DATA/up-sftp.txt" ]] || fail "sftp upload missing"
pass "uploads through webdav (into crypt) and sftp"

echo "Isolation and auth:"
got=$(rc lsf webdav-bob: 2>"$WORK/err") || fail "bob: $(cat "$WORK/err")"
[[ "$got" == "secret stuff/" ]] || fail "bob sees: $got"
got=$(rc lsf "webdav-bob:secret stuff" 2>/dev/null)
[[ "$got" == *s.txt* && "$got" != *docs* ]] || fail "bob sub-folder: $got"
pass "bob only sees his remote, limited to the sub-folder"
rc lsf webdav-bad: >/dev/null 2>&1 && fail "wrong webdav password accepted"
rc lsf s3-bad: >/dev/null 2>&1 && fail "wrong s3 secret accepted"
pass "wrong passwords are refused"

echo "Upstream configuration reload:"
mkdir -p "$WORK/reconfigured"
cp -a "$DATA/." "$WORK/reconfigured/"
DATA="$WORK/reconfigured"
echo "updated" >"$DATA/hello.txt"
rclone --config "$CONF" config update files remote="$DATA" --non-interactive >/dev/null
for _ in $(seq 1 50); do
	got=$(rc cat webdav:files/hello.txt 2>/dev/null) || true
	[[ "$got" == "updated" ]] && break
	sleep 0.2
done
for r in webdav sftp s3 ftp http; do
	got=$(rc cat "$r:files/hello.txt" 2>"$WORK/err") || fail "$r reload: $(cat "$WORK/err")"
	[[ "$got" == "updated" ]] || fail "$r kept the old rclone config: $got"
done
pass "all protocols follow edits to rclone.conf"

echo "Revocation (with an upload still queued in the server's cache):"
head -c 2000000 /dev/urandom >"$WORK/big.bin"
rc copyto "$WORK/big.bin" "webdav-bob:secret stuff/big.bin" || fail "bob upload"
before=$(find "$DATA/enc" -type f | wc -l)
python3 - "$DIR/state.json" <<'EOF'
import json, sys
p = sys.argv[1]
s = json.load(open(p))
s["clients"] = [c for c in s["clients"] if c["username"] != "alice"]
json.dump(s, open(p + ".tmp", "w"))
EOF
mv "$DIR/state.json.tmp" "$DIR/state.json"
sleep 2
"$BIN" --dir "$DIR" status | grep -q "pending upload" || fail "daemon did not wait for the queued upload"
pass "restart waits for the queued upload"
for _ in $(seq 1 30); do
	"$BIN" --dir "$DIR" status | grep -q "pending upload" || break
	sleep 1
done
sleep 3
after=$(find "$DATA/enc" -type f | wc -l)
[[ $after -gt $before ]] || fail "queued upload was lost in the restart"
got=$(rc cat "webdav-bob:secret stuff/big.bin" 2>/dev/null | cmp - "$WORK/big.bin" && echo same)
[[ "$got" == "same" ]] || fail "uploaded file differs after restart"
pass "queued upload reached the (encrypted) storage intact"
for r in webdav sftp s3 ftp http; do
	rc lsf "$r:" >/dev/null 2>&1 && fail "$r: deleted client still has access"
done
rc lsf webdav-bob: >/dev/null 2>&1 || fail "bob lost access"
pass "deleted client is locked out on every protocol after draining, others keep working"

echo "all good"

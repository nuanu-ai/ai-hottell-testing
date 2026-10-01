#!/bin/sh
# Runs release.sh against a local fake of the Gitea release API and checks what it
# sent: the release, its tag and commit, the token, and the four uploaded files byte
# for byte. Also checks DRY_RUN and the failures. Nothing reaches a real Gitea.

set -eu

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/hottell-release-test.XXXXXX")
server_pid=
cleanup() {
	[ -z "$server_pid" ] || kill "$server_pid" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

failures=0
fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

token=test-token-not-a-secret
commit=0123456789abcdef0123456789abcdef01234567
mkdir -p "$work/bin" "$work/received"
printf 'fake arm64 binary\n' >"$work/bin/hottell-darwin-arm64"
printf 'fake amd64 binary\n' >"$work/bin/hottell-darwin-amd64"

# The fake answers like Gitea: 201 with the release, then 201 per uploaded asset.
# It saves the release request and every uploaded file under $work/received; a
# request without the expected token gets 401.
python3 -c '
import email.parser, email.policy, http.server, json, os, sys, urllib.parse
received, token, port_file = sys.argv[1], sys.argv[2], sys.argv[3]

class Handler(http.server.BaseHTTPRequestHandler):
    def reply(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        with open(os.path.join(received, "requests"), "a") as log:
            log.write(self.path + "\n")
        if self.headers.get("Authorization") != "token " + token:
            return self.reply(401, {"message": "token is required"})
        url = urllib.parse.urlparse(self.path)
        if url.path == "/api/v1/repos/alva/harness-telemetry/releases":
            body = self.rfile.read(int(self.headers["Content-Length"]))
            with open(os.path.join(received, "release.json"), "wb") as f:
                f.write(body)
            return self.reply(201, {"id": 7, "html_url": "http://fake/releases/7"})
        if url.path == "/api/v1/repos/alva/harness-telemetry/releases/7/assets":
            name = urllib.parse.parse_qs(url.query)["name"][0]
            body = self.rfile.read(int(self.headers["Content-Length"]))
            head = ("Content-Type: " + self.headers["Content-Type"] + "\r\n\r\n").encode()
            form = email.parser.BytesParser(policy=email.policy.HTTP).parsebytes(head + body)
            part = next(p for p in form.iter_parts() if p.get_param("name", header="content-disposition") == "attachment")
            with open(os.path.join(received, name), "wb") as f:
                f.write(part.get_payload(decode=True))
            return self.reply(201, {"id": 1, "name": name})
        self.reply(404, {"message": "not found"})

    def log_message(self, *args):
        pass

server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(port_file + ".tmp", "w") as f:
    f.write(str(server.server_address[1]))
os.rename(port_file + ".tmp", port_file)
server.serve_forever()
' "$work/received" "$token" "$work/port" 2>"$work/server.log" &
server_pid=$!

i=0
while ! [ -s "$work/port" ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || {
		echo "the fake Gitea did not start: $(cat "$work/server.log")" >&2
		exit 1
	}
	sleep 0.1
done
gitea_url="http://127.0.0.1:$(cat "$work/port")"

# release <version> [env assignments...]: runs release.sh, leaves its status in
# $status and its output in $work/out.
release() {
	version=$1
	shift
	status=0
	env GITEA_URL="$gitea_url" GITEA_REPO=alva/harness-telemetry TMPDIR="$work" "$@" \
		"$here/release.sh" "$version" "$commit" "$work/bin" "$work/dist" >"$work/out" 2>&1 || status=$?
}

reset_received() {
	rm -f "$work/received/"*
}

# A published release: the release request, then every asset, byte for byte.
reset_received
release 0.1.0 GITEA_TOKEN="$token"
if [ "$status" -ne 0 ]; then
	fail "publish: release.sh exited $status: $(cat "$work/out")"
fi
expected='{"tag_name":"hottell-v0.1.0","target_commitish":"'"$commit"'","name":"hottell-v0.1.0","draft":false,"prerelease":false}'
got=$(jq -c '{tag_name, target_commitish, name, draft, prerelease}' "$work/received/release.json" 2>/dev/null || true)
if [ "$got" != "$expected" ]; then
	fail "publish: release request $got, expected $expected"
fi
for asset in hottell-darwin-arm64 hottell-darwin-amd64 SHA256SUMS install.sh; do
	if ! cmp -s "$work/dist/$asset" "$work/received/$asset"; then
		fail "publish: $asset is not uploaded as prepared"
	fi
done
if ! cmp -s "$here/install.sh" "$work/received/install.sh"; then
	fail "publish: the uploaded install.sh differs from deployments/hottell/install.sh"
fi
if ! (cd "$work/bin" && shasum -a 256 -c -s "$work/received/SHA256SUMS"); then
	fail "publish: the uploaded SHA256SUMS does not match the binaries: $(cat "$work/received/SHA256SUMS")"
fi
if [ "$(grep -c '' "$work/received/SHA256SUMS")" -ne 2 ]; then
	fail "publish: SHA256SUMS must list exactly the two binaries: $(cat "$work/received/SHA256SUMS")"
fi
if ! grep -q "Published hottell-v0.1.0: http://fake/releases/7" "$work/out"; then
	fail "publish: no release link in the output: $(cat "$work/out")"
fi
if grep -q "$token" "$work/out"; then
	fail "publish: the token is printed"
fi
if [ -n "$(find "$work" -maxdepth 1 -name 'hottell-release.*')" ]; then
	fail "publish: the temporary directory with the token is left"
fi

# DRY_RUN prepares the files and sends nothing, with no token.
reset_received
release 0.1.0 DRY_RUN=1 GITEA_TOKEN=
if [ "$status" -ne 0 ]; then
	fail "dry run: release.sh exited $status: $(cat "$work/out")"
fi
if [ -e "$work/received/requests" ]; then
	fail "dry run: requests were sent: $(cat "$work/received/requests")"
fi
if ! [ -f "$work/dist/SHA256SUMS" ] || ! grep -q "DRY_RUN: would upload install.sh" "$work/out"; then
	fail "dry run: nothing prepared or planned: $(cat "$work/out")"
fi

reset_received
release 0.1.0 GITEA_TOKEN=
if [ "$status" -eq 0 ] || ! grep -q "GITEA_TOKEN is not set" "$work/out" || [ -e "$work/received/requests" ]; then
	fail "no token: expected a failure before any request, got status $status: $(cat "$work/out")"
fi

reset_received
release v0.1 GITEA_TOKEN="$token"
if [ "$status" -eq 0 ] || ! grep -q "is not X.Y.Z" "$work/out" || [ -e "$work/received/requests" ]; then
	fail "bad version: expected a failure before any request, got status $status: $(cat "$work/out")"
fi

reset_received
release 0.1.0 GITEA_TOKEN=wrong-token
if [ "$status" -eq 0 ] || ! grep -q "HTTP 401" "$work/out"; then
	fail "wrong token: expected a failure on HTTP 401, got status $status: $(cat "$work/out")"
fi

rm -f "$work/bin/hottell-darwin-amd64"
reset_received
release 0.1.0 GITEA_TOKEN="$token"
if [ "$status" -eq 0 ] || ! grep -q "hottell-darwin-amd64 is not built" "$work/out" || [ -e "$work/received/requests" ]; then
	fail "missing binary: expected a failure before any request, got status $status: $(cat "$work/out")"
fi

if [ "$failures" -ne 0 ]; then
	echo "release_test.sh: $failures failure(s)" >&2
	exit 1
fi
echo "release_test.sh: ok"

#!/bin/sh
# Runs install.sh the way a user does, `curl <origin>/install.sh | sh`, against a local
# HTTP server that serves the script and fake binaries the way the service does.
#
# uname and xattr are replaced on PATH, so each architecture is tried on any Mac; the
# fake binaries only record how they were run, so nothing outside a temporary
# directory is touched.

set -eu

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/hottell-install-test.XXXXXX")
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

mkdir -p "$work/www/download" "$work/fakebin" "$work/home" "$work/tmp"

# Fake binaries: each records its architecture, arguments, directory and how many
# bytes it could read from stdin.
for arch in arm64 amd64; do
	cat >"$work/www/download/hottell-darwin-$arch" <<EOF
#!/bin/sh
printf '%s|%s|%s|%s\n' "$arch" "\$*" "\$(basename "\$PWD")" "\$(wc -c | tr -d ' ')" >>"\$HOTTELL_TEST_LOG"
EOF
done
(cd "$work/www/download" && shasum -a 256 hottell-darwin-arm64 hottell-darwin-amd64 >SHA256SUMS)

cat >"$work/fakebin/uname" <<'EOF'
#!/bin/sh
case "$1" in
-s) echo Darwin ;;
-m) echo "$FAKE_MACHINE" ;;
*) exit 2 ;;
esac
EOF
# The quarantine attribute is reported present so that install.sh must remove it.
cat >"$work/fakebin/xattr" <<'EOF'
#!/bin/sh
echo "xattr $*" >>"$HOTTELL_TEST_XATTR_LOG"
EOF
chmod 755 "$work/fakebin/uname" "$work/fakebin/xattr"

port_file="$work/port"
python3 -c '
import functools, http.server, sys
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=sys.argv[1])
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
with open(sys.argv[2], "w") as f:
    f.write(str(server.server_address[1]))
server.serve_forever()
' "$work/www" "$port_file.tmp" 2>"$work/server.log" &
server_pid=$!
i=0
while ! [ -s "$port_file.tmp" ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || {
		echo "the local HTTP server did not start" >&2
		exit 1
	}
	sleep 0.1
done
origin="http://127.0.0.1:$(cat "$port_file.tmp")"

# The service substitutes its origin into the script it serves.
serve_script() {
	sed "s|__ORIGIN__|$1|" "$here/install.sh" >"$work/www/install.sh"
}

# run_install <machine> [<argument>...]: runs `curl <origin>/install.sh | sh -s --
# <argument>...` and leaves its exit status in $status and its output in $work/out.
run_install() {
	machine_arg=$1
	shift
	: >"$work/run.log"
	: >"$work/xattr.log"
	status=0
	curl -fsSL "$origin/install.sh" |
		env PATH="$work/fakebin:$PATH" HOME="$work/home" TMPDIR="$work/tmp" FAKE_MACHINE="$machine_arg" \
			HOTTELL_TEST_LOG="$work/run.log" HOTTELL_TEST_XATTR_LOG="$work/xattr.log" \
			sh -s -- "$@" >"$work/out" 2>&1 || status=$?
}

expect_failure() { # <case> <message part>
	if [ "$status" -eq 0 ]; then
		fail "$1: install.sh exited 0"
	fi
	if ! grep -qF "$2" "$work/out"; then
		fail "$1: output lacks \"$2\": $(cat "$work/out")"
	fi
	if [ -s "$work/run.log" ]; then
		fail "$1: the binary ran: $(cat "$work/run.log")"
	fi
}

expect_clean_tmp() { # <case>
	if [ -n "$(ls -A "$work/tmp")" ]; then
		fail "$1: the temporary directory is left: $(ls -A "$work/tmp")"
	fi
}

serve_script "$origin"

for pair in arm64:arm64 x86_64:amd64; do
	machine=${pair%%:*}
	arch=${pair#*:}
	run_install "$machine"
	if [ "$status" -ne 0 ]; then
		fail "$machine: install.sh exited $status: $(cat "$work/out")"
	fi
	# arch | arguments | run from install.sh's temporary directory | bytes of stdin
	if ! grep -qE "^$arch\|install\|hottell-install\.[^|]+\|0\$" "$work/run.log"; then
		fail "$machine: expected hottell-darwin-$arch run as ./hottell install with empty stdin, got: $(cat "$work/run.log")"
	fi
	if ! grep -q "^xattr -d com.apple.quarantine .*/hottell\$" "$work/xattr.log"; then
		fail "$machine: quarantine not removed, xattr calls: $(cat "$work/xattr.log")"
	fi
	expect_clean_tmp "$machine"
done

# The script's own arguments go to hottell install.
run_install arm64 --replace-foreign-hooks
if [ "$status" -ne 0 ] || ! grep -qE '^arm64\|install --replace-foreign-hooks\|[^|]+\|0$' "$work/run.log"; then
	fail "arguments: expected hottell install --replace-foreign-hooks, got status $status: $(cat "$work/run.log" "$work/out")"
fi

# Run from a file with the user's input on stdin, the binary still reads nothing.
: >"$work/run.log"
status=0
printf 'yes\n' |
	env PATH="$work/fakebin:$PATH" HOME="$work/home" TMPDIR="$work/tmp" FAKE_MACHINE=arm64 \
		HOTTELL_TEST_LOG="$work/run.log" HOTTELL_TEST_XATTR_LOG="$work/xattr.log" \
		sh "$work/www/install.sh" >"$work/out" 2>&1 || status=$?
if [ "$status" -ne 0 ] || ! grep -qE '^arm64\|install\|[^|]+\|0$' "$work/run.log"; then
	fail "stdin: expected hottell install run with empty stdin, got status $status: $(cat "$work/run.log" "$work/out")"
fi

run_install i386
expect_failure "unsupported architecture" "unsupported architecture i386"

cp "$work/www/download/SHA256SUMS" "$work/SHA256SUMS.good"
sed 's/^[0-9a-f]\{8\}/00000000/' "$work/SHA256SUMS.good" >"$work/www/download/SHA256SUMS"
run_install arm64
expect_failure "checksum mismatch" "checksum of hottell-darwin-arm64 does not match SHA256SUMS"
expect_clean_tmp "checksum mismatch"

grep -v 'hottell-darwin-amd64$' "$work/SHA256SUMS.good" >"$work/www/download/SHA256SUMS"
run_install x86_64
expect_failure "no SHA256SUMS line" "SHA256SUMS has no line for hottell-darwin-amd64"
cp "$work/SHA256SUMS.good" "$work/www/download/SHA256SUMS"

mv "$work/www/download/hottell-darwin-arm64" "$work/hottell-darwin-arm64"
run_install arm64
expect_failure "binary missing on the server" "cannot download $origin/download/hottell-darwin-arm64"
mv "$work/hottell-darwin-arm64" "$work/www/download/hottell-darwin-arm64"

cp "$work/www/download/hottell-darwin-arm64" "$work/hottell-darwin-arm64"
printf '#!/bin/sh\nexit 3\n' >"$work/www/download/hottell-darwin-arm64"
(cd "$work/www/download" && shasum -a 256 hottell-darwin-arm64 hottell-darwin-amd64 >SHA256SUMS)
run_install arm64
if [ "$status" -eq 0 ] || ! grep -qF "hottell install failed" "$work/out"; then
	fail "hottell install fails: expected install.sh to fail, got status $status: $(cat "$work/out")"
fi
expect_clean_tmp "hottell install fails"
mv "$work/hottell-darwin-arm64" "$work/www/download/hottell-darwin-arm64"
cp "$work/SHA256SUMS.good" "$work/www/download/SHA256SUMS"

serve_script "__ORIGIN__"
run_install arm64
expect_failure "origin not substituted" "the service address is not set"
serve_script "$origin"

if [ "$failures" -ne 0 ]; then
	echo "install_test.sh: $failures failure(s)" >&2
	exit 1
fi
echo "install_test.sh: ok"

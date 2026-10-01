#!/bin/sh
# Installs hottell on this Mac: curl -fsSL <service>/install.sh | sh
#
# Downloads the binary for this Mac's architecture and SHA256SUMS from the service,
# checks the checksum, removes the quarantine attribute and runs `hottell install`,
# which puts the binary in place and sets everything else up. Stops at the first
# error and never asks the user anything. The script's arguments go to
# `hottell install`: curl -fsSL <service>/install.sh | sh -s -- --replace-foreign-hooks
#
# The service replaces __ORIGIN__ with its public origin when it serves this script.

set -eu

origin='__ORIGIN__'

fail() {
	echo "hottell install.sh: $*" >&2
	exit 1
}

# The whole script is one function called on its last line, so a download cut off
# midway through `curl | sh` runs nothing.
main() {
	case "$origin" in
	http://* | https://*) ;;
	*) fail "the service address is not set in this script; download it from the service's /install.sh" ;;
	esac

	[ "$(uname -s)" = Darwin ] || fail "hottell runs on macOS only, this is $(uname -s)"

	machine=$(uname -m)
	case "$machine" in
	arm64) arch=arm64 ;;
	x86_64) arch=amd64 ;;
	*) fail "unsupported architecture $machine, expected arm64 or x86_64" ;;
	esac
	binary="hottell-darwin-$arch"

	for tool in curl shasum xattr; do
		command -v "$tool" >/dev/null 2>&1 || fail "$tool is not found"
	done

	dir=$(mktemp -d "${TMPDIR:-/tmp}/hottell-install.XXXXXX") || fail "cannot create a temporary directory"
	trap 'rm -rf "$dir"' EXIT
	trap 'exit 1' HUP INT TERM

	echo "Downloading $binary from $origin"
	curl -fsSL -o "$dir/$binary" "$origin/download/$binary" || fail "cannot download $origin/download/$binary"
	curl -fsSL -o "$dir/SHA256SUMS" "$origin/download/SHA256SUMS" || fail "cannot download $origin/download/SHA256SUMS"

	grep "  $binary\$" "$dir/SHA256SUMS" >"$dir/SHA256SUMS.$arch" || fail "SHA256SUMS has no line for $binary"
	(cd "$dir" && shasum -a 256 -c -s "SHA256SUMS.$arch") || fail "checksum of $binary does not match SHA256SUMS"

	mv "$dir/$binary" "$dir/hottell"
	chmod 755 "$dir/hottell"
	if xattr -p com.apple.quarantine "$dir/hottell" >/dev/null 2>&1; then
		xattr -d com.apple.quarantine "$dir/hottell" || fail "cannot remove the quarantine attribute"
	fi

	echo "Running hottell install"
	# Nothing is asked of the user: the binary gets no stdin, even when this script
	# runs from a terminal as `sh install.sh`.
	(cd "$dir" && ./hottell install "$@") </dev/null || fail "hottell install failed"
}

main "$@"

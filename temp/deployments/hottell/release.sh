#!/bin/sh
# Publishes a hottell release in Gitea; run through `task release VERSION=X.Y.Z`.
#
# Usage: release.sh <version> <commit> <bin-dir> <dist-dir>
#
# Collects hottell-darwin-arm64 and hottell-darwin-amd64 from <bin-dir>, install.sh
# and their SHA256SUMS in <dist-dir>, then creates the release hottell-v<version>
# with the tag of that name on <commit> through the Gitea API and uploads the four
# files to it.
#
# Environment:
#   GITEA_TOKEN  Gitea access token with write access to the repository; never stored
#   GITEA_URL    Gitea address, default https://git.alva.dev
#   GITEA_REPO   owner/name, default alva/harness-telemetry
#   DRY_RUN      when non-empty, prepares <dist-dir> and prints the requests without
#                sending them; no token is needed

set -eu

fail() {
	echo "release.sh: $*" >&2
	exit 1
}

[ "$#" -eq 4 ] || fail "usage: release.sh <version> <commit> <bin-dir> <dist-dir>"
version=$1
commit=$2
bin_dir=$3
dist=$4

echo "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || fail "version $version is not X.Y.Z"
echo "$commit" | grep -Eq '^[0-9a-f]{40}$' || fail "commit $commit is not a full commit hash"

here=$(cd "$(dirname "$0")" && pwd)
gitea_url=${GITEA_URL:-https://git.alva.dev}
repo=${GITEA_REPO:-alva/harness-telemetry}
api="$gitea_url/api/v1/repos/$repo"
tag="hottell-v$version"
binaries="hottell-darwin-arm64 hottell-darwin-amd64"
assets="$binaries SHA256SUMS install.sh"

rm -rf "$dist"
mkdir -p "$dist"
for binary in $binaries; do
	[ -f "$bin_dir/$binary" ] || fail "$bin_dir/$binary is not built"
	cp "$bin_dir/$binary" "$dist/$binary"
done
cp "$here/install.sh" "$dist/install.sh"
# install.sh is left out: the service rewrites it when serving it.
# shellcheck disable=SC2086 # the list of binaries is split on purpose
(cd "$dist" && shasum -a 256 $binaries >SHA256SUMS)
echo "Prepared $dist:"
# shellcheck disable=SC2086 # the list of assets is split on purpose
(cd "$dist" && ls -l $assets) | sed 's/^/  /'

if [ -n "${DRY_RUN:-}" ]; then
	echo "DRY_RUN: would create the release $tag on $commit: POST $api/releases"
	for asset in $assets; do
		echo "DRY_RUN: would upload $asset: POST $api/releases/<id>/assets?name=$asset"
	done
	exit 0
fi

[ -n "${GITEA_TOKEN:-}" ] || fail "GITEA_TOKEN is not set"

work=$(mktemp -d "${TMPDIR:-/tmp}/hottell-release.XXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
# The token goes to curl in a file, so it never appears in a process listing.
(
	umask 077
	printf 'Authorization: token %s\n' "$GITEA_TOKEN" >"$work/auth"
)

# gitea <method> <url> [curl arguments...]: leaves the response body in $work/body;
# returns 1 with the reason on stderr on any status but 2xx.
gitea() {
	method=$1
	url=$2
	shift 2
	if ! code=$(curl -sS -o "$work/body" -w '%{http_code}' -X "$method" -H @"$work/auth" "$@" "$url"); then
		echo "release.sh: $method $url: request failed" >&2
		return 1
	fi
	case "$code" in
	2??) ;;
	*)
		echo "release.sh: $method $url: HTTP $code: $(head -c 500 "$work/body")" >&2
		return 1
		;;
	esac
}

jq -n --arg tag "$tag" --arg commit "$commit" --arg version "$version" '{
	tag_name: $tag,
	target_commitish: $commit,
	name: $tag,
	body: ("hottell " + $version + " for macOS arm64 and amd64."),
	draft: false,
	prerelease: false
}' >"$work/release.json"

echo "Creating the release $tag on $commit"
gitea POST "$api/releases" -H 'Content-Type: application/json' --data-binary @"$work/release.json" ||
	fail "the release $tag is not created"
id=$(jq -er '.id' "$work/body") || fail "the release response has no id: $(head -c 500 "$work/body")"
release_url=$(jq -r '.html_url // empty' "$work/body")

for asset in $assets; do
	echo "Uploading $asset"
	gitea POST "$api/releases/$id/assets?name=$asset" -F "attachment=@$dist/$asset" ||
		fail "the release $tag is created, but $asset is not uploaded; delete the release and the tag in Gitea, then run again"
done

echo "Published $tag${release_url:+: $release_url}"

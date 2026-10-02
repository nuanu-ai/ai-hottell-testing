# AI Hottell testing distribution

This public repository distributes the macOS client from nuanu-ai/ai-hottell.
The exact source revision and release are recorded in SOURCE.json. Source files
in cmd/hottell, internal/hottell, pkg/deepv2 and skills are copied unchanged;
the Go module path is preserved for imports. Development tests/fixtures and
server/deployment configuration are excluded from this distribution.

Use a feature branch and PR. Preserve unrelated changes. Never run install,
uninstall or restore on the host while verifying a distribution. Verify the
upstream tag/commit, downloaded SHA256SUMS, both architecture assets, the native
binary version, and installer syntax before publishing. install.sh pins one
release; update its URL and SOURCE.json together. Publish the matching upstream
binaries and SHA256SUMS in this repository's GitHub Release. Do not publish
local configs, credentials, session data or private infrastructure artifacts.

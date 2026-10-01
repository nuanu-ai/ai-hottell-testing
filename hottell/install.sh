#!/bin/sh
# Загрузчик hottell для macOS: скачивает бинарь того релиза, с которым опубликован
# этот скрипт, проверяет его по SHA256SUMS и запускает `hottell install`.
# Релиз подставляет свой тег вместо плейсхолдера ниже. Приватный репозиторий:
# нужен gh с авторизованной учётной записью.
set -eu

REPO=nuanu-ai/ai-hottell
TAG=__HOTTELL_TAG__

fail() {
  echo "install.sh: $*" >&2
  exit 1
}

case "$TAG" in
  "" | __*) fail "в скрипте нет тега релиза; скачайте install.sh из GitHub Release" ;;
esac

command -v gh >/dev/null 2>&1 || fail "нужен gh (https://cli.github.com), репозиторий приватный"
gh auth status >/dev/null 2>&1 || fail "gh не авторизован: выполните 'gh auth login'"

case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  x86_64) ARCH=amd64 ;;
  *) fail "неподдерживаемая архитектура: $(uname -m)" ;;
esac
ASSET="hottell-darwin-$ARCH"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT INT TERM

gh release download "$TAG" -R "$REPO" -p "$ASSET" -p SHA256SUMS -D "$WORK" --clobber ||
  fail "не удалось скачать $ASSET и SHA256SUMS релиза $TAG"

# Проверяется только строка скачанного файла: в манифесте есть обе архитектуры.
awk -v f="$ASSET" '$2 == f || $2 == "*" f' "$WORK/SHA256SUMS" > "$WORK/asset.sum"
[ "$(wc -l < "$WORK/asset.sum" | tr -d ' ')" = 1 ] ||
  fail "в SHA256SUMS нет ровно одной строки для $ASSET"
(cd "$WORK" && shasum -a 256 -c asset.sum >/dev/null) ||
  fail "контрольная сумма $ASSET не совпала, установка остановлена"

chmod +x "$WORK/$ASSET"
# Атрибута может не быть.
xattr -d com.apple.quarantine "$WORK/$ASSET" 2>/dev/null || true

"$WORK/$ASSET" install

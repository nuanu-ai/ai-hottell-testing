#!/bin/sh
# Проверка install.sh без сети: поддельные gh и uname лежат первыми в PATH.
# Запуск: sh hottell/install_sh_test.sh
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT

FAILS=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; FAILS=$((FAILS + 1)); }

# Скрипт, как его публикует релиз: с подставленным тегом.
sed 's/__HOTTELL_TAG__/v9.9.9/' "$HERE/install.sh" > "$ROOT/install.sh"

sha() { shasum -a 256 "$1" | awk '{print $1}'; }

# Поддельный gh: `auth status` завершается по FAKE_GH_AUTH; `release download`
# копирует запрошенные -p файлы из $FAKE_RELEASE в каталог -D и пишет вызов в лог.
mkdir -p "$ROOT/stub"
cat > "$ROOT/stub/gh" <<'SH'
#!/bin/sh
if [ "$1" = auth ]; then exit "${FAKE_GH_AUTH:-0}"; fi
[ "$1" = release ] && [ "$2" = download ] || exit 2
shift 2
dir=.; names=
while [ $# -gt 0 ]; do
  case $1 in
    -D) dir=$2; shift ;;
    -p) names="$names $2"; shift ;;
    -R) shift ;;
    -*) ;;
    *) echo "$1" >> "$FAKE_LOG.tag" ;;
  esac
  shift
done
for n in $names; do cp "$FAKE_RELEASE/$n" "$dir/$n" || exit 1; done
SH
cat > "$ROOT/stub/uname" <<'SH'
#!/bin/sh
echo "${FAKE_ARCH:-arm64}"
SH
chmod +x "$ROOT/stub/gh" "$ROOT/stub/uname"

# Поддельный бинарь: фиксирует, что его вызвали с "install".
mkbin() { # путь
  cat > "$1" <<'SH'
#!/bin/sh
echo "called: $*" >> "$FAKE_LOG"
SH
}

# Релиз: оба бинаря и манифест на две архитектуры. Скачивается один из них.
newrelease() { # имя
  R="$ROOT/$1"; mkdir -p "$R"
  mkbin "$R/hottell-darwin-arm64"
  printf '#!/bin/sh\necho amd64 >> "$FAKE_LOG"\n' > "$R/hottell-darwin-amd64"
  {
    printf '%s  hottell-darwin-amd64\n' "$(sha "$R/hottell-darwin-amd64")"
    printf '%s  hottell-darwin-arm64\n' "$(sha "$R/hottell-darwin-arm64")"
  } > "$R/SHA256SUMS"
}

# run <имя случая> [ENV=...]: код возврата в $RC, вывод в $ROOT/out, лог бинаря в $ROOT/log.
run() {
  name=$1; shift
  : > "$ROOT/log"; rm -f "$ROOT/log.tag"; mkdir -p "$ROOT/tmp"
  env "$@" TMPDIR="$ROOT/tmp" FAKE_LOG="$ROOT/log" \
    sh "$ROOT/install.sh" > "$ROOT/out" 2>&1
  RC=$?
}

# 1. Успех: arm64, манифест на две архитектуры, скачан один бинарь.
newrelease rel
run success PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R" FAKE_ARCH=arm64
[ "$RC" -eq 0 ] && grep -qx 'called: install' "$ROOT/log" \
  && ok "success: arm64, two-arch manifest, binary called with install" \
  || { bad "success: rc=$RC"; cat "$ROOT/out"; }
grep -qx 'v9.9.9' "$ROOT/log.tag" && ok "success: release tag is the substituted one" \
  || bad "success: tag not passed to gh"
[ -z "$(ls -A "$ROOT/tmp")" ] && ok "success: temp dir removed" || bad "success: temp dir left"

# 2. x86_64 -> amd64.
run amd64 PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R" FAKE_ARCH=x86_64
[ "$RC" -eq 0 ] && grep -qx amd64 "$ROOT/log" \
  && ok "x86_64 is mapped to the amd64 binary" || { bad "amd64: rc=$RC"; cat "$ROOT/out"; }

# 3. Несовпадение контрольной суммы: ничего не запускается.
newrelease mismatch
printf 'tampered\n' >> "$R/hottell-darwin-arm64"
run mismatch PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R" FAKE_ARCH=arm64
[ "$RC" -ne 0 ] && [ ! -s "$ROOT/log" ] \
  && ok "checksum mismatch: fails, binary not run" || { bad "mismatch: rc=$RC log=$(cat "$ROOT/log")"; }
[ -z "$(ls -A "$ROOT/tmp")" ] && ok "mismatch: temp dir removed" || bad "mismatch: temp dir left"

# 4. В манифесте нет строки скачанного файла (есть только другая архитектура).
newrelease noline
grep -v arm64 "$R/SHA256SUMS" > "$R/S" && mv "$R/S" "$R/SHA256SUMS"
run noline PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R" FAKE_ARCH=arm64
[ "$RC" -ne 0 ] && [ ! -s "$ROOT/log" ] \
  && ok "no manifest line for the asset: fails, binary not run" || bad "noline: rc=$RC"

# 5. Без gh в PATH.
run nogh PATH="$ROOT/stubonly:/usr/bin:/bin" FAKE_RELEASE="$R"
if [ "$RC" -ne 0 ] && grep -qi 'gh' "$ROOT/out"; then ok "no gh: refuses and names gh"; else bad "nogh: rc=$RC"; fi

# 6. gh без авторизации.
newrelease rel2
run noauth PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R" FAKE_GH_AUTH=1
[ "$RC" -ne 0 ] && [ ! -s "$ROOT/log" ] && ok "gh not authenticated: refuses" || bad "noauth: rc=$RC"

# 7. Неподдерживаемая архитектура.
run arch PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R" FAKE_ARCH=i386
[ "$RC" -ne 0 ] && [ ! -s "$ROOT/log" ] && ok "unsupported architecture: refuses" || bad "arch: rc=$RC"

# 8. Неподставленный тег.
cp "$HERE/install.sh" "$ROOT/install.sh"
run notag PATH="$ROOT/stub:$PATH" FAKE_RELEASE="$R"
[ "$RC" -ne 0 ] && [ ! -s "$ROOT/log" ] && ok "unsubstituted tag: refuses" || bad "notag: rc=$RC"

[ "$FAILS" -eq 0 ] && echo "install_sh_test: all passed" || { echo "install_sh_test: $FAILS failed"; exit 1; }

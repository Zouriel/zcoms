#!/usr/bin/env bash
#
# Packages zcoms into ready-to-run, downloadable bundles under dist/:
#   - zcoms-linux-x64.tar.gz : zc + libtdjson.so side by side (just run ./zc)
#   - zcoms-win64.zip        : zc.exe + bin/*.dll
#
# The Linux library to bundle is found automatically (~/.local/bin or /usr/lib)
# or can be pointed at explicitly with LIBTDJSON=/path/to/libtdjson.so.
#
# NOTE: the bundled libtdjson.so is dynamically linked; it requires the host to
# have a glibc/OpenSSL at least as new as the machine it was built on. Build on
# an old baseline (e.g. an older distro / container) for the widest reach.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DIST="$ROOT/dist"
rm -rf "$DIST"
mkdir -p "$DIST"

# --- locate the Linux TDLib library to bundle ---
LIBTDJSON="${LIBTDJSON:-}"
if [[ -z "$LIBTDJSON" ]]; then
  for candidate in "$HOME"/.local/bin/libtdjson.so.* /usr/lib/libtdjson.so.* /usr/local/lib/libtdjson.so.*; do
    if [[ -e "$candidate" && ! -L "$candidate" ]]; then
      LIBTDJSON="$candidate"
      break
    fi
  done
fi
if [[ -z "$LIBTDJSON" || ! -e "$LIBTDJSON" ]]; then
  echo "error: could not find libtdjson.so to bundle; set LIBTDJSON=/path/to/libtdjson.so" >&2
  exit 1
fi
echo "Bundling libtdjson: $LIBTDJSON"

# --- linux bundle ---
LINUX_DIR="$DIST/zcoms-linux-x64"
mkdir -p "$LINUX_DIR"
echo "Building Linux binary..."
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -o "$LINUX_DIR/zc" .
soname="$(basename "$LIBTDJSON")"
cp "$LIBTDJSON" "$LINUX_DIR/$soname"
( cd "$LINUX_DIR" && ln -sf "$soname" libtdjson.so )
cp README.md LICENSE "$LINUX_DIR/" 2>/dev/null || true
( cd "$DIST" && tar czf zcoms-linux-x64.tar.gz zcoms-linux-x64 )

# --- windows bundle ---
WIN_DIR="$DIST/zcoms-win64"
mkdir -p "$WIN_DIR/bin"
echo "Building Windows binary..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o "$WIN_DIR/zc.exe" .
cp bin/*.dll "$WIN_DIR/bin/"
cp README.md LICENSE "$WIN_DIR/" 2>/dev/null || true
if command -v zip >/dev/null 2>&1; then
  ( cd "$DIST" && zip -qr zcoms-win64.zip zcoms-win64 )
elif command -v python3 >/dev/null 2>&1; then
  ( cd "$DIST" && python3 -c "import shutil; shutil.make_archive('zcoms-win64', 'zip', '.', 'zcoms-win64')" )
else
  echo "warning: no 'zip' or python3; falling back to tar.gz for Windows" >&2
  ( cd "$DIST" && tar czf zcoms-win64.tar.gz zcoms-win64 )
fi

echo "Done. Artifacts:"
ls -la "$DIST"/*.tar.gz "$DIST"/*.zip 2>/dev/null

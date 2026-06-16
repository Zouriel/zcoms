#!/usr/bin/env bash
#
# Builds a fully self-contained, portable Linux bundle in an old-glibc container
# (Ubuntu 20.04 => glibc 2.31) and assembles dist/zcoms-linux-x64.tar.gz.
#
# The bundle contains, all targeting old glibc and carrying their own OpenSSL:
#   zc                     (Go binary, cgo; glibc floor ~2.3)
#   libtdjson.so[.x.y.z]   (TDLib; glibc floor ~2.29, rpath=$ORIGIN)
#   libssl.so.1.1          (rpath=$ORIGIN)
#   libcrypto.so.1.1
# so it runs on essentially every x86-64 Linux from ~2019 on, regardless of the
# host's glibc or OpenSSL version.
#
# Requires: docker, and a local Go toolchain (reused inside the container).
#   GOROOT_DIR=/path/to/go   (default: $HOME/.local/go-sdk/go, else `go env GOROOT`)
#   TDLIB_REF=master         (TDLib git ref to build)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GOROOT_DIR="${GOROOT_DIR:-$HOME/.local/go-sdk/go}"
if [[ ! -x "$GOROOT_DIR/bin/go" ]]; then
  GOROOT_DIR="$(go env GOROOT 2>/dev/null || true)"
fi
if [[ ! -x "$GOROOT_DIR/bin/go" ]]; then
  echo "error: no Go toolchain found; set GOROOT_DIR=/path/to/go" >&2
  exit 1
fi
TDLIB_REF="${TDLIB_REF:-master}"
JOBS="${JOBS:-4}"

OUT="$ROOT/dist"
STAGE_NAME="zcoms-linux-x64"
rm -rf "$OUT/$STAGE_NAME" "$OUT/$STAGE_NAME.tar.gz"
mkdir -p "$OUT/$STAGE_NAME"

echo "Using Go: $GOROOT_DIR"
echo "Building portable bundle in ubuntu:20.04 (this includes a full TDLib compile)..."

docker run --rm \
  -v "$ROOT:/src:ro" \
  -v "$OUT/$STAGE_NAME:/out" \
  -v "$GOROOT_DIR:/usr/local/go:ro" \
  -e TDLIB_REF="$TDLIB_REF" -e JOBS="$JOBS" \
  -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
  ubuntu:20.04 bash -euo pipefail -c '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq git cmake g++ make zlib1g-dev libssl-dev gperf \
        build-essential patchelf libssl1.1 ca-certificates >/dev/null

    # --- TDLib (libtdjson.so) ---
    cd /tmp
    git clone --depth 1 --branch "$TDLIB_REF" https://github.com/tdlib/td.git 2>/dev/null \
      || git clone https://github.com/tdlib/td.git
    cd td && [ "$TDLIB_REF" != "master" ] && git checkout "$TDLIB_REF" || true
    mkdir -p build && cd build
    cmake -DCMAKE_BUILD_TYPE=Release -DTD_ENABLE_LTO=OFF .. >/dev/null
    cmake --build . --target tdjson -j"$JOBS"
    cp -a libtdjson.so* /out/
    strip /out/libtdjson.so* 2>/dev/null || true

    # --- zc (Go binary, cgo) ---
    export PATH=/usr/local/go/bin:$PATH
    export GOCACHE=/tmp/gocache GOMODCACHE=/tmp/gomod GOTOOLCHAIN=local
    cd /src
    # -buildvcs=false: the repo is bind-mounted with a different owner than the
    # container user, so git refuses VCS stamping ("dubious ownership", exit 128).
    CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o /out/zc .

    # --- bundle OpenSSL 1.1 + make libs self-referential ---
    SSLDIR=/usr/lib/x86_64-linux-gnu
    cp -a "$SSLDIR/libssl.so.1.1" "$SSLDIR/libcrypto.so.1.1" /out/
    for so in /out/libtdjson.so.* /out/libssl.so.1.1; do
      [ -f "$so" ] && patchelf --set-rpath "\$ORIGIN" "$so"
    done

    cp /src/README.md /src/LICENSE /out/ 2>/dev/null || true
    chown -R "${HOST_UID}:${HOST_GID}" /out
    echo PORTABLE_BUNDLE_OK
  '

echo "Packaging tarball..."
( cd "$OUT" && tar czf "$STAGE_NAME.tar.gz" "$STAGE_NAME" )
echo "Done: $OUT/$STAGE_NAME.tar.gz"
ls -la "$OUT/$STAGE_NAME.tar.gz"

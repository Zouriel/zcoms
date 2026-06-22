#!/usr/bin/env bash
set -u

if [ "$#" -lt 3 ]; then
  echo "usage: scheduled-zc-send.sh <tg|wa> <recipient> <message>" >&2
  exit 64
fi

channel="$1"
recipient="$2"
shift 2
message="$*"

timestamp() {
  date '+%Y-%m-%d %H:%M:%S %Z %z'
}

cd "$HOME/personal/zcoms" || exit 1

echo "[$(timestamp)] scheduled-zc-send start channel=$channel recipient=$recipient"

output_file="$(mktemp)"
case "$channel" in
  tg)
    "$HOME/.local/bin/zc" tg send "$recipient" "$message" >"$output_file" 2>&1
    rc=$?
    ;;
  wa)
    "$HOME/.local/bin/zc" wa send "$recipient" "$message" >"$output_file" 2>&1
    rc=$?
    ;;
  *)
    echo "unsupported channel: $channel" >&2
    rm -f "$output_file"
    exit 64
    ;;
esac

cat "$output_file"

if [ "$rc" -ne 0 ] && grep -q 'Message sent' "$output_file"; then
  echo "[$(timestamp)] zc exited $rc after accepting the message; treating as sent"
  rm -f "$output_file"
  exit 0
fi

echo "[$(timestamp)] zc exited with code $rc"
rm -f "$output_file"
exit "$rc"

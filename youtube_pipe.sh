#!/usr/bin/env bash
set -euo pipefail

MODE="${1:-}"
VIDEO_ID="${2:-}"
COOKIE_FILE="${3:-}"

URL="https://www.youtube.com/watch?v=${VIDEO_ID}"

YTDLP_ARGS=(
  --quiet
  --no-warnings
  --no-playlist
  --geo-bypass
  --retries 0
  --extractor-retries 0
  --socket-timeout 10
  --concurrent-fragments 8
  --js-runtimes "deno:/usr/local/bin/deno"
  --extractor-args "youtubepot-bgutilhttp:base_url=http://bgutil-ytdlp-pot-provider.railway.internal:4416"
  --extractor-args "youtube:player_client=mweb;player_js_version=actual"
)

if [[ -n "$COOKIE_FILE" && -f "$COOKIE_FILE" ]]; then
  YTDLP_ARGS+=(--cookies "$COOKIE_FILE")
fi

if [[ "$MODE" == "audio" ]]; then
  yt-dlp \
    "${YTDLP_ARGS[@]}" \
    -f "140/bestaudio[ext=m4a]/bestaudio" \
    -o - \
    "$URL" |
  ffmpeg \
    -hide_banner \
    -loglevel warning \
    -probesize 512K \
    -analyzeduration 1000000 \
    -re \
    -i pipe:0 \
    -vn \
    -f s16le \
    -ac 2 \
    -ar 48000 \
    pipe:1

elif [[ "$MODE" == "video" ]]; then
  yt-dlp \
    "${YTDLP_ARGS[@]}" \
    -f "136/bestvideo[height<=720][ext=mp4]/bestvideo[height<=720]" \
    -o - \
    "$URL" |
  ffmpeg \
    -hide_banner \
    -loglevel error \
    -probesize 512K \
    -analyzeduration 1000000 \
    -re \
    -i pipe:0 \
    -an \
    -f rawvideo \
    -r 30 \
    -pix_fmt yuv420p \
    -vf "scale=1280:720:flags=lanczos" \
    pipe:1

else
  echo "Unknown mode: $MODE" >&2
  exit 2
fi

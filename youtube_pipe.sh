#!/usr/bin/env bash

set +e
trap 'exit 0' PIPE

MODE="${1:-}"
VIDEO_ID="${2:-}"
COOKIE_FILE="${3:-}"

URL="https://www.youtube.com/watch?v=${VIDEO_ID}"

YTDLP_ARGS=(
  --quiet
  --no-warnings
  --no-playlist
  --geo-bypass
  --retries 1
  --extractor-retries 1
  --socket-timeout 10
  --concurrent-fragments 4
  --js-runtimes "deno:/usr/local/bin/deno"
  --extractor-args "youtubepot-bgutilhttp:base_url=http://bgutil-ytdlp-pot-provider.railway.internal:4416"
  --extractor-args "youtube:player_client=mweb;player_js_version=actual"
)

if [[ -n "$COOKIE_FILE" && -f "$COOKIE_FILE" ]]; then
  YTDLP_ARGS+=(--cookies "$COOKIE_FILE")
fi

run_ytdlp() {
  yt-dlp \
    "${YTDLP_ARGS[@]}" \
    -f "18/best[height<=720][ext=mp4]/best[height<=720]/best" \
    -o - \
    "$URL" \
    2>/dev/null
}

if [[ "$MODE" == "audio" ]]; then
  run_ytdlp |
  ffmpeg \
    -hide_banner \
    -loglevel fatal \
    -probesize 256K \
    -analyzeduration 500000 \
    -i pipe:0 \
    -map 0:a:0? \
    -vn \
    -af "aresample=async=1:first_pts=0,asetpts=PTS-STARTPTS,arealtime" \
    -ac 2 \
    -ar 48000 \
    -f s16le \
    pipe:1 2>/dev/null

  exit 0

elif [[ "$MODE" == "video" ]]; then
  run_ytdlp |
  ffmpeg \
    -hide_banner \
    -loglevel fatal \
    -probesize 256K \
    -analyzeduration 500000 \
    -i pipe:0 \
    -map 0:v:0? \
    -an \
    -vf "setpts=PTS-STARTPTS,fps=30,scale=1280:720:flags=fast_bilinear,realtime" \
    -pix_fmt yuv420p \
    -f rawvideo \
    pipe:1 2>/dev/null

  exit 0
fi

exit 0

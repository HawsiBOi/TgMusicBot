package vc

import (
	"ashokshau/tgmusic/src/vc/ntgcalls"
	"fmt"
	"regexp"
	"strings"
)

var isURLRegex = regexp.MustCompile(`^https?://`)

const ytDlpPipePrefix = "|||HAWSI_YTDLP_PIPE|||"

func buildYouTubePipe(videoID string, cookieFile string) string {
	videoURL := "https://www.youtube.com/watch?v=" + videoID

	var cmd strings.Builder

	cmd.WriteString("yt-dlp ")
	cmd.WriteString("--quiet --no-warnings ")
	cmd.WriteString("--no-playlist --geo-bypass ")
	cmd.WriteString("--retries 0 --extractor-retries 0 ")
	cmd.WriteString("--socket-timeout 10 ")
	cmd.WriteString("--concurrent-fragments 8 ")
	cmd.WriteString(`--js-runtimes "deno:/usr/local/bin/deno" `)
	cmd.WriteString(`--extractor-args "youtubepot-bgutilhttp:base_url=http://bgutil-ytdlp-pot-provider.railway.internal:4416" `)
	cmd.WriteString(`--extractor-args "youtube:player_client=mweb;player_js_version=actual" `)

	if cookieFile != "" {
		cmd.WriteString(fmt.Sprintf("--cookies %q ", cookieFile))
	}

	cmd.WriteString(`-f "18/best[height<=720][ext=mp4]" `)
	cmd.WriteString("-o - ")
	cmd.WriteString(fmt.Sprintf("%q", videoURL))

	return cmd.String()
}

func appendGoogleVideoHeaders(cmd *strings.Builder, mediaPath string) {
	if !strings.Contains(strings.ToLower(mediaPath), "googlevideo.com") {
		return
	}

	cmd.WriteString(`-user_agent "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/144.0.0.0 Safari/537.36" `)
}

// getMediaDescription creates a media description for ntgcalls based on the provided file path, video status, and ffmpeg parameters.
func getMediaDescription(filePath string, isVideo bool, ffmpegParameters string) ntgcalls.MediaDescription {
	audioDescription := &ntgcalls.AudioDescription{
		MediaSource:  ntgcalls.MediaSourceShell,
		SampleRate:   48000,
		ChannelCount: 2,
	}

	audioPath := filePath
	videoPath := filePath

	isYouTubePipe := strings.HasPrefix(filePath, ytDlpPipePrefix)
	var youtubePipe string

	if isYouTubePipe {
		payload := strings.TrimPrefix(filePath, ytDlpPipePrefix)
		parts := strings.SplitN(payload, "|||", 2)

		videoID := strings.TrimSpace(parts[0])
		cookieFile := ""

		if len(parts) == 2 {
			cookieFile = strings.TrimSpace(parts[1])
		}

		youtubePipe = buildYouTubePipe(videoID, cookieFile)
	}

	const dualSeparator = "|||HAWSI_DUAL_STREAM|||"
	if strings.Contains(filePath, dualSeparator) {
		parts := strings.SplitN(filePath, dualSeparator, 2)
		videoPath = strings.TrimSpace(parts[0])
		audioPath = strings.TrimSpace(parts[1])
	}

	quotedAudioPath := fmt.Sprintf("\"%s\"", audioPath)
	quotedVideoPath := fmt.Sprintf("\"%s\"", videoPath)

	isURL := isURLRegex.MatchString(videoPath)
	if isYouTubePipe {
		isURL = true
	}
	isAudioURL := isURLRegex.MatchString(audioPath)

	isLiveHLS := strings.Contains(strings.ToLower(videoPath), ".m3u8") ||
		strings.Contains(strings.ToLower(videoPath), "manifest.googlevideo.com")

	var audioCmd strings.Builder
	audioCmd.WriteString("ffmpeg ")
	appendGoogleVideoHeaders(&audioCmd, audioPath)
	if isAudioURL && !isLiveHLS {
		audioCmd.WriteString("-reconnect 1 -reconnect_streamed 1 -reconnect_delay_max 2 ")
	}

	var seekFlags, filterFlags string
	if ffmpegParameters != "" {
		if strings.Contains(ffmpegParameters, "filter:") {
			filterFlags = ffmpegParameters
		} else {
			seekFlags = ffmpegParameters
		}
	}

	if seekFlags != "" {
		audioCmd.WriteString(seekFlags + " ")
	}

	if isYouTubePipe {
		audioCmd.WriteString("-probesize 512K -analyzeduration 1000000 -i pipe:0 ")
		audioCmdStr := youtubePipe + " | " + audioCmd.String()
		audioCmd.Reset()
		audioCmd.WriteString(audioCmdStr)
	} else {
		audioCmd.WriteString("-i " + quotedAudioPath + " ")
	}
	if filterFlags != "" {
		audioCmd.WriteString(filterFlags + " ")
	}

	audioCmd.WriteString(fmt.Sprintf("-f s16le -ac %d -ar %d -v warning pipe:1",
		audioDescription.ChannelCount,
		audioDescription.SampleRate,
	))
	audioDescription.Input = audioCmd.String()

	if !isVideo {
		return ntgcalls.MediaDescription{
			Microphone: audioDescription,
		}
	}

	originalWidth, originalHeight := getVideoDimensions(videoPath)

	width := 1280
	height := 720

	if originalWidth > 0 && originalHeight > 0 {
		ratio := float64(originalWidth) / float64(originalHeight)
		newW := min(originalWidth, width)
		newH := int(float64(newW) / ratio)

		if newH > height {
			newH = height
			newW = int(float64(newH) * ratio)
		}

		if newW%2 != 0 {
			newW--
		}
		if newH%2 != 0 {
			newH--
		}

		width = newW
		height = newH
	}

	videoFPS := 30

	// Local downloaded videos are fully decoded into raw YUV frames.
	// Use 24 FPS to reduce CPU and pipe pressure without affecting
	// direct/live stream playback.
	if !isURL {
		videoFPS = 24
	}

	videoDescription := &ntgcalls.VideoDescription{
		MediaSource: ntgcalls.MediaSourceShell,
		Width:       int16(width),
		Height:      int16(height),
		Fps:         uint8(videoFPS),
	}

	var videoCmd strings.Builder
	videoCmd.WriteString("ffmpeg ")
	appendGoogleVideoHeaders(&videoCmd, videoPath)

	if isURL && !isLiveHLS {
		videoCmd.WriteString("-reconnect 1 -reconnect_streamed 1 -reconnect_delay_max 2 ")
	}

	if seekFlags != "" {
		videoCmd.WriteString(seekFlags + " ")
	}

	if isYouTubePipe {
		videoCmd.WriteString("-probesize 512K -analyzeduration 1000000 -i pipe:0 ")
		videoCmdStr := youtubePipe + " | " + videoCmd.String()
		videoCmd.Reset()
		videoCmd.WriteString(videoCmdStr)
	} else {
		videoCmd.WriteString(fmt.Sprintf("-i %s ", quotedVideoPath))
	}

	if filterFlags != "" {
		videoCmd.WriteString(filterFlags + " ")
	}

	if !isURL {
		videoCmd.WriteString(fmt.Sprintf("-threads 0 -f rawvideo -pix_fmt yuv420p -vf \"fps=%d,scale=%d:%d:flags=fast_bilinear\" -v error pipe:1",
			videoDescription.Fps,
			videoDescription.Width,
			videoDescription.Height,
		))
	} else {
		videoCmd.WriteString(fmt.Sprintf("-f rawvideo -r %d -pix_fmt yuv420p -vf scale=%d:%d -v error pipe:1",
			videoDescription.Fps,
			videoDescription.Width,
			videoDescription.Height,
		))
	}
	videoDescription.Input = videoCmd.String()

	return ntgcalls.MediaDescription{
		Microphone: audioDescription,
		Camera:     videoDescription,
	}
}

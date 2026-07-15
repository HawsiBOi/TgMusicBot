/*
 * TgMusicBot - Telegram Music Bot
 *  Copyright (c) 2025-2026 Ashok Shau
 *
 *  Licensed under GNU GPL v3
 *  See https://github.com/AshokShau/TgMusicBot
 */

package dl

import (
	"time"

	"ashokshau/tgmusic/config"
	"ashokshau/tgmusic/src/utils"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// youTubeData provides an interface for fetching track and playlist information from YouTube.
type youTubeData struct {
	Query    string
	ApiUrl   string
	APIKey   string
	Patterns map[string]*regexp.Regexp
}

var youtubePatterns = map[string]*regexp.Regexp{
	"youtube":   regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?youtube\.com/.*`),
	"youtu_be":  regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?youtu\.be/.*`),
	"yt_music":  regexp.MustCompile(`(?i)^(?:https?://)?music\.youtube\.com/.*`),
	"yt_shorts": regexp.MustCompile(`(?i)^(?:https?://)?(?:www\.)?youtube\.com/shorts/.*`),
}

// newYouTubeData initializes a youTubeData instance with pre-compiled regex patterns and a cleaned query.
func newYouTubeData(query string) *youTubeData {
	return &youTubeData{
		Query:    strings.TrimSpace(query),
		ApiUrl:   strings.TrimRight(config.ApiUrl, "/"),
		APIKey:   config.ApiKey,
		Patterns: youtubePatterns,
	}
}

func (y *youTubeData) isValid() bool {
	if y.Query == "" {
		slog.Info("The query or patterns are empty.")
		return false
	}

	for _, pattern := range y.Patterns {
		if pattern.MatchString(y.Query) {
			return true
		}
	}
	return false
}

func (y *youTubeData) getInfo() (utils.PlatformTracks, error) {
	if !y.isValid() {
		return utils.PlatformTracks{}, errors.New("the provided URL is invalid or the platform is not supported")
	}

	y.Query = normalizeYouTubeURL(y.Query)
	videoID := extractVideoID(y.Query)
	playlistID := extractPlaylistID(y.Query)

	switch {
	case playlistID != "":
		playlistCtx, playlistCancel := context.WithTimeout(
			context.Background(),
			90*time.Second,
		)
		defer playlistCancel()

		slog.Info(
			"[YouTube] Playlist link detected",
			"playlist_id", playlistID,
			"url", y.Query,
		)

		if strings.HasPrefix(playlistID, "RD") {
			slog.Info(
				"[YouTube] Mix playlist detected",
				"playlist_id", playlistID,
			)

			return getYouTubeMixPlaylist(
				playlistCtx,
				playlistID,
			)
		}

		return getYouTubePlaylist(
			playlistCtx,
			playlistID,
		)

	case videoID != "":
		ctx, cancel := context.WithTimeout(
			context.Background(),
			15*time.Second,
		)
		defer cancel()
		for _, query := range []string{videoID, y.Query} {
			tracks, err := searchYouTube(query, 10)
			if err != nil {
				continue
			}

			for _, track := range tracks {
				if track.Id == videoID {
					return utils.PlatformTracks{Results: []utils.MusicTrack{track}}, nil
				}
			}
		}

		if title, err := getYouTubeTitleFromOEmbed(videoID); err == nil && title != "" {
			tracks, err := searchYouTube(title, 10)
			if err == nil {
				for _, track := range tracks {
					if track.Id == videoID {
						return utils.PlatformTracks{Results: []utils.MusicTrack{track}}, nil
					}
				}
			}
		}

		slog.Warn("Video ID was extracted but no matching track was found in search results", "video_id", videoID)
		return getYouTubeVideo(ctx, videoID)
	}

	return utils.PlatformTracks{}, errors.New("no video or playlist results were found")
}

func (y *youTubeData) search() (utils.PlatformTracks, error) {
	tracks, err := searchYouTube(y.Query, 5)
	if err != nil {
		return utils.PlatformTracks{}, err
	}

	if len(tracks) == 0 {
		return utils.PlatformTracks{}, errors.New("no video results were found")
	}

	return utils.PlatformTracks{Results: tracks}, nil
}

func (y *youTubeData) getTrack() (utils.TrackInfo, error) {
	if y.Query == "" {
		return utils.TrackInfo{}, errors.New("the query is empty")
	}

	if !y.isValid() {
		return utils.TrackInfo{}, errors.New("the provided URL is invalid or the platform is not supported")
	}

	if y.ApiUrl != "" && y.APIKey != "" {
		if trackInfo, err := newApiData(y.Query).getTrack(); err == nil {
			return trackInfo, nil
		}
	}

	getInfo, err := y.getInfo()
	if err != nil {
		return utils.TrackInfo{}, err
	}
	if len(getInfo.Results) == 0 {
		return utils.TrackInfo{}, errors.New("no video results were found")
	}

	track := getInfo.Results[0]
	trackInfo := utils.TrackInfo{
		Id:       track.Id,
		URL:      track.Url,
		Platform: utils.YouTube,
	}

	return trackInfo, nil
}

var (
	youtubeRequestMu   sync.Mutex
	lastYouTubeRequest time.Time
)

func waitForYouTubeRequest() {
	youtubeRequestMu.Lock()
	defer youtubeRequestMu.Unlock()

	const minimumDelay = 2 * time.Second

	if lastYouTubeRequest.IsZero() {
		lastYouTubeRequest = time.Now()
		return
	}

	elapsed := time.Since(lastYouTubeRequest)

	if elapsed < minimumDelay {
		delay := minimumDelay - elapsed

		slog.Info(
			"[YouTube] Adaptive request gate waiting",
			"delay", delay,
		)

		time.Sleep(delay)
	}

	lastYouTubeRequest = time.Now()
}

func isYouTubeRateLimited(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())

	return strings.Contains(text, "rate-limited") ||
		strings.Contains(text, "rate limited") ||
		strings.Contains(text, "too many requests") ||
		strings.Contains(text, "http error 429") ||
		strings.Contains(text, "this content isn't available, try again later")
}

func isYouTubeBotCheck(err error) bool {
	if err == nil {
		return false
	}

	text := strings.ToLower(err.Error())
	text = strings.ReplaceAll(text, "’", "'")

	return strings.Contains(text, "sign in to confirm you're not a bot") ||
		strings.Contains(text, "confirm you're not a bot") ||
		strings.Contains(text, "use --cookies-from-browser") ||
		strings.Contains(text, "use --cookies for the authentication")
}

// downloadTrack handles the download of a track from YouTube.
func (y *youTubeData) downloadTrack(info utils.TrackInfo, video bool) (string, error) {

	resolveStart := time.Now()

	streamURL, resolveErr := y.resolveDirectMediaURL(info.Id, video)

	slog.Info(
		"[LATENCY] YouTube direct resolve finished",
		"duration", time.Since(resolveStart),
		"video_id", info.Id,
		"video", video,
	)

	if resolveErr == nil && streamURL != "" {
		slog.Info(
			"[YouTube] Using low-latency direct stream",
			"video_id", info.Id,
			"video", video,
		)

		return streamURL, nil
	}

	if isYouTubeRateLimited(resolveErr) {
		slog.Warn(
			"[YouTube] Session rate limited; download fallback skipped",
			"video_id", info.Id,
			"error", resolveErr,
		)

		return "", fmt.Errorf(
			"YouTube session is temporarily rate limited: %w",
			resolveErr,
		)
	}

	slog.Warn(
		"[YouTube] Direct stream failed; using download fallback",
		"video_id", info.Id,
		"video", video,
		"error", resolveErr,
	)

	if !video && y.ApiUrl != "" && y.APIKey != "" {
		if filePath, err := y.downloadWithApi(info.Id, video); err == nil {
			return filePath, nil
		}
	}

	slog.Info(
		"[YouTube] Starting immediate download fallback",
		"video_id", info.Id,
		"video", video,
	)

	return y.downloadWithYtDlp(info.Id, video)
}

func validateDirectMediaURL(mediaURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Range", "bytes=0-1")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.youtube.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK &&
		resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("direct media URL returned HTTP %d", resp.StatusCode)
	}

	return nil
}

func (y *youTubeData) resolveDirectMediaURL(videoID string, video bool) (string, error) {
	if videoID == "" {
		return "", errors.New("videoID is empty")
	}

	videoURL := "https://www.youtube.com/watch?v=" + videoID

	resolve := func(cookieFile string) (string, error) {
		args := []string{
			"--no-warnings",
			"--no-playlist",
			"--geo-bypass",
			"--socket-timeout", "10",
			"--retries", "0",
			"--extractor-retries", "0",
			"--js-runtimes", "deno:/usr/local/bin/deno",
			"--extractor-args", "youtube:player_js_version=actual",
		}

		if video {
			args = append(
				args,
				"-f",
				"bestvideo[height<=720][vcodec^=avc1]+bestaudio[ext=m4a]/best[height<=720]",
				"--get-url",
			)
		} else {
			args = append(
				args,
				"-f",
				"bestaudio[ext=m4a]/bestaudio",
				"--get-url",
			)
		}

		if cookieFile != "" {
			args = append(args, "--cookies", cookieFile)
		} else if config.Proxy != "" {
			args = append(args, "--proxy", config.Proxy)
		}

		args = append(args, videoURL)

		ctx, cancel := context.WithTimeout(
			context.Background(),
			8*time.Second,
		)
		defer cancel()

		cmd := exec.CommandContext(ctx, "yt-dlp", args...)
		output, err := cmd.Output()

		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", errors.New("direct stream resolve timed out")
			}

			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return "", fmt.Errorf(
					"yt-dlp direct resolve failed: %s",
					strings.TrimSpace(string(exitErr.Stderr)),
				)
			}

			return "", fmt.Errorf(
				"yt-dlp direct resolve failed: %w",
				err,
			)
		}

		rawOutput := strings.TrimSpace(string(output))
		if rawOutput == "" {
			return "", errors.New("yt-dlp returned empty URL")
		}

		var urls []string

		for _, line := range strings.Split(rawOutput, "\n") {
			line = strings.TrimSpace(line)

			if strings.HasPrefix(line, "http://") ||
				strings.HasPrefix(line, "https://") {
				urls = append(urls, line)
			}
		}

		if video {
			if len(urls) < 2 {
				return "", fmt.Errorf(
					"separate video/audio URLs not returned; got %d URL(s)",
					len(urls),
				)
			}

			if err := validateDirectMediaURL(urls[0]); err != nil {
				return "", fmt.Errorf("direct video URL preflight failed: %w", err)
			}

			if err := validateDirectMediaURL(urls[1]); err != nil {
				return "", fmt.Errorf("direct audio URL preflight failed: %w", err)
			}

			return urls[0] +
				"|||HAWSI_DUAL_STREAM|||" +
				urls[1], nil
		}

		if len(urls) == 0 {
			return "", errors.New("audio stream URL not returned")
		}

		if err := validateDirectMediaURL(urls[0]); err != nil {
			return "", fmt.Errorf("direct audio URL preflight failed: %w", err)
		}

		return urls[0], nil
	}

	slog.Info(
		"[YouTube] Trying guest direct resolve",
		"video_id", videoID,
		"video", video,
	)

	streamURL, guestErr := resolve("")
	if guestErr == nil {
		return streamURL, nil
	}

	if isYouTubeRateLimited(guestErr) {
		return "", guestErr
	}

	cookieFile := y.getCookieFile()
	if cookieFile == "" {
		return "", guestErr
	}

	slog.Info(
		"[YouTube] Guest resolve failed; trying one cookie",
		"video_id", videoID,
		"video", video,
	)

	slog.Info(
		"[YouTube] Immediate cookie retry",
		"video_id", videoID,
	)

	streamURL, cookieErr := resolve(cookieFile)
	if cookieErr == nil {
		return streamURL, nil
	}

	if isYouTubeBotCheck(cookieErr) {
		slog.Warn(
			"[YouTube] Cookie rejected by YouTube",
			"video_id", videoID,
			"cookie", cookieFile,
		)

		return "", fmt.Errorf(
			"YouTube cookie rejected: %w",
			cookieErr,
		)
	}

	return "", cookieErr
}

// buildYtdlpParams constructs the command-line parameters for yt-dlp to download media.
func (y *youTubeData) buildYtdlpParams(videoID string, video bool) ([]string, string) {
	outputTemplate := filepath.Join(
		config.DownloadsDir,
		"%(id)s.%(ext)s",
	)

	var cookieFile string

	params := []string{
		"yt-dlp",
		"--no-warnings",
		"--quiet",
		"--geo-bypass",
		"--retries", "0",
		"--extractor-retries", "0",
		"--continue",
		"--no-part",
		"--concurrent-fragments", "1",
		"--socket-timeout", "10",
		"--throttled-rate", "100K",
		"--retry-sleep", "1",
		"--no-write-thumbnail",
		"--no-write-info-json",
		"--no-embed-metadata",
		"--no-embed-chapters",
		"--no-embed-subs",
		"--js-runtimes", "deno:/usr/local/bin/deno",
		"--extractor-args", "youtube:player_js_version=actual",
		"-o", outputTemplate,
	}

	if video {
		params = append(
			params,
			"-f",
			"bestvideo[height<=720]+bestaudio/best[height<=720]",
			"--merge-output-format",
			"mp4",
		)
	} else {
		params = append(
			params,
			"-f",
			"bestaudio[ext=m4a]/bestaudio",
		)
	}

	cookieFile = y.getCookieFile()
	if cookieFile != "" {
		params = append(params, "--cookies", cookieFile)
	} else if config.Proxy != "" {
		params = append(params, "--proxy", config.Proxy)
	}

	videoURL := "https://www.youtube.com/watch?v=" + videoID

	params = append(
		params,
		videoURL,
		"--print",
		"after_move:filepath",
	)

	return params, cookieFile
}

// downloadWithYtDlp downloads media from YouTube using the yt-dlp command-line tool.
func (y *youTubeData) downloadWithYtDlp(videoID string, video bool) (string, error) {
	if videoID == "" {
		return "", errors.New("videoID is empty")
	}

	maxAttempts := len(config.CookiesPath) + 1
	if maxAttempts < 2 {
		maxAttempts = 2
	}

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ytdlpParams, cookieFile := y.buildYtdlpParams(videoID, video)

		ctx, cancel := context.WithTimeout(
			context.Background(),
			2*time.Minute,
		)

		cmd := exec.CommandContext(
			ctx,
			ytdlpParams[0],
			ytdlpParams[1:]...,
		)

		output, err := cmd.Output()
		ctxErr := ctx.Err()
		cancel()

		if err == nil {
			downloadedPathStr := strings.TrimSpace(string(output))

			if downloadedPathStr == "" {
				lastErr = fmt.Errorf(
					"no output path was returned for %s",
					videoID,
				)
				continue
			}

			if _, statErr := os.Stat(downloadedPathStr); statErr != nil {
				lastErr = fmt.Errorf(
					"the file was not found at the reported path: %s",
					downloadedPathStr,
				)
				continue
			}

			return downloadedPathStr, nil
		}

		if errors.Is(ctxErr, context.DeadlineExceeded) {
			lastErr = fmt.Errorf(
				"yt-dlp timed out for video ID: %s",
				videoID,
			)
			continue
		}

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			lastErr = fmt.Errorf(
				"yt-dlp failed with exit code %d: %s",
				exitErr.ExitCode(),
				string(exitErr.Stderr),
			)
		} else {
			lastErr = fmt.Errorf(
				"an unexpected error occurred while downloading %s: %w",
				videoID,
				err,
			)
		}

		if cookieFile != "" && isYouTubeBotCheck(lastErr) {
			slog.Warn(
				"[YouTube] Cookie rejected; trying another cookie",
				"cookie", cookieFile,
				"video_id", videoID,
				"attempt", attempt,
			)

			_ = os.Remove(cookieFile)
			continue
		}

		if isYouTubeRateLimited(lastErr) {
			return "", lastErr
		}

		break
	}

	if lastErr == nil {
		lastErr = errors.New("YouTube download failed")
	}

	return "", lastErr
}

// getCookieFile retrieves the path to a cookie file from the configured list.
func (y *youTubeData) getCookieFile() string {
	cookiesPath := config.CookiesPath
	if len(cookiesPath) == 0 {
		return ""
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(cookiesPath))))
	if err != nil {
		slog.Info("Could not generate a random number", "error", err)
		return cookiesPath[0]
	}

	return cookiesPath[n.Int64()]
}

// downloadWithApi downloads a track using the external API.
func (y *youTubeData) downloadWithApi(videoID string, _ bool) (string, error) {
	videoUrl := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)
	api := newApiData(videoUrl)
	track, err := api.getTrack()
	if err != nil {
		return "", err
	}

	down, err := newDownload(track)
	if err != nil {
		slog.Info("Error creating download: " + err.Error())
		return "", err
	}

	return down.Process()
}

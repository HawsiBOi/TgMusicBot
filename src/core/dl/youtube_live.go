package dl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type YouTubeLiveInfo struct {
	Title     string
	StreamURL string
	IsLive    bool
}

type youtubeLiveJSON struct {
	Title      string `json:"title"`
	URL        string `json:"url"`
	IsLive     bool   `json:"is_live"`
	LiveStatus string `json:"live_status"`
	Formats    []struct {
		URL string `json:"url"`
	} `json:"formats"`
}

func ResolveYouTubeLive(url string) (*YouTubeLiveInfo, error) {
	if url == "" {
		return nil, errors.New("empty YouTube URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	args := []string{
		"--no-warnings",
		"--no-playlist",
		"--geo-bypass",
		"-J",
	}

	yt := newYouTubeData(url)
	cookieFile := yt.getCookieFile()

	if cookieFile != "" {
		args = append(args, "--cookies", cookieFile)
	}

	args = append(args, url)

	waitForYouTubeRequest()

	cmd := exec.CommandContext(ctx, "yt-dlp", args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		return nil, fmt.Errorf(
			"yt-dlp live extraction failed: %s",
			strings.TrimSpace(string(output)),
		)
	}

	var info youtubeLiveJSON
	if err := json.Unmarshal(output, &info); err != nil {
		return nil, fmt.Errorf("failed to parse live info: %w", err)
	}

	isLive := info.IsLive || info.LiveStatus == "is_live"

	if !isLive {
		return &YouTubeLiveInfo{
			Title:  info.Title,
			IsLive: false,
		}, nil
	}

	streamURL := info.URL

	if streamURL == "" {
		for i := len(info.Formats) - 1; i >= 0; i-- {
			if info.Formats[i].URL != "" {
				streamURL = info.Formats[i].URL
				break
			}
		}
	}

	if streamURL == "" {
		return nil, errors.New("no playable live stream URL found")
	}

	return &YouTubeLiveInfo{
		Title:     info.Title,
		StreamURL: streamURL,
		IsLive:    true,
	}, nil
}

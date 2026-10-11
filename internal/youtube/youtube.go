// Package youtube parses YouTube links into 11-character video IDs.
package youtube

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var idRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

var hosts = map[string]bool{
	"youtube.com":       true,
	"www.youtube.com":   true,
	"m.youtube.com":     true,
	"music.youtube.com": true,
	"youtu.be":          true,
}

var ErrInvalid = errors.New("not a recognised YouTube link")

// ParseVideoID returns the video ID for a YouTube URL, or ErrInvalid.
func ParseVideoID(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 300 {
		return "", ErrInvalid
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", ErrInvalid
	}
	host := strings.ToLower(u.Hostname())
	if !hosts[host] {
		return "", ErrInvalid
	}

	var id string
	if host == "youtu.be" {
		id = strings.SplitN(strings.TrimPrefix(u.Path, "/"), "/", 2)[0]
	} else {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		switch {
		case u.Path == "/watch":
			id = u.Query().Get("v")
		case len(parts) >= 2 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live" || parts[0] == "v"):
			id = parts[1]
		}
	}
	if !idRe.MatchString(id) {
		return "", ErrInvalid
	}
	return id, nil
}

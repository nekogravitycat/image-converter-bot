// Package discordurl handles Discord signed CDN attachment URLs and the result message built around them.
package discordurl

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// ErrNoExpiry is returned when an attachment URL lacks the ex query parameter.
var ErrNoExpiry = errors.New("attachment URL has no ex parameter")

// ParseExpiry returns the expiry encoded in the URL's ex query parameter (hexadecimal Unix seconds).
func ParseExpiry(rawURL string) (time.Time, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse attachment URL: %w", err)
	}
	ex := u.Query().Get("ex")
	if ex == "" {
		return time.Time{}, ErrNoExpiry
	}
	secs, err := strconv.ParseInt(ex, 16, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ex parameter: %w", err)
	}
	if secs <= 0 {
		return time.Time{}, fmt.Errorf("parse ex parameter: non-positive timestamp %d", secs)
	}
	return time.Unix(secs, 0), nil
}

// Redact strips the query string so signed URLs never reach the logs.
func Redact(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid url>"
	}
	return u.Host + u.Path
}

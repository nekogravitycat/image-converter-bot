package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	errInputTooLarge = errors.New("input file too large")
	errDownload      = errors.New("download failed")
)

// discordCDNHosts are the only hosts we fetch from. URLs come from Discord event payloads,
// never from user text, but we still refuse anything else as defence against SSRF.
var discordCDNHosts = []string{"cdn.discordapp.com", "media.discordapp.net"}

func isDiscordCDN(u *url.URL) bool {
	if u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range discordCDNHosts {
		if host == h {
			return true
		}
	}
	return false
}

func newDownloadClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Minute, // upper bound; callers pass tighter contexts
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || !isDiscordCDN(req.URL) {
				return fmt.Errorf("refusing redirect to %s", req.URL.Host)
			}
			return nil
		},
	}
}

// download fetches an attachment URL accepted by allow, reading at most maxBytes.
func download(ctx context.Context, client *http.Client, allow func(*url.URL) bool, rawURL string, maxBytes int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !allow(u) {
		return nil, fmt.Errorf("%w: not a Discord CDN URL", errDownload)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDownload, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDownload, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", errDownload, resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes", errInputTooLarge, resp.ContentLength)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errDownload, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: more than %d bytes", errInputTooLarge, maxBytes)
	}
	return data, nil
}

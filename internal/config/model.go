// Package config holds per-guild image conversion settings, their SQLite persistence and an in-memory cache.
package config

import (
	"errors"
	"fmt"
	"slices"

	"github.com/disgoorg/snowflake/v2"
)

// Accepted ranges for guild settings.
const (
	MinDimension     = 1
	MaxDimension     = 16384
	MinJPEGQuality   = 1
	MaxJPEGQuality   = 100
	MaxFileSizeLimit = 500 << 20 // upper bound accepted for max_file_size_bytes
	MinFileSizeLimit = 64 << 10  // anything smaller cannot hold a 64px image reliably
	bytesPerMebibyte = 1 << 20
)

// ErrInvalid wraps validation failures so callers can show the message to users.
var ErrInvalid = errors.New("invalid configuration")

// Config is the effective configuration of one guild.
type Config struct {
	GuildID          snowflake.ID
	MaxWidth         int
	MaxHeight        int
	MaxFileSizeBytes *int64 // nil means disabled
	JPEGQuality      int
	PreserveAlpha    bool
	StripMetadata    bool
	Channels         []snowflake.ID // sorted ascending
}

// Validate checks every field against the accepted ranges.
func (c Config) Validate() error {
	if c.MaxWidth < MinDimension || c.MaxWidth > MaxDimension ||
		c.MaxHeight < MinDimension || c.MaxHeight > MaxDimension {
		return fmt.Errorf("%w: dimensions must be between %d and %d", ErrInvalid, MinDimension, MaxDimension)
	}
	if c.JPEGQuality < MinJPEGQuality || c.JPEGQuality > MaxJPEGQuality {
		return fmt.Errorf("%w: JPEG quality must be between %d and %d", ErrInvalid, MinJPEGQuality, MaxJPEGQuality)
	}
	if c.MaxFileSizeBytes != nil && (*c.MaxFileSizeBytes < MinFileSizeLimit || *c.MaxFileSizeBytes > MaxFileSizeLimit) {
		return fmt.Errorf("%w: maximum file size must be between %d KiB and %d MiB", ErrInvalid,
			MinFileSizeLimit>>10, MaxFileSizeLimit>>20)
	}
	return nil
}

// HasChannel reports whether channelID is on the auto-conversion allowlist.
func (c Config) HasChannel(channelID snowflake.ID) bool {
	_, found := slices.BinarySearch(c.Channels, channelID)
	return found
}

// Clone returns a deep copy so cached values are never mutated by callers.
func (c Config) Clone() Config {
	c.Channels = slices.Clone(c.Channels)
	if c.MaxFileSizeBytes != nil {
		v := *c.MaxFileSizeBytes
		c.MaxFileSizeBytes = &v
	}
	return c
}

// MebibytesToBytes converts a user-facing MiB value to bytes.
func MebibytesToBytes(mib float64) int64 {
	return int64(mib * bytesPerMebibyte)
}

package config

import "github.com/disgoorg/snowflake/v2"

// Defaults for a guild that has never been configured.
const (
	DefaultMaxWidth       = 1500
	DefaultMaxHeight      = 1500
	DefaultJPEGQuality    = 90
	DefaultPreserveAlpha  = true
	DefaultStripMetadata  = true
	DefaultDeleteOriginal = false
)

// Default returns the configuration a guild starts with: no channels, no file-size limit.
func Default(guildID snowflake.ID) Config {
	return Config{
		GuildID:        guildID,
		MaxWidth:       DefaultMaxWidth,
		MaxHeight:      DefaultMaxHeight,
		JPEGQuality:    DefaultJPEGQuality,
		PreserveAlpha:  DefaultPreserveAlpha,
		StripMetadata:  DefaultStripMetadata,
		DeleteOriginal: DefaultDeleteOriginal,
	}
}

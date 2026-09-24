package discordurl

import (
	"fmt"
	"strings"
	"time"

	"github.com/disgoorg/disgo/discord"
)

// RefreshButtonID is the custom ID of the Refresh URL button. It carries no state:
// the interaction itself identifies the message, so old buttons keep working after restarts.
const RefreshButtonID = "image_refresh_url"

const summaryPrefix = "Converted: "

const fieldSep = " · "

// expiryMarker identifies where the trailing expiry note starts on the first line, so
// SummaryFromContent can strip it back off.
const expiryMarker = fieldSep + "URL expires <t:"

// Summary renders the first line of a result message, e.g.
// "Converted: 3024×4032 → 1125×1500 · JPEG · 1.8 MiB".
func Summary(inW, inH, outW, outH int, format string, size int) string {
	return fmt.Sprintf("%s%d×%d → %d×%d · %s · %s", summaryPrefix, inW, inH, outW, outH, strings.ToUpper(format), FormatSize(size))
}

// FormatSize renders a byte count using binary units.
func FormatSize(n int) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}

// ResultContent builds the full result message. The URL sits alone in a code block
// so it can be selected and copied in VR. A zero expiresAt omits the expiry note.
func ResultContent(summary, attachmentURL string, expiresAt time.Time) string {
	var b strings.Builder
	if summary != "" {
		b.WriteString(summary)
	}
	if !expiresAt.IsZero() {
		if summary != "" {
			b.WriteString(fieldSep)
		}
		fmt.Fprintf(&b, "URL expires <t:%d:R>", expiresAt.Unix())
	}
	if summary != "" || !expiresAt.IsZero() {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	b.WriteString(attachmentURL)
	b.WriteString("\n```")
	return b.String()
}

// SummaryFromContent recovers the summary line from an existing result message,
// stripping the trailing expiry note if present.
func SummaryFromContent(content string) string {
	line, _, _ := strings.Cut(content, "\n")
	if !strings.HasPrefix(line, summaryPrefix) {
		return ""
	}
	if i := strings.Index(line, expiryMarker); i != -1 {
		line = line[:i]
	}
	return line
}

// Components returns the action row holding the Refresh URL button.
func Components() []discord.LayoutComponent {
	return []discord.LayoutComponent{
		discord.NewActionRow(discord.NewSecondaryButton("Refresh URL", RefreshButtonID)),
	}
}

package discordurl

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseExpiry(t *testing.T) {
	got, err := ParseExpiry("https://cdn.discordapp.com/attachments/1/2/a.jpg?ex=6ABCD123&is=6ABB7FA3&hm=deadbeef&")
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(0x6ABCD123); got.Unix() != want {
		t.Fatalf("expiry = %d, want %d", got.Unix(), want)
	}
}

func TestParseExpiryMissing(t *testing.T) {
	_, err := ParseExpiry("https://cdn.discordapp.com/attachments/1/2/a.jpg?is=1&hm=2")
	if !errors.Is(err, ErrNoExpiry) {
		t.Fatalf("err = %v, want ErrNoExpiry", err)
	}
}

func TestParseExpiryInvalidHex(t *testing.T) {
	if _, err := ParseExpiry("https://cdn.discordapp.com/a.jpg?ex=zzzz"); err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestRedactDropsSignature(t *testing.T) {
	got := Redact("https://cdn.discordapp.com/attachments/1/2/a.jpg?ex=1&is=2&hm=secret")
	if strings.Contains(got, "secret") || got != "cdn.discordapp.com/attachments/1/2/a.jpg" {
		t.Fatalf("Redact = %q", got)
	}
}

func TestResultContentRoundTrip(t *testing.T) {
	summary := Summary(3024, 4032, 1125, 1500, "jpeg", 1887437)
	if summary != "Converted: 3024×4032 → 1125×1500 · JPEG · 1.8 MiB" {
		t.Fatalf("summary = %q", summary)
	}
	url := "https://cdn.discordapp.com/attachments/1/2/converted-a8f31c.jpg?ex=6ABCD123"
	content := ResultContent(summary, url, time.Unix(1790232540, 0))
	want := summary + " · URL expires <t:1790232540:R>\n```\n" + url + "\n```"
	if content != want {
		t.Fatalf("content =\n%s\nwant\n%s", content, want)
	}
	if got := SummaryFromContent(content); got != summary {
		t.Fatalf("SummaryFromContent = %q", got)
	}
	if got := SummaryFromContent("something else"); got != "" {
		t.Fatalf("SummaryFromContent on foreign content = %q", got)
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[int]string{512: "512 B", 866304: "846 KiB", 1887437: "1.8 MiB"}
	for n, want := range cases {
		if got := FormatSize(n); got != want {
			t.Errorf("FormatSize(%d) = %q, want %q", n, got, want)
		}
	}
}

package handlers

import (
	"testing"
	"time"
)

// TestTTLDeadline pins the ttl grammar: any positive amount with one of the
// five units, calendar steps for months/years, and a rejection for everything
// else. The old preset spellings are in the table because they must keep
// parsing — links and MCP callers already speak them.
func TestTTLDeadline(t *testing.T) {
	// A day-of-month that exists in every month, so the calendar cases have
	// one unambiguous answer.
	now := time.Date(2026, time.January, 15, 10, 30, 0, 0, time.UTC)

	valid := []struct {
		spec string
		want time.Time
	}{
		{"24h", now.Add(24 * time.Hour)},
		{"48h", now.Add(48 * time.Hour)},
		{"72h", now.Add(72 * time.Hour)},
		{"7d", now.AddDate(0, 0, 7)},
		{"30d", now.AddDate(0, 0, 30)},
		{"1h", now.Add(time.Hour)},
		{"36h", now.Add(36 * time.Hour)},
		{"1d", now.AddDate(0, 0, 1)},
		{"2w", now.AddDate(0, 0, 14)},
		// Calendar steps, not 30-day and 365-day approximations.
		{"1mo", time.Date(2026, time.February, 15, 10, 30, 0, 0, time.UTC)},
		{"6mo", time.Date(2026, time.July, 15, 10, 30, 0, 0, time.UTC)},
		{"1y", time.Date(2027, time.January, 15, 10, 30, 0, 0, time.UTC)},
		{"10y", time.Date(2036, time.January, 15, 10, 30, 0, 0, time.UTC)},
	}
	for _, c := range valid {
		got, errMsg := ttlDeadline(c.spec, now)
		if errMsg != "" {
			t.Errorf("ttlDeadline(%q) = error %q, want %v", c.spec, errMsg, c.want)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("ttlDeadline(%q) = %v, want %v", c.spec, got, c.want)
		}
	}

	invalid := []string{
		"",
		"0h",     // zero is not a lifetime
		"0mo",    //
		"-1d",    // the sign is not part of the grammar
		"1.5d",   //
		"7",      // no unit
		"d",      // no amount
		"99x",    // unknown unit
		"7D",     // case-sensitive
		"1m",     // ambiguous minute/month spelling, deliberately not accepted
		"1 d",    //
		"7days",  //
		"11y",    // over the sanity cap
		"200mo",  // same cap, reached through a different unit
		"999999", //
	}
	for _, spec := range invalid {
		if _, errMsg := ttlDeadline(spec, now); errMsg == "" {
			t.Errorf("ttlDeadline(%q) was accepted, want an error", spec)
		}
	}
}

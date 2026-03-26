package builtins

import (
	"testing"
	"time"
)

func TestFormatDateStringGNUModifiers(t *testing.T) {
	t.Parallel()

	utc530 := time.FixedZone("+0530", 5*3600+30*60)
	when := time.Date(1999, time.June, 1, 5, 4, 3, 123, utc530)

	tests := []struct {
		format string
		want   string
	}{
		{format: "%10Y", want: "0000001999"},
		{format: "%_10m", want: "         6"},
		{format: "%-10Y", want: "1999"},
		{format: "%^B", want: "JUNE"},
		{format: "%10Y-%_5m-%-5d", want: "0000001999-    6-1"},
		{format: "%::z", want: "+05:30:00"},
		{format: "%:::z", want: "+05:30"},
		{format: "%N", want: "000000123"},
	}

	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()
			got, err := formatDateString(when, tc.format)
			if err != nil {
				t.Fatalf("formatDateString(%q) error = %v", tc.format, err)
			}
			if got != tc.want {
				t.Fatalf("formatDateString(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}

	signed, err := formatDateString(time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC), "%+6Y")
	if err != nil {
		t.Fatalf("formatDateString(%q) error = %v", "%+6Y", err)
	}
	if got, want := signed, "+01970"; got != want {
		t.Fatalf("formatDateString(%q) = %q, want %q", "%+6Y", got, want)
	}
}

func TestFormatDateStringPreservesIncompleteDirectiveLiterally(t *testing.T) {
	t.Parallel()

	when := time.Date(1999, time.June, 1, 5, 4, 3, 0, time.UTC)
	got, err := formatDateString(when, "%Y%")
	if err != nil {
		t.Fatalf("formatDateString(%q) error = %v", "%Y%", err)
	}
	if want := "1999%"; got != want {
		t.Fatalf("formatDateString(%q) = %q, want %q", "%Y%", got, want)
	}
}

func TestParseDateValueVariants(t *testing.T) {
	t.Parallel()

	base := time.Date(2024, time.May, 6, 7, 8, 9, 0, time.UTC)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "pure digits", input: "930", want: "2024-05-06 09:30:00 UTC"},
		{name: "relative ago", input: "1 hour ago", want: "2024-05-06 06:08:09 UTC"},
		{name: "next weekday", input: "next Fri", want: "2024-05-10 07:08:09 UTC"},
		{name: "keyword with time zone", input: "yesterday 10:00 GMT", want: "2024-05-05 10:00:00 UTC"},
		{name: "comment stripping", input: "2026(this is a comment)-01-05", want: "2026-01-05 00:00:00 UTC"},
		{name: "epoch fractional", input: "@1.5", want: "1970-01-01 00:00:01 UTC"},
		{name: "military", input: "m9", want: "2024-05-05 21:00:00 UTC"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := parseDateValue(tc.input, base, time.UTC)
			if err != nil {
				t.Fatalf("parseDateValue(%q) error = %v", tc.input, err)
			}
			if text := got.Format("2006-01-02 15:04:05 MST"); text != tc.want {
				t.Fatalf("parseDateValue(%q) = %q, want %q", tc.input, text, tc.want)
			}
		})
	}
}

func TestParseDateLegacyTimestamp(t *testing.T) {
	t.Parallel()

	base := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	got, err := parseDateLegacyTimestamp("050607082024.11", base, time.UTC)
	if err != nil {
		t.Fatalf("parseDateLegacyTimestamp() error = %v", err)
	}
	if text, want := got.Format("2006-01-02 15:04:05 MST"), "2024-05-06 07:08:11 UTC"; text != want {
		t.Fatalf("parseDateLegacyTimestamp() = %q, want %q", text, want)
	}
}

func TestResolveDateLocationValueSupportsPOSIXTZ(t *testing.T) {
	t.Parallel()

	loc, err := resolveDateLocationValue("EST5")
	if err != nil {
		t.Fatalf("resolveDateLocationValue(%q) error = %v", "EST5", err)
	}
	when := time.Date(1970, time.January, 1, 0, 0, 0, 0, loc)
	if got, want := when.Format("2006-01-02 15:04:05 MST -0700"), "1970-01-01 00:00:00 EST -0500"; got != want {
		t.Fatalf("resolved EST5 = %q, want %q", got, want)
	}
}

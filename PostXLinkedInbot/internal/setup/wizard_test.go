package setup

import "testing"

func TestSanitizeToken(t *testing.T) {
	t.Run("trims", func(t *testing.T) {
		got := sanitizeToken("  abc  ")
		if got != "abc" {
			t.Fatalf("got %q", got)
		}
	})
	t.Run("strips bearer case-insensitive", func(t *testing.T) {
		cases := []string{"Bearer abc", "bearer abc", "BEARER abc"}
		for _, in := range cases {
			got := sanitizeToken(in)
			if got != "abc" {
				t.Fatalf("in=%q got=%q", in, got)
			}
		}
	})
}

func TestParseLinkedInAuthor(t *testing.T) {
	t.Run("accepts urn", func(t *testing.T) {
		in := "urn:li:person:123"
		got, ok := parseLinkedInAuthor(in)
		if !ok || got != in {
			t.Fatalf("ok=%v got=%q", ok, got)
		}
	})
	t.Run("accepts person shorthand", func(t *testing.T) {
		got, ok := parseLinkedInAuthor("person:123")
		if !ok || got != "urn:li:person:123" {
			t.Fatalf("ok=%v got=%q", ok, got)
		}
	})
	t.Run("accepts org shorthand", func(t *testing.T) {
		got, ok := parseLinkedInAuthor("org:456")
		if !ok || got != "urn:li:organization:456" {
			t.Fatalf("ok=%v got=%q", ok, got)
		}
	})
	t.Run("rejects empty", func(t *testing.T) {
		_, ok := parseLinkedInAuthor("  ")
		if ok {
			t.Fatalf("expected reject")
		}
	})
}

package core

import "testing"

func TestSuggestedPeerName(t *testing.T) {
	peers := map[string]string{"laptop": "key"}
	for _, comment := range []string{"phone", "世界", "user@host", "Self", "tab\vname"} {
		if got := SuggestedPeerName(comment, peers); got != comment {
			t.Errorf("valid unused comment %q: got %q", comment, got)
		}
	}
	for _, comment := range []string{"", "self", "laptop", "two words", " leading", "trailing ", "a\tb", "a\rb", "a\nb", "a\x00b"} {
		if got := SuggestedPeerName(comment, peers); got != "" {
			t.Errorf("ineligible comment %q: got %q", comment, got)
		}
	}
}

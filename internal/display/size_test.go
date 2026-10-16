package display

import "testing"

func TestSizeUnitsAndRounding(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"}, {1023, "1023 B"}, {1024, "1.0 KiB"},
		{1280, "1.2 KiB"}, {193638, "189.1 KiB"},
		{1048575, "1024.0 KiB"}, {1048576, "1.0 MiB"}, {1 << 30, "1024.0 MiB"},
	} {
		if got := Size(tc.bytes); got != tc.want {
			t.Errorf("Size(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

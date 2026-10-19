package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestSettingsLabelClassifiesBuildProvenance(t *testing.T) {
	const revision = "1234567890abcdef1234567890abcdef12345678"
	for _, tc := range []struct {
		name, version, revision string
		info                    *debug.BuildInfo
		want                    string
	}{
		{"release", "0.1.0-rc.4", revision, nil, "keyparcel 0.1.0-rc.4"},
		{"different release", "2.3.4", revision, nil, "keyparcel 2.3.4"},
		{"dirty candidate", "0.1.0-rc.4", revision + "-dirty", nil, "keyparcel dev · 1234567890ab · modified"},
		{"manual Git", "development", "unknown", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: revision}, {Key: "vcs.modified", Value: "false"}}}, "keyparcel dev · 1234567890ab"},
		{"dirty Git", "development", "unknown", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}, {Key: "vcs.revision", Value: revision}}}, "keyparcel dev · 1234567890ab · modified"},
		{"missing metadata", "development", "unknown", nil, "keyparcel dev · revision unknown"},
		{"missing Git stamp", "development", "unknown", &debug.BuildInfo{}, "keyparcel dev · revision unknown"},
		{"dirty without revision", "development", "unknown", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}}, "keyparcel dev · revision unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := settingsLabel(tc.version, tc.revision, tc.info); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAndroidLabelUsesAppProvenance(t *testing.T) {
	for _, tc := range []struct{ version, revision, want string }{
		{"development", "abcdef0123456789", "keyparcel dev · abcdef012345"},
		{"development", "abcdef0123456789-dirty", "keyparcel dev · abcdef012345 · modified"},
		{"development", "unknown", "keyparcel dev · revision unknown"},
		{"development", "development", "keyparcel dev · revision unknown"},
		{"", "", "keyparcel dev · revision unknown"},
		{"0.1.0-rc.4", "abcdef0123456789", "keyparcel 0.1.0-rc.4"},
	} {
		if got := Label(tc.version, tc.revision); got != tc.want {
			t.Fatalf("%q/%q: got %q, want %q", tc.version, tc.revision, got, tc.want)
		}
	}
}

// Package buildinfo exposes provenance injected by candidate builds.
package buildinfo

import (
	"fmt"
	"runtime/debug"
	"strings"
)

var Version = "development"
var Revision = "unknown"

func String() string { return fmt.Sprintf("%s (%s)", Version, Revision) }

// SettingsLabel uses explicit release metadata or the executable's Git stamp.
func SettingsLabel() string {
	info, _ := debug.ReadBuildInfo()
	return settingsLabel(Version, Revision, info)
}

func settingsLabel(version, revision string, info *debug.BuildInfo) string {
	if version == "" || version == "development" {
		revision = "unknown"
		modified := false
		if info != nil {
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					revision = setting.Value
				case "vcs.modified":
					modified = setting.Value == "true"
				}
			}
		}
		if modified {
			revision += "-dirty"
		}
	}
	return Label(version, revision)
}

// Label also accepts the Android app's independently stamped build metadata.
func Label(version, revision string) string {
	modified := strings.HasSuffix(revision, "-dirty")
	if version != "" && version != "development" && !modified {
		return "keyparcel " + version
	}
	revision = strings.TrimSuffix(revision, "-dirty")
	if revision == "" || revision == "unknown" || revision == "development" {
		return "keyparcel dev · revision unknown"
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	label := "keyparcel dev · " + revision
	if modified {
		label += " · modified"
	}
	return label
}

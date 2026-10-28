package diagnostics

import (
	"regexp"
	"strings"
)

var releaseVersion = regexp.MustCompile(`^(v?(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8})\.(?:0|[1-9][0-9]{0,8}))(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

// VersionObservation deliberately omits arbitrary prerelease/build strings.
// Agent hello and custom build labels can contain addresses or other private text.
type VersionObservation struct {
	Status        string `json:"status"`
	Base          string `json:"base,omitempty"`
	Prerelease    bool   `json:"prerelease"`
	BuildMetadata bool   `json:"build_metadata"`
}

func ObserveVersion(raw string) VersionObservation {
	if raw == "dev" {
		return VersionObservation{Status: "development"}
	}
	if raw == "ci" {
		return VersionObservation{Status: "ci"}
	}
	if len(raw) > 128 {
		return VersionObservation{Status: "unavailable"}
	}
	match := releaseVersion.FindStringSubmatch(raw)
	if match == nil {
		return VersionObservation{Status: "unavailable"}
	}
	return VersionObservation{Status: "release", Base: strings.TrimPrefix(match[1], "v"), Prerelease: match[2] != "", BuildMetadata: match[3] != ""}
}

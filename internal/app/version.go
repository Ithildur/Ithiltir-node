package app

// Version is set at build time via -ldflags.
// Format: MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD], strict SemVer without a v prefix.
var Version = "0.1.0-alpha.1"

func VersionString() string {
	return Version
}

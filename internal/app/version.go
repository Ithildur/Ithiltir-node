package app

// Version is set at build time via -ldflags.
// Format: x.y.z.a[-αN], where x/y/z/a are non-negative integers and α is one Greek letter or alpha/beta/gamma/delta/epsilon/rc.
var Version = "1.0.0.0-alpha1"

func VersionString() string {
	return Version
}

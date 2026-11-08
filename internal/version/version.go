// Package version holds build information set at link time.
package version

// These values are overridden with -ldflags "-X ..." by the build.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

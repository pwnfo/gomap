package version

import (
	"fmt"
	"runtime"
)

var (
	// Version is the semantic version of Gomap (overridable at link time via -ldflags).
	Version = "1.0.0"
	// Commit is the optional git commit hash injected at build time.
	Commit = ""
	// Author credits.
	Author = "Ryan R. <pwnfo@proton.me>"
	// Description is the short tagline shown in CLI banners and help output.
	Description = "Generate high-performance wordlists using pattern logic and expressions."
	// RepoURL is the canonical repository URL.
	RepoURL = "https://github.com/pwnfo/gomap"
)

// Full returns the formatted version string including Go runtime and target architecture.
func Full() string {
	if Commit != "" {
		return fmt.Sprintf("Gomap v%s (%s, %s %s/%s)", Version, Commit, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("Gomap v%s (%s %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

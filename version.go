package main

import "runtime/debug"

// version can be set by packagers with -ldflags "-X main.version=v1.2.3".
// Otherwise it comes from the build info Go embeds: the module version for
// `go install …@vX.Y.Z`, or a VCS-derived version for a local build.
var version = ""

func versionString() string {
	v := version
	if v == "" {
		v = "(devel)"
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
			v = bi.Main.Version
		}
	}
	return "tidefiles " + v
}

// Package version holds link-time build identity shared by the two commands.
package version

import "fmt"

var (
	Version         = "0.9.0"
	PackageRevision = "development"
	SourceCommit    = "unknown"
)

func Line(command string) string {
	return fmt.Sprintf("%s %s (main build %s; commit %s)", command, Version, PackageRevision, SourceCommit)
}

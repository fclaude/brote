//go:build !unix

package std

import "os/exec"

// ownGroup is a no-op where there are no process groups: only the child itself
// is killed on a timeout.
func ownGroup(cmd *exec.Cmd) {}

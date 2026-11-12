package std

import (
	"os"
	"path/filepath"
	"testing"
)

func mustAbs(t testing.TB, p string) string {
	t.Helper()
	p, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func nativeCommandPath() string {
	// The current working directory supplies a real Windows volume; this path
	// deliberately need not exist for syntax and round-trip tests.
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Join(filepath.VolumeName(cwd)+string(filepath.Separator), "tools", "jq")
}

package lsp

import (
	"path/filepath"
	"testing"
)

func TestNativeFileURI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sp ace #é.scm")
	uri := fileURI(path)
	got, ok := uriPath(uri)
	if !ok || got != path {
		t.Fatalf("%q -> %q -> %q, %v", path, uri, got, ok)
	}
	if uriKey(uri) != uriKey(fileURI(filepath.Clean(path))) {
		t.Fatal("file identity changed")
	}
	for _, invalid := range []string{"https://localhost/a", "file:///a?query", "file:///a#fragment"} {
		if _, ok := uriPath(invalid); ok {
			t.Errorf("accepted %q", invalid)
		}
	}
}

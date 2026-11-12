package lsp

import "testing"

func TestWindowsFileURIs(t *testing.T) {
	for path, uri := range map[string]string{
		`C:\work\a b.scm`:        "file:///C:/work/a%20b.scm",
		`\\server\share\a b.scm`: "file://server/share/a%20b.scm",
	} {
		if got := fileURI(path); got != uri {
			t.Errorf("fileURI(%q) = %q, want %q", path, got, uri)
		}
		if got, ok := uriPath(uri); !ok || got != path {
			t.Errorf("uriPath(%q) = %q, %v", uri, got, ok)
		}
	}
	if uriKey("file:///C:/Work/a.scm") != uriKey("file:///c:/work/a.scm") {
		t.Fatal("drive/case variants duplicated a document")
	}
	if got, ok := uriPath("file://localhost/C:/work/a.scm"); !ok || got != `C:\work\a.scm` {
		t.Fatalf("localhost: %q, %v", got, ok)
	}
}

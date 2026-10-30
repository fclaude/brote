package scheme

import "testing"

func TestFormat(t *testing.T) {
	cases := map[string]string{
		// A child is indented from where its parent ends up, not from where
		// the parent was before formatting.
		"(define x\n        (foo a\n             b))\n": "(define x\n  (foo a\n    b))\n",
		// Trailing whitespace inside a string is part of the string.
		"(define s \"one   \ntwo\")  \n":       "(define s \"one   \ntwo\")\n",
		"(define s \"\"\"one \n  two\"\"\")\n": "(define s \"\"\"one \n  two\"\"\")\n",
		// Outside a string it goes.
		"(f a)   \n  (g b)\t\n": "(f a)\n(g b)\n",
	}
	for src, want := range cases {
		got, err := Format(src)
		if err != nil {
			t.Fatalf("Format(%q): %v", src, err)
		}
		if got != want {
			t.Errorf("Format(%q) = %q, want %q", src, got, want)
		}
		again, err := Format(got)
		if err != nil || again != got {
			t.Errorf("Format is not idempotent on %q: %q", got, again)
		}
		// Formatting never changes what the source reads as.
		a, _ := Read(src)
		b, _ := Read(got)
		if len(a) != len(b) {
			t.Fatalf("Format(%q) changed the number of forms", src)
		}
		for i := range a {
			if Write(a[i]) != Write(b[i]) {
				t.Errorf("Format(%q) changed form %d: %s became %s", src, i, Write(a[i]), Write(b[i]))
			}
		}
	}
}

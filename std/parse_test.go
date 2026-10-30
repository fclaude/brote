package std

import "testing"

func TestParseINI(t *testing.T) {
	pol := `(policy (encoding))`
	expect(t, pol, map[string]string{
		// Two sections, comments, key=value with spaces, a valueless key.
		`(ini-parse "[server]\nhost = example.com\nport=8080\n; a comment\n# another\n[db]\nname\n")`:
			`(("server" ("host" . "example.com") ("port" . "8080")) ("db" ("name" . "")))`,
		// CRLF and LF parse the same; keys before any header are in "".
		"(ini-parse \"top=1\\r\\n[sec]\\r\\nk = v\\r\\n\")":
			`(("" ("top" . "1")) ("sec" ("k" . "v")))`,
		// A duplicate key keeps its last value.
		`(ini-parse "[s]\nk=1\nk=2\n")`: `(("s" ("k" . "2")))`,
		// Empty text is no sections.
		`(ini-parse "")`: `()`,
	})
	expectErr(t, pol, map[string]string{
		`(ini-parse "[bad")`:  "bad INI section header",
		`(ini-parse "=v")`:   "bad INI line",
	})
}

package std

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fclaude/brote/scheme"
)

// run evaluates src in a fresh interpreter under policy and returns the
// printed value of the last form.
func run(t testing.TB, policy, src string, opts ...Option) (string, error) {
	t.Helper()
	in := scheme.New()
	rt, err := Install(in, mustParse(t, policy), opts...)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	t.Cleanup(func() { rt.Close() })
	v, err := in.EvalString(context.Background(), src)
	if err != nil {
		return "", err
	}
	return scheme.Write(v), nil
}

func expect(t *testing.T, policy string, cases map[string]string) {
	t.Helper()
	for src, want := range cases {
		got, err := run(t, policy, src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
		} else if got != want {
			t.Errorf("%s\n got  %s\n want %s", src, got, want)
		}
	}
}

func expectErr(t *testing.T, policy string, cases map[string]string) {
	t.Helper()
	for src, want := range cases {
		_, err := run(t, policy, src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", src, err, want)
		}
	}
}

func TestPathModule(t *testing.T) {
	expect(t, `(policy (path))`, map[string]string{
		`(path-join "a" "b" "../c")`: `"a/c"`,
		`(path-base "a/b.txt")`:      `"b.txt"`,
		`(path-dir "a/b.txt")`:       `"a"`,
		`(path-ext "a/b.tar.gz")`:    `".gz"`,
		`(path-clean "a//b/./c/..")`: `"a/b"`,
	})
}

func TestEncodingModule(t *testing.T) {
	expect(t, `(policy (encoding))`, map[string]string{
		`(base64-encode "héllo")`:                    `"aMOpbGxv"`,
		`(base64-decode "aMOpbGxv")`:                 `"héllo"`,
		`(base64-encode "??>>" #t)`:                  `"Pz8-Pg"`,
		`(base64-decode "Pz8-Pg" #t)`:                `"??>>"`,
		`(hex-encode "hi")`:                          `"6869"`,
		`(hex-decode "6869")`:                        `"hi"`,
		`(url-encode "a b&c=d/é")`:                   `"a+b%26c%3Dd%2F%C3%A9"`,
		`(url-decode "a+b%26c")`:                     `"a b&c"`,
		`(csv-parse "a,b\n\"c,d\",\"e\"\"f\"\n")`:    `(("a" "b") ("c,d" "e\"f"))`,
		`(csv-parse "a;b\n" ";")`:                    `(("a" "b"))`,
		`(csv-emit '(("a" "b,c") (1 2.5 #t)))`:       `"a,\"b,c\"\n1,2.5,#t\n"`,
		`(csv-parse "")`:                             `()`,
		`(csv-parse (csv-emit '(("x\ny" "\"q\""))))`: `(("x\ny" "\"q\""))`,
	})
	expectErr(t, `(policy (encoding))`, map[string]string{
		`(base64-decode "!!!")`:  "not valid base64",
		`(base64-decode "/w==")`: "not UTF-8",
		`(hex-decode "zz")`:      "not valid hex",
		`(url-decode "%zz")`:     "bad escape",
		`(csv-parse "a" "ab")`:   "one character",
	})
}

func TestRegexpModule(t *testing.T) {
	expect(t, `(policy (regexp))`, map[string]string{
		`(regexp-matches? "^a+b$" "aaab")`:                             `#t`,
		`(regexp-matches? "^a+b$" "aaabc")`:                            `#f`,
		`(regexp-match "(\\d+)-(\\d+)?" "ab 12- cd")`:                  `("12-" "12" #f)`,
		`(regexp-match "x" "abc")`:                                     `#f`,
		`(regexp-match-all "[a-z]+(\\d)" "ab1 cd2 e")`:                 `(("ab1" "1") ("cd2" "2"))`,
		`(regexp-replace "(\\w+)@(\\w+)" "me@host you@there" "$2:$1")`: `"host:me there:you"`,
		`(regexp-split ",\\s*" "a, b,c")`:                              `("a" "b" "c")`,
		`(regexp-quote "a.b*c")`:                                       `"a\\.b\\*c"`,
		`(regexp-matches? (regexp-quote "1+1=2") "so 1+1=2!")`:         `#t`,
	})
	expectErr(t, `(policy (regexp))`, map[string]string{
		`(regexp-match "(" "x")`:             "bad pattern",
		`(regexp-match "a{1000}{1000}" "x")`: "bad pattern",
		`(regexp-match "(a)\\1" "aa")`:       "bad pattern", // RE2 has no backreferences
	})
}

// RE2 finishes in linear time on the input that makes backtracking engines hang.
func TestRegexpIsLinearTime(t *testing.T) {
	start := time.Now()
	got, err := run(t, `(policy (regexp))`, `(regexp-matches? "^(a+)+$" (string-append (make-string 5000 #\a) "b"))`)
	if err != nil {
		// make-string may not exist; fall back to building the text in Go-free Scheme.
		got, err = run(t, `(policy (regexp))`, `(regexp-matches? "^(a+)+$" (apply string-append (append (map (lambda (i) "a") (iota 5000)) '("b"))))`)
	}
	if err != nil || got != "#f" {
		t.Fatalf("got %s, %v", got, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestDigestModule(t *testing.T) {
	expect(t, `(policy (digest))`, map[string]string{
		`(sha256 "abc")`: `"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"`,
		`(sha1 "abc")`:   `"a9993e364706816aba3e25717850c26c9cd0d89d"`,
		`(md5 "abc")`:    `"900150983cd24fb0d6963f7d28e17f72"`,
		`(hmac-sha256 "key" "The quick brown fox jumps over the lazy dog")`: `"f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"`,
		`(string-length (sha512 ""))`:                                       `128`,
	})
}

func TestRandModule(t *testing.T) {
	expect(t, `(policy (rand))`, map[string]string{
		`(let ((n (rand-int 5))) (and (>= n 0) (< n 5)))`:        `#t`,
		`(let ((x (rand-float))) (and (>= x 0) (< x 1)))`:        `#t`,
		`(string-length (rand-hex 16))`:                          `32`,
		`(equal? (sort (rand-shuffle '(1 2 3 4)) <) '(1 2 3 4))`: `#t`,
		`(equal? (rand-hex 8) (rand-hex 8))`:                     `#f`,
	})
	expectErr(t, `(policy (rand))`, map[string]string{
		`(rand-int 0)`:      "positive",
		`(rand-hex 0)`:      "expected 1 to",
		`(rand-hex 999999)`: "expected 1 to",
	})
}

func TestTimeModule(t *testing.T) {
	expect(t, `(policy (time (max-sleep 1s)))`, map[string]string{
		`(time-format 0 "rfc3339")`:                                         `"1970-01-01T00:00:00Z"`,
		`(time-format 86400.5 "datetime")`:                                  `"1970-01-02 00:00:00"`,
		`(= (time-parse "2024-02-29" "date") 1709164800)`:                   `#t`,
		`(= (time-parse "2024-02-29T01:00:00+01:00" "rfc3339") 1709164800)`: `#t`,
		`(> (time-now) 1.7e9)`:                                              `#t`,
		`(> (time-ms) 1700000000000)`:                                       `#t`,
		`(time-sleep 0.01)`:                                                 `#<void>`,
	})
	expectErr(t, `(policy (time (max-sleep 1s)))`, map[string]string{
		`(time-sleep 5)`:                "more than",
		`(time-sleep -1)`:               "non-negative",
		`(time-format 0 "date" "mars")`: "utc",
		`(time-parse "nope" "date")`:    "parsing time",
	})
}

func TestSleepStopsWithTheContext(t *testing.T) {
	in := scheme.New()
	rt, err := Install(in, mustParse(t, `(policy (time (max-sleep 1h)))`))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = in.EvalString(ctx, `(time-sleep 3600)`)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("sleep ended after %v with %v", time.Since(start), err)
	}
	if scheme.Catchable(ctx, err) {
		t.Errorf("a cancelled sleep must not be catchable by guard: %v", err)
	}
}

func TestEnvModule(t *testing.T) {
	t.Setenv("BROTE_T_ONE", "1")
	t.Setenv("BROTE_T_TWO", "2")
	p := `(policy (env (allow "BROTE_T_ONE" "BROTE_T_MISSING")))`
	expect(t, p, map[string]string{
		`(env-get "BROTE_T_ONE")`:            `"1"`,
		`(env-get "BROTE_T_MISSING")`:        `#f`,
		`(env-get "BROTE_T_MISSING" 7)`:      `7`,
		`(env-list)`:                         `(("BROTE_T_ONE" . "1"))`,
		`(tainted? (env-get "BROTE_T_ONE"))`: `#t`,
	})
	expectErr(t, p, map[string]string{`(env-get "BROTE_T_TWO")`: "not granted"})
	got, err := run(t, `(policy (env (allow *)))`, `(assoc-ref (env-list) "BROTE_T_TWO")`)
	if err != nil || got != `"2"` {
		t.Errorf("(allow *): %s, %v", got, err)
	}
}

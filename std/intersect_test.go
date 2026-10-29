package std

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIntersectCases(t *testing.T) {
	for _, c := range []struct {
		name, host, req string
		want            string   // the granted policy, in policy syntax; "" for a policy granting nothing
		refused         []string // exactly these
	}{
		{"module not in host", `(policy (profile pure))`, `(policy (fs (read "/a")))`, `(policy)`, []string{"fs"}},
		{"module in both", `(policy (profile safe))`, `(policy (regexp) (rand))`, `(policy (regexp) (rand))`, nil},
		{"narrower path", `(policy (fs (read "/a")))`, `(policy (fs (read "/a/b")))`, `(policy (fs (read "/a/b") (max-file 16777216)))`, nil},
		{"wider path is narrowed", `(policy (fs (read "/a/b")))`, `(policy (fs (read "/a")))`, `(policy (fs (read "/a/b") (max-file 16777216)))`, []string{"fs read /a"}},
		{"disjoint path", `(policy (fs (read "/a")))`, `(policy (fs (read "/c")))`, `(policy)`, []string{"fs read /c"}},
		{"sibling prefix is not inside", `(policy (fs (read "/a")))`, `(policy (fs (read "/ab")))`, `(policy)`, []string{"fs read /ab"}},
		{"a write request is cut to read", `(policy (fs (read "/a")))`, `(policy (fs (write "/a")))`, `(policy (fs (read "/a") (max-file 16777216)))`, []string{"fs write /a"}},
		{"write grant covers a read request", `(policy (fs (write "/a")))`, `(policy (fs (read "/a/x")))`, `(policy (fs (read "/a/x") (max-file 16777216)))`, nil},
		{"unscoped host", `(policy (profile full))`, `(policy (fs (write "/a")))`, `(policy (fs (write "/a") (max-file 16777216)))`, nil},
		{"unscoped request", `(policy (fs (read "/a")))`, `(policy (fs (read *)))`, `(policy (fs (read "/a") (max-file 16777216)))`, []string{"fs read *"}},
		{"host exact", `(policy (net (allow "api.x.com")))`, `(policy (net (allow "api.x.com")))`, `(policy (net (allow "api.x.com") (methods "get" "head") (private #f) (max-body 5242880) (timeout "30s")))`, nil},
		{"host not allowed", `(policy (net (allow "api.x.com")))`, `(policy (net (allow "evil.com")))`, `(policy)`, []string{"net evil.com"}},
		{"wildcard narrows to exact", `(policy (net (allow "*.x.com")))`, `(policy (net (allow "api.x.com")))`, `(policy (net (allow "api.x.com") (methods "get" "head") (private #f) (max-body 5242880) (timeout "30s")))`, nil},
		{"apex is not under its wildcard", `(policy (net (allow "*.x.com")))`, `(policy (net (allow "x.com")))`, `(policy)`, []string{"net x.com"}},
		{"port narrows", `(policy (net (allow "x.com")))`, `(policy (net (allow "x.com:8443")))`, `(policy)`, []string{"net x.com:8443"}},
		{"private needs both", `(policy (net (allow "x.com")))`, `(policy (net (allow "x.com") (private #t)))`, `(policy (net (allow "x.com") (methods "get" "head") (private #f) (max-body 5242880) (timeout "30s")))`, []string{"net private"}},
		{"methods intersect", `(policy (net (allow "x.com") (methods get post)))`, `(policy (net (allow "x.com") (methods get delete)))`, `(policy (net (allow "x.com") (methods "get") (private #f) (max-body 5242880) (timeout "30s")))`, []string{"net method delete"}},
		{"limits take the smaller", `(policy (net (allow "x.com") (max-body 1MB) (timeout 5s)))`, `(policy (net (allow "x.com") (max-body 2MB) (timeout 1s)))`, `(policy (net (allow "x.com") (methods "get" "head") (private #f) (max-body 1048576) (timeout "1s")))`, nil},
		{"commands", `(policy (exec (allow "git" "ls")))`, `(policy (exec (allow "git" "rm")))`, `(policy (exec (allow "git") (timeout "30s") (max-output 1048576) (env (allow))))`, []string{"exec rm"}},
		{"command env", `(policy (exec (allow "git") (env (allow "PATH" "HOME"))))`, `(policy (exec (allow "git") (env (allow "PATH" "TOKEN"))))`, `(policy (exec (allow "git") (timeout "30s") (max-output 1048576) (env (allow "PATH"))))`, []string{"exec env TOKEN"}},
		{"env vars", `(policy (env (allow "A" "B")))`, `(policy (env (allow "B" "C")))`, `(policy (env (allow "B")))`, []string{"env C"}},
		{"sleep limit", `(policy (time (max-sleep 5s)))`, `(policy (time (max-sleep 60s)))`, `(policy (time (max-sleep "5s")))`, nil},
		{"nothing requested", `(policy (profile full))`, `(policy)`, `(policy)`, nil},
	} {
		h, r := mustParse(t, c.host), mustParse(t, c.req)
		got, refused := h.Intersect(r)
		want := mustParse(t, c.want)
		if !reflect.DeepEqual(got, want.Resolve("")) {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, want.Resolve(""))
		}
		if !reflect.DeepEqual(refused, c.refused) {
			t.Errorf("%s: refused %q, want %q", c.name, refused, c.refused)
		}
	}
}

func randomPolicy(rng *rand.Rand) *Policy {
	pick := func(pool []string) []string {
		var out []string
		for _, s := range pool {
			if rng.IntN(2) == 0 {
				out = append(out, s)
			}
		}
		return out
	}
	p := &Policy{Modules: map[string]bool{}}
	for _, m := range []string{Path, Regexp, Rand, Time} {
		if rng.IntN(2) == 0 {
			p.Modules[m] = true
		}
	}
	if p.Modules[Time] {
		p.Time.MaxSleep = time.Duration(1+rng.IntN(3)) * time.Second
	}
	if rng.IntN(5) == 0 {
		p.profile("full")
		if rng.IntN(2) == 0 { // scope something back down
			p.FS = FSScope{Read: pick([]string{"/a", "/a/b", "/d"})}
			if len(p.FS.Read) == 0 {
				delete(p.Modules, FS)
			}
		}
		p.normalize()
		return p
	}
	if v := pick([]string{"A", "B", "C"}); len(v) > 0 {
		p.Modules[Env], p.Env.Allow = true, v
	}
	rd, wr := pick([]string{"/a", "/a/b", "/a/b/c", "/d"}), pick([]string{"/a", "/a/b", "/d"})
	if len(rd)+len(wr) > 0 {
		p.Modules[FS], p.FS.Read, p.FS.Write, p.FS.MaxFile = true, rd, wr, int64(1+rng.IntN(3))*mib
	}
	if v := pick([]string{"x.com", "a.x.com", "*.x.com", "*.x.com:443", "x.com:8080", "[::1]:80"}); len(v) > 0 {
		p.Modules[Net] = true
		p.Net.Allow = v
		p.Net.Methods = pick([]string{"get", "head", "post", "delete"})
		p.Net.Private = rng.IntN(2) == 0
		p.Net.MaxBody = int64(1+rng.IntN(3)) * mib
		p.Net.Timeout = time.Duration(1+rng.IntN(3)) * time.Second
		if len(p.Net.Methods) == 0 {
			p.Net.Methods = []string{"get"}
		}
	}
	if v := pick([]string{"git", "ls", "rm"}); len(v) > 0 {
		p.Modules[Exec] = true
		p.Exec.Allow = v
		p.Exec.Env.Allow = pick([]string{"PATH", "HOME"})
		p.Exec.Timeout = time.Duration(1+rng.IntN(3)) * time.Second
	}
	p.normalize()
	return p
}

// The property that makes plugins safe: whatever a plugin asks for, the grant
// is within what the host holds and within what the plugin asked.
func TestIntersectNeverWidens(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	within := func(a, b *Policy) bool {
		got, refused := b.Intersect(a) // a is within b iff b grants all of a
		return len(refused) == 0 && reflect.DeepEqual(got, a.Resolve(""))
	}
	for i := 0; i < 3000; i++ {
		h, r := randomPolicy(rng), randomPolicy(rng)
		eff, _ := h.Intersect(r)
		if !within(eff, h) {
			t.Fatalf("grant is not within the host\nhost %s\nreq  %s\neff  %s", h, r, eff)
		}
		if !within(eff, r) {
			t.Fatalf("grant is not within the request\nhost %s\nreq  %s\neff  %s", h, r, eff)
		}
		back, _ := r.Intersect(h)
		if !reflect.DeepEqual(eff, back) {
			t.Fatalf("intersection depends on order\nhost %s\nreq  %s\n h∩r %s\n r∩h %s", h, r, eff, back)
		}
		if again, refused := h.Intersect(h); len(refused) != 0 || !reflect.DeepEqual(again, h.Resolve("")) {
			t.Fatalf("a policy narrowed by itself changed\n%s\n%s %v", h, again, refused)
		}
		// Whatever the result, a policy made of it must install.
		if _, err := ParsePolicy(eff.String()); err != nil {
			t.Fatalf("grant does not print as a policy: %s: %v", eff, err)
		}
		// A request that is within the host is granted whole.
		if within(r, h) {
			if got, _ := h.Intersect(r); !reflect.DeepEqual(got, r.Resolve("")) {
				t.Fatalf("a request within the host was cut\nhost %s\nreq  %s\ngot  %s", h, r, got)
			}
		}
	}
}

func TestIntersectResolvesRelativePaths(t *testing.T) {
	h := mustParse(t, `(policy (fs (read "data")))`).Resolve("/base")
	r := mustParse(t, `(policy (fs (read "data/sub")))`).Resolve("/base")
	got, refused := h.Intersect(r)
	if len(refused) != 0 || !reflect.DeepEqual(got.FS.Read, []string{"/base/data/sub"}) {
		t.Errorf("got %v, %v", got.FS.Read, refused)
	}
	r = mustParse(t, `(policy (fs (read "data/../../etc")))`).Resolve("/base")
	if got, refused := h.Intersect(r); got.Has(FS) || !strings.Contains(strings.Join(refused, ";"), "fs read /etc") {
		t.Errorf(".. must not widen: %v, %v", got.FS.Read, refused)
	}
}

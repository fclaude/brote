package std

import (
	"path/filepath"
	"slices"
	"strings"
)

// Intersect narrows p by req and returns what both allow, with a note for each
// thing req asked for that p does not allow. It can only narrow: every grant of
// the result is within p and within req. This is how a host limits a plugin to
// what the plugin asked for and the host allows.
//
// A scoped module whose scope ends up empty (the plugin asked for a host p does
// not allow) is left out of the result and noted. Relative directory trees are
// taken against the current directory, so Resolve both policies first when
// they were written against different ones.
func (p *Policy) Intersect(req *Policy) (*Policy, []string) {
	h, r := p.Resolve(""), req.Resolve("")
	eff := &Policy{Modules: map[string]bool{}}
	var refused []string
	note := func(mod string, items []string) {
		for _, it := range items {
			refused = append(refused, mod+" "+it)
		}
	}
	for _, m := range Modules {
		if !r.Modules[m] {
			continue
		}
		if !h.Modules[m] {
			refused = append(refused, m)
			continue
		}
		eff.Modules[m] = true
	}

	if eff.Modules[Env] {
		set, ref := meet(strSet{h.Env.Any, h.Env.Allow}, strSet{r.Env.Any, r.Env.Allow}, equalCovers)
		eff.Env = EnvScope{Any: set.any, Allow: set.items}
		note(Env, ref)
		if set.empty() {
			delete(eff.Modules, Env)
		}
	}
	if eff.Modules[FS] {
		hr := strSet{h.FS.AnyRead || h.FS.AnyWrite, append(slices.Clone(h.FS.Read), h.FS.Write...)}
		// Write includes read, so what the plugin asks to write it also asks to read.
		rr := strSet{r.FS.AnyRead || r.FS.AnyWrite, append(slices.Clone(r.FS.Read), r.FS.Write...)}
		rd, _ := meet(hr, rr, pathCovers)
		_, ref1 := meet(hr, strSet{r.FS.AnyRead, r.FS.Read}, pathCovers)
		wr, ref2 := meet(strSet{h.FS.AnyWrite, h.FS.Write}, strSet{r.FS.AnyWrite, r.FS.Write}, pathCovers)
		eff.FS = FSScope{AnyRead: rd.any, Read: rd.items, AnyWrite: wr.any, Write: wr.items, MaxFile: min(h.FS.MaxFile, r.FS.MaxFile)}
		note("fs read", ref1)
		note("fs write", ref2)
		if rd.empty() && wr.empty() {
			delete(eff.Modules, FS)
		}
	}
	if eff.Modules[Net] {
		hosts, ref := meet(strSet{h.Net.Any, h.Net.Allow}, strSet{r.Net.Any, r.Net.Allow}, hostCovers)
		methods, refM := meet(strSet{false, h.Net.Methods}, strSet{false, r.Net.Methods}, equalCovers)
		eff.Net = NetScope{
			Any: hosts.any, Allow: hosts.items, Methods: methods.items,
			Private: h.Net.Private && r.Net.Private,
			MaxBody: min(h.Net.MaxBody, r.Net.MaxBody),
			Timeout: min(h.Net.Timeout, r.Net.Timeout),
		}
		note(Net, ref)
		note("net method", refM)
		if hosts.empty() || methods.empty() {
			delete(eff.Modules, Net)
		}
		if r.Net.Private && !h.Net.Private {
			refused = append(refused, "net private")
		}
	}
	if eff.Modules[Exec] {
		cmds, ref := meet(strSet{h.Exec.Any, h.Exec.Allow}, strSet{r.Exec.Any, r.Exec.Allow}, equalCovers)
		env, refE := meet(strSet{h.Exec.Env.Any, h.Exec.Env.Allow}, strSet{r.Exec.Env.Any, r.Exec.Env.Allow}, equalCovers)
		eff.Exec = ExecScope{
			Any: cmds.any, Allow: cmds.items,
			Env:     EnvScope{Any: env.any, Allow: env.items},
			Timeout: min(h.Exec.Timeout, r.Exec.Timeout), MaxOutput: min(h.Exec.MaxOutput, r.Exec.MaxOutput),
		}
		note(Exec, ref)
		note("exec env", refE)
		if cmds.empty() {
			delete(eff.Modules, Exec)
		}
	}
	if eff.Modules[Time] {
		eff.Time = TimeScope{MaxSleep: min(h.Time.MaxSleep, r.Time.MaxSleep)}
	}
	if eff.Modules[Digest] {
		keys, ref := meet(strSet{h.Digest.Any, h.Digest.Keys}, strSet{r.Digest.Any, r.Digest.Keys}, equalCovers)
		eff.Digest = DigestScope{Any: keys.any, Keys: keys.items}
		note("digest key", ref)
		// Hashing and signing need no keys, so an empty key list keeps the module.
	}
	eff.normalize()
	return eff, refused
}

// strSet is a set of names, or everything.
type strSet struct {
	any   bool
	items []string
}

func (s strSet) empty() bool { return !s.any && len(s.items) == 0 }

// meet returns what host and req both allow, and the entries of req that host
// does not cover. covers(h, r) says whether entry h includes everything entry
// r does; where two entries overlap, the narrower one is what both allow.
func meet(host, req strSet, covers func(h, r string) bool) (strSet, []string) {
	switch {
	case host.any:
		return req, nil
	case req.any:
		return strSet{items: slices.Clone(host.items)}, []string{"*"}
	}
	var out strSet
	for _, h := range host.items {
		for _, r := range req.items {
			switch {
			case covers(h, r):
				out.items = append(out.items, r)
			case covers(r, h):
				out.items = append(out.items, h)
			}
		}
	}
	slices.Sort(out.items)
	out.items = slices.Compact(out.items)
	var refused []string
	for _, r := range req.items {
		if !slices.ContainsFunc(host.items, func(h string) bool { return covers(h, r) }) {
			refused = append(refused, r)
		}
	}
	return out, refused
}

func equalCovers(h, r string) bool { return h == r }

// pathCovers reports whether directory h contains directory r (or is it).
func pathCovers(h, r string) bool {
	rel, err := filepath.Rel(h, r)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hostCovers reports whether net pattern h admits every request pattern r does.
func hostCovers(h, r string) bool {
	hh, hp, err1 := splitHostPattern(h)
	rh, rp, err2 := splitHostPattern(r)
	if err1 != nil || err2 != nil {
		return false
	}
	switch {
	case hp == "":
		if rp != "" && rp != "80" && rp != "443" {
			return false
		}
	case hp != rp:
		return false
	}
	if hh == rh {
		return true
	}
	suffix, ok := strings.CutPrefix(hh, "*")
	if !ok {
		return false
	}
	// *.example.com covers a.example.com and *.b.example.com, not example.com.
	rhost := strings.TrimPrefix(rh, "*")
	return strings.HasSuffix(rhost, suffix) && rhost != suffix
}

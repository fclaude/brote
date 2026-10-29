package std

import (
	"context"
	"errors"

	"github.com/fclaude/brote/scheme"
)

// Gate returns a scheme.Gate that closes the one door std cannot: the core
// builtin load, which reads any file the process can. Under this gate load
// reads only what fs-read-text could, and not at all when the fs module is not
// granted. Everything else goes to next, which may be nil. Install it on an
// interpreter that runs code you do not trust:
//
//	in.SetGate(rt.Gate(nil))
func (r *Runtime) Gate(next scheme.Gate) scheme.Gate { return &loadGate{rt: r, next: next} }

type loadGate struct {
	rt   *Runtime
	next scheme.Gate
}

func (g *loadGate) Before(ctx context.Context, p *scheme.Prim, args []scheme.Value) error {
	if p.Name == "load" && p.Meta.Origin == "builtin" {
		if err := g.checkLoad(args); err != nil {
			return &scheme.Denied{Prim: p.Name, Reason: err.Error()}
		}
	}
	if g.next != nil {
		return g.next.Before(ctx, p, args)
	}
	return nil
}

// checkLoad allows a path only if the fs grant lets the script read it, with
// the operating system's own containment check (a symbolic link out of the
// tree fails here, as it does for fs-read-text).
func (g *loadGate) checkLoad(args []scheme.Value) error {
	if g.rt.fs == nil {
		return errors.New("load is not available: the fs module is not granted")
	}
	if len(args) == 0 || args[0].Kind != scheme.KString {
		return nil // the arity and type errors are load's own
	}
	r, name, err := g.rt.fs.resolve(args[0].S, false)
	if err != nil {
		return err
	}
	root, err := r.open()
	if err != nil {
		return err
	}
	if _, err := root.Stat(name); err != nil {
		return g.rt.fs.fail(args[0].S, err)
	}
	return nil
}

func (g *loadGate) RegisterChecker(name, verdict string, predicate scheme.Value) error {
	if g.next != nil {
		return g.next.RegisterChecker(name, verdict, predicate)
	}
	return errors.New("register-check is not available")
}

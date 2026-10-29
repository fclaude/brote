package analysis

import (
	"fmt"
	"strings"

	"github.com/fclaude/brote/scheme"
)

// Warnings for the mistakes that tasks make easy. The memory model keeps a
// script from corrupting anything, but a sequence of operations on shared data
// is still not atomic, and the editor can see most of the ways to write one.
// They are warnings, since the analysis cannot tell what a procedure value
// does, and are about the shapes that nearly always are wrong.

// warning is the LSP severity for a diagnostic that is advice.
const warning = 2

// taskThunks says, for the procedures that run one of their arguments as a
// task, which arguments are the procedures that run (counting the operator as
// argument 0). race takes a list of them.
var taskThunks = map[string][]int{
	"spawn":       {1},
	"group-spawn": {2},
	"pmap":        {1},
	"pmap-limit":  {2},
	"pfor-each":   {1},
	"race":        {1},
}

// waits are the calls that wait for another task.
var waits = map[string]bool{
	"channel-send!": true, "channel-recv": true, "channel-for-each": true, "select": true,
	"task-wait": true, "task-wait-all": true, "with-lock": true, "once-do": true,
	"task-group": true, "pmap": true, "pmap-limit": true, "pfor-each": true, "race": true,
}

// effects are calls whose effect happens each time a retried procedure runs.
var effects = map[string]bool{"display": true, "write": true, "newline": true}

// retried says which argument of a procedure is one it may call more than once.
var retried = map[string]int{"swap!": 2, "hash-update!": 3}

func (w walker) concurrency(nodes []*scheme.Syntax) {
	refs := map[Range]*Reference{}
	for i := range w.d.References {
		refs[w.d.References[i].Range] = &w.d.References[i]
	}
	var visit func(n *scheme.Syntax)
	visit = func(n *scheme.Syntax) {
		if n == nil || n.Prefix == "quote" || n.Prefix == "vector" {
			return
		}
		if h := head(n); h != "" && len(n.Children) > 1 {
			for _, i := range taskThunks[h] {
				if i >= len(n.Children) {
					continue
				}
				arg := n.Children[i]
				if h == "race" {
					// (race (list (lambda () ...) ...))
					if head(arg) == "list" {
						for _, l := range arg.Children[1:] {
							w.sharedWrites(l, refs)
						}
					}
					continue
				}
				w.sharedWrites(arg, refs)
			}
			if h == "with-lock" && len(n.Children) > 2 {
				w.waitsInside(n.Children[2])
			}
			if i, ok := retried[h]; ok && i < len(n.Children) {
				w.effectsInside(n.Children[i], h)
			}
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	for _, n := range nodes {
		visit(n)
	}
}

// sharedWrites warns about each set! in thunk of a variable that is not the
// thunk's own, which other tasks can see.
func (w walker) sharedWrites(thunk *scheme.Syntax, refs map[Range]*Reference) {
	if head(thunk) != "lambda" {
		return
	}
	inside := w.d.rng(thunk.Start, thunk.End)
	var visit func(n *scheme.Syntax)
	visit = func(n *scheme.Syntax) {
		if n == nil || n.Prefix == "quote" || n.Prefix == "vector" {
			return
		}
		if head(n) == "set!" && len(n.Children) >= 2 && n.Children[1].Atom != "" {
			target := n.Children[1]
			r := refs[w.d.rng(target.Start, target.End)]
			shared := r == nil || r.Definition == nil || before(r.Definition.Scope.Start, inside.Start)
			if shared {
				msg := fmt.Sprintf("%s is set! from a task, and other tasks can see it: at the same time, one task's write can be lost. Use an atom (make-atom and swap!) or with-lock.", target.Atom)
				if len(n.Children) > 2 && mentions(n.Children[2], target.Atom) {
					msg = fmt.Sprintf("%s is read and then set! from a task: if another task writes it in between, that write is lost. Use an atom (swap!) or with-lock.", target.Atom)
				}
				w.warn(n, msg)
			}
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(thunk)
}

// waitsInside warns about each call in a lock's procedure that waits for
// another task, which may be waiting for the lock.
func (w walker) waitsInside(thunk *scheme.Syntax) {
	var visit func(n *scheme.Syntax)
	visit = func(n *scheme.Syntax) {
		if n == nil || n.Prefix == "quote" || n.Prefix == "vector" {
			return
		}
		if h := head(n); waits[h] && n != thunk {
			w.warn(n, fmt.Sprintf("%s waits for another task while this task holds a lock; if that task needs the lock, neither can go on. Do the waiting outside with-lock.", h))
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(thunk)
}

// effectsInside warns about effects in a procedure that swap! or hash-update!
// may call again.
func (w walker) effectsInside(proc *scheme.Syntax, by string) {
	if head(proc) != "lambda" {
		return
	}
	var visit func(n *scheme.Syntax)
	visit = func(n *scheme.Syntax) {
		if n == nil || n.Prefix == "quote" || n.Prefix == "vector" {
			return
		}
		h := head(n)
		if h != "" && n != proc && (effects[h] || (strings.HasSuffix(h, "!") && h != "set!")) {
			w.warn(n, fmt.Sprintf("%s may call this procedure more than once if another task changes the value first, so %s would happen each time. Keep it to computing the new value.", by, h))
		}
		for _, c := range n.Children {
			visit(c)
		}
	}
	visit(proc)
}

func (w walker) warn(n *scheme.Syntax, msg string) {
	w.d.Diagnostics = append(w.d.Diagnostics, Diagnostic{Range: w.d.rng(n.Start, n.End), Severity: warning, Message: msg, Source: "brote"})
}

// mentions reports whether name occurs in n.
func mentions(n *scheme.Syntax, name string) bool {
	if n == nil {
		return false
	}
	if n.Atom == name {
		return true
	}
	for _, c := range n.Children {
		if mentions(c, name) {
			return true
		}
	}
	return false
}

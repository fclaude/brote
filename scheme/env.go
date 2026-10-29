package scheme

import (
	"sync"
)

// Env is a lexical environment: a frame of bindings and a parent. It is safe
// for concurrent use: tasks share the frames their closures captured.
//
// The root frame (the global environment) is read by every task on every
// call, so it is a sync.Map and a read takes no lock. Every other frame has
// its own lock, held for one lookup or one update and never across a call into
// Scheme.
//
// A local frame is two parallel slices, names and values, and not a map:
// frames are small, a call makes one, and a map costs several allocations
// where this costs two. The names of a call's frame are the closure's own
// parameter list, shared by every call and copied only if the body defines
// something new.
type Env struct {
	parent *Env
	global *sync.Map // the root frame's bindings; nil on every other frame

	mu    sync.RWMutex
	names []string
	vals  []Value
	own   bool           // names belongs to this frame; otherwise it is a closure's, shared
	index map[string]int // name -> position, once the frame is too big to scan
}

// indexAt is how many bindings a frame may have before it keeps an index.
const indexAt = 12

// NewEnv creates an empty frame under parent (nil for the global frame).
func NewEnv(parent *Env) *Env {
	if parent == nil {
		return &Env{global: new(sync.Map)}
	}
	return &Env{parent: parent, own: true}
}

// frameOf is a frame under parent that binds names to vals. It takes the
// slices over: vals must not be used by the caller again, and names is not
// modified (the frame copies it before it adds a binding). Nothing can see the
// frame yet, so it is built without the lock.
func frameOf(parent *Env, names []string, vals []Value) *Env {
	e := &Env{parent: parent, names: names, vals: vals}
	if len(names) > indexAt {
		e.reindex()
	}
	return e
}

// find is the position of name in this frame, or -1. The caller holds the lock.
// It looks from the end, so when a name appears twice the later binding wins.
func (e *Env) find(name string) int {
	if e.index != nil {
		if i, ok := e.index[name]; ok {
			return i
		}
		return -1
	}
	for i := len(e.names) - 1; i >= 0; i-- {
		if e.names[i] == name {
			return i
		}
	}
	return -1
}

func (e *Env) reindex() {
	e.index = make(map[string]int, len(e.names)*2)
	for i, n := range e.names {
		e.index[n] = i
	}
}

// Lookup finds name in this frame or any ancestor.
func (e *Env) Lookup(name string) (Value, bool) {
	for env := e; env != nil; env = env.parent {
		if env.global != nil {
			if v, ok := env.global.Load(name); ok {
				return v.(Value), true
			}
			continue
		}
		env.mu.RLock()
		if i := env.find(name); i >= 0 {
			v := env.vals[i]
			env.mu.RUnlock()
			return v, true
		}
		env.mu.RUnlock()
	}
	return Value{}, false
}

// Define binds name in this frame, shadowing any ancestor binding.
func (e *Env) Define(name string, v Value) {
	if e.global != nil {
		e.global.Store(name, v)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if i := e.find(name); i >= 0 {
		e.vals[i] = v
		return
	}
	if !e.own {
		e.names = append(make([]string, 0, len(e.names)+1), e.names...)
		e.own = true
	}
	e.names = append(e.names, name)
	e.vals = append(e.vals, v)
	switch {
	case e.index != nil:
		e.index[name] = len(e.names) - 1
	case len(e.names) > indexAt:
		e.reindex()
	}
}

// Set assigns an existing binding in the nearest frame that has it.
func (e *Env) Set(name string, v Value) bool {
	for env := e; env != nil; env = env.parent {
		if env.global != nil {
			if _, ok := env.global.Load(name); ok {
				env.global.Store(name, v)
				return true
			}
			continue
		}
		env.mu.Lock()
		if i := env.find(name); i >= 0 {
			env.vals[i] = v
			env.mu.Unlock()
			return true
		}
		env.mu.Unlock()
	}
	return false
}

// Snapshot copies this frame's bindings (not its ancestors').
func (e *Env) Snapshot() map[string]Value {
	if e.global != nil {
		m := make(map[string]Value)
		e.global.Range(func(k, v any) bool {
			m[k.(string)] = v.(Value)
			return true
		})
		return m
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	m := make(map[string]Value, len(e.names))
	for i, n := range e.names {
		m[n] = e.vals[i]
	}
	return m
}

// Restore makes this frame's bindings a copy of a snapshot: names bound
// since are unbound again. Closures over the frame see the restored values.
func (e *Env) Restore(m map[string]Value) {
	if e.global != nil {
		e.global.Clear()
		for k, v := range m {
			e.global.Store(k, v)
		}
		return
	}
	names := make([]string, 0, len(m))
	vals := make([]Value, 0, len(m))
	for k, v := range m {
		names = append(names, k)
		vals = append(vals, v)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.names, e.vals, e.own, e.index = names, vals, true, nil
	if len(names) > indexAt {
		e.reindex()
	}
}

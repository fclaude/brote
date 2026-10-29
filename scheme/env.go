package scheme

import (
	"maps"
	"sync"
)

// Env is a lexical environment: a frame of bindings and a parent. It is safe
// for concurrent use: tasks share the frames their closures captured.
//
// The root frame (the global environment) is read by every task on every
// call, so it is a sync.Map and a read takes no lock. Every other frame has
// its own lock; it is held for one map operation and never across a call
// into Scheme.
type Env struct {
	parent *Env
	global *sync.Map // the root frame's bindings; nil on every other frame

	mu   sync.RWMutex
	vars map[string]Value
}

// NewEnv creates an empty frame under parent (nil for the global frame).
func NewEnv(parent *Env) *Env {
	if parent == nil {
		return &Env{global: new(sync.Map)}
	}
	return &Env{parent: parent, vars: make(map[string]Value)}
}

// newFrame is a frame under parent with room for n bindings.
func newFrame(parent *Env, n int) *Env {
	return &Env{parent: parent, vars: make(map[string]Value, n)}
}

// init binds name in a frame that no one else can see yet (a call's frame
// before its body runs), so it takes no lock. Use Define once the frame can
// have been captured.
func (e *Env) init(name string, v Value) {
	e.vars[name] = v
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
		v, ok := env.vars[name]
		env.mu.RUnlock()
		if ok {
			return v, true
		}
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
	e.vars[name] = v
	e.mu.Unlock()
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
		if _, ok := env.vars[name]; ok {
			env.vars[name] = v
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
	return maps.Clone(e.vars)
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
	vars := maps.Clone(m)
	if vars == nil {
		vars = make(map[string]Value)
	}
	e.mu.Lock()
	e.vars = vars
	e.mu.Unlock()
}

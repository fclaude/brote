package scheme

import "sync"

// Hash is the storage behind a hash table. Entries keep the order they were
// first added in, so hash-keys, hash-values and hash->alist always list them
// the same way. Removing an entry leaves a hole that is squeezed out once
// holes outnumber live entries.
//
// Tasks share tables, so every method takes the table's lock for the length
// of one operation. None calls back into Scheme while holding it: each, which
// callers use to walk a table and so might call anything, walks a copy.
type Hash struct {
	mu      sync.RWMutex
	entries []hashEntry
	index   map[hashKey]int
	live    int
	// clock numbers writes, so an entry's ver says which write made it: two
	// reads that saw the same ver saw no write between them, even if the key
	// was removed and added back (update, below).
	clock uint64
}

type hashEntry struct {
	key, val Value
	ver      uint64
	dead     bool
}

// hashKey is what two keys must share to be the same key: equal? on strings,
// symbols, integers, characters and booleans, where a string and a symbol of the same
// letters are different keys.
type hashKey struct {
	kind Kind
	s    string
	i    int64
}

// NewHash makes an empty hash table value.
func NewHash() Value {
	return Value{Kind: KHash, Hash: &Hash{index: map[hashKey]int{}}}
}

// keyOf is the identity of a value used as a key, or false if its kind cannot
// be a key.
func keyOf(v Value) (hashKey, bool) {
	switch v.Kind {
	case KString, KSymbol:
		return hashKey{kind: v.Kind, s: v.S}, true
	case KInt, KBool, KChar:
		return hashKey{kind: v.Kind, i: v.I}, true
	}
	return hashKey{}, false
}

func (h *Hash) get(k hashKey) (Value, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	i, ok := h.index[k]
	if !ok {
		return Value{}, false
	}
	return h.entries[i].val, true
}

// count is the number of entries.
func (h *Hash) count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.live
}

// snapshot reads the entry under k together with the version of the write
// that made it; ok is false, and ver 0, when there is none.
func (h *Hash) snapshot(k hashKey) (val Value, ver uint64, ok bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	i, ok := h.index[k]
	if !ok {
		return Value{}, 0, false
	}
	e := h.entries[i]
	return e.val, e.ver, true
}

// setIfUnchanged stores val under k only if the entry is still the one
// snapshot returned: the same ver, or still absent if it was absent. It
// reports whether it stored.
func (h *Hash) setIfUnchanged(k hashKey, key, val Value, ver uint64, present bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	i, ok := h.index[k]
	if ok != present || (ok && h.entries[i].ver != ver) {
		return false
	}
	h.setLocked(k, key, val)
	return true
}

// set stores val under key. A key already there keeps its place and takes the
// provenance of both spellings, so a tainted key cannot be hidden by writing
// it over a clean one.
func (h *Hash) set(k hashKey, key, val Value) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setLocked(k, key, val)
}

func (h *Hash) setLocked(k hashKey, key, val Value) {
	h.clock++
	if i, ok := h.index[k]; ok {
		e := &h.entries[i]
		e.key.Taint = Union(e.key.Taint, key.Taint)
		e.val, e.ver = val, h.clock
		return
	}
	h.index[k] = len(h.entries)
	h.entries = append(h.entries, hashEntry{key: key, val: val, ver: h.clock})
	h.live++
}

func (h *Hash) remove(k hashKey) {
	h.mu.Lock()
	defer h.mu.Unlock()
	i, ok := h.index[k]
	if !ok {
		return
	}
	h.entries[i] = hashEntry{dead: true}
	delete(h.index, k)
	h.live--
	if len(h.entries) > 32 && h.live*2 < len(h.entries) {
		h.compact()
	}
}

func (h *Hash) compact() {
	kept := make([]hashEntry, 0, h.live)
	for _, e := range h.entries {
		if e.dead {
			continue
		}
		k, _ := keyOf(e.key)
		h.index[k] = len(kept)
		kept = append(kept, e)
	}
	h.entries = kept
}

// each calls fn for every live entry in order. It walks a copy taken at one
// instant, so fn may do anything, including change the table.
func (h *Hash) each(fn func(key, val Value)) {
	h.mu.RLock()
	live := make([]hashEntry, 0, h.live)
	for _, e := range h.entries {
		if !e.dead {
			live = append(live, e)
		}
	}
	h.mu.RUnlock()
	for _, e := range live {
		fn(e.key, e.val)
	}
}

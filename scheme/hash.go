package scheme

// Hash is the storage behind a hash table. Entries keep the order they were
// first added in, so hash-keys, hash-values and hash->alist always list them
// the same way. Removing an entry leaves a hole that is squeezed out once
// holes outnumber live entries.
type Hash struct {
	entries []hashEntry
	index   map[hashKey]int
	live    int
}

type hashEntry struct {
	key, val Value
	dead     bool
}

// hashKey is what two keys must share to be the same key: equal? on strings,
// symbols, integers and booleans, where a string and a symbol of the same
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
	case KInt, KBool:
		return hashKey{kind: v.Kind, i: v.I}, true
	}
	return hashKey{}, false
}

func (h *Hash) get(k hashKey) (Value, bool) {
	i, ok := h.index[k]
	if !ok {
		return Value{}, false
	}
	return h.entries[i].val, true
}

// set stores val under key. A key already there keeps its place and takes the
// provenance of both spellings, so a tainted key cannot be hidden by writing
// it over a clean one.
func (h *Hash) set(k hashKey, key, val Value) {
	if i, ok := h.index[k]; ok {
		e := &h.entries[i]
		e.key.Taint = Union(e.key.Taint, key.Taint)
		e.val = val
		return
	}
	h.index[k] = len(h.entries)
	h.entries = append(h.entries, hashEntry{key: key, val: val})
	h.live++
}

func (h *Hash) remove(k hashKey) {
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

// each calls fn for every live entry in order.
func (h *Hash) each(fn func(key, val Value)) {
	for _, e := range h.entries {
		if !e.dead {
			fn(e.key, e.val)
		}
	}
}

package std

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fclaude/brote/scheme"
)

// watchObj is a polling watch over one granted directory: a goroutine stats
// the entries every interval and queues the names that changed, and
// fs-watch-changes drains the queue. It polls because the module depends on
// nothing outside the standard library.
type watchObj struct {
	f        *fsys
	root     *fsRoot
	name     string // the directory's name inside the root
	dir      string // the script's path, for messages
	interval time.Duration

	mu       sync.Mutex
	snapshot map[string]fileState
	pending  map[string]bool
	stopped  bool
	stopCh   chan struct{}
	doneCh   chan struct{}
}

type fileState struct {
	size  int64
	mtime int64 // UnixNano
}

func (w *watchObj) Kind() string   { return "watch" }
func (w *watchObj) String() string { return "#<watch " + w.dir + ">" }

// Close stops the watch; it is what Runtime.Close calls.
func (w *watchObj) Close() error { return w.stop() }

func (w *watchObj) stop() error {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return nil
	}
	w.stopped = true
	close(w.stopCh)
	w.mu.Unlock()
	<-w.doneCh
	return nil
}

// poll stats the directory once and queues what changed since the last poll.
func (w *watchObj) poll() error {
	root, err := w.root.open()
	if err != nil {
		return err
	}
	d, err := root.Open(w.name)
	if err != nil {
		return w.f.fail(w.dir, err)
	}
	defer d.Close()
	ents, err := d.ReadDir(maxListEntries + 1)
	if err != nil && err != io.EOF {
		return w.f.fail(w.dir, err)
	}
	cur := make(map[string]fileState, len(ents))
	for _, e := range ents {
		st, err := root.Stat(filepath.Join(w.name, e.Name()))
		if err != nil {
			continue // removed between the listing and the stat
		}
		cur[e.Name()] = fileState{size: st.Size(), mtime: st.ModTime().UnixNano()}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return nil
	}
	for name, st := range cur {
		if old, ok := w.snapshot[name]; !ok || old != st {
			w.pending[name] = true
		}
	}
	for name := range w.snapshot {
		if _, ok := cur[name]; !ok {
			w.pending[name] = true
		}
	}
	w.snapshot = cur
	return nil
}

func (w *watchObj) run() {
	defer close(w.doneCh)
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-t.C:
			_ = w.poll()
		}
	}
}

// installWatch registers fs-watch, fs-watch-changes, fs-watch-stop! and
// watch? as part of the fs module: a watch reads a granted directory, so a
// policy without the fs grant leaves them unbound.
func installWatch(s *installer) error {
	f := s.fs
	any1 := []scheme.Param{{Name: "x", Type: "any"}}
	watchP := scheme.Param{Name: "watch", Type: "watch"}

	s.add(&scheme.Prim{
		Name: "fs-watch",
		Meta: scheme.Meta{
			Doc: "Watch a granted directory for changes, polled every interval seconds (default 1): returns a watch. fs-watch-changes drains the names that appeared, changed (size or modification time) or were removed, and fs-watch-stop! ends the watch. The watch runs until stopped or the host closes the runtime, not only until the evaluation that started it ends.",
			Params: []scheme.Param{
				{Name: "path", Type: "string", Role: "source"},
				{Name: "interval", Type: "number", Doc: "seconds between polls, 0.01 to 3600", Optional: true},
			},
			Host: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := c.String(0)
			if err != nil {
				return scheme.Value{}, err
			}
			interval := time.Second
			if len(c.Args) > 1 {
				n, err := c.Number(1)
				if err != nil {
					return scheme.Value{}, err
				}
				secs := n.AsFloat()
				if secs < 0.01 || secs > 3600 || secs != secs {
					return scheme.Value{}, fmt.Errorf("argument 2: expected 0.01 to 3600 seconds, got %s", scheme.Write(n))
				}
				interval = time.Duration(secs * float64(time.Second))
			}
			r, name, err := f.resolve(p, false)
			if err != nil {
				return scheme.Value{}, err
			}
			root, err := r.open()
			if err != nil {
				return scheme.Value{}, err
			}
			st, err := root.Stat(name)
			if err != nil {
				return scheme.Value{}, f.fail(p, err)
			}
			if !st.IsDir() {
				return scheme.Value{}, fmt.Errorf("%s: not a directory", p)
			}
			w := &watchObj{
				f: f, root: r, name: name, dir: p, interval: interval,
				snapshot: map[string]fileState{}, pending: map[string]bool{},
				stopCh: make(chan struct{}), doneCh: make(chan struct{}),
			}
			if err := w.poll(); err != nil {
				return scheme.Value{}, err
			}
			w.mu.Lock()
			w.pending = map[string]bool{} // the first poll is the baseline
			w.mu.Unlock()
			go w.run()
			s.closers = append(s.closers, w)
			s.report(FS, "watch", p, nil)
			return scheme.ObjectValue(w), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "fs-watch-changes",
		Meta: scheme.Meta{
			Doc: "The names in a watched directory that appeared, changed or were removed since the last call, sorted, as a list of strings; drains the queue.",
			Params: []scheme.Param{watchP},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			w, ok := scheme.ObjectOf(c.Args[0]).(*watchObj)
			if !ok {
				return scheme.Value{}, fmt.Errorf("argument 1: expected a watch, got %s", scheme.Write(c.Args[0]))
			}
			w.mu.Lock()
			names := make([]string, 0, len(w.pending))
			for n := range w.pending {
				names = append(names, n)
			}
			w.pending = map[string]bool{}
			w.mu.Unlock()
			slices.Sort(names)
			out := make([]scheme.Value, len(names))
			for i, n := range names {
				out[i] = scheme.Str(n)
			}
			return scheme.List(out...), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "fs-watch-stop!",
		Meta: scheme.Meta{
			Doc:    "Stop a watch: its goroutine ends. Stopping a stopped watch does nothing.",
			Params: []scheme.Param{watchP},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			w, ok := scheme.ObjectOf(c.Args[0]).(*watchObj)
			if !ok {
				return scheme.Value{}, fmt.Errorf("argument 1: expected a watch, got %s", scheme.Write(c.Args[0]))
			}
			err := w.stop()
			s.report(FS, "unwatch", w.dir, err)
			return scheme.Void, err
		},
	})
	s.add(&scheme.Prim{
		Name: "watch?",
		Meta: scheme.Meta{
			Doc:    "True for watches.",
			Params: any1,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			_, ok := scheme.ObjectOf(c.Args[0]).(*watchObj)
			return scheme.Bool(ok), nil
		},
	})
	return nil
}

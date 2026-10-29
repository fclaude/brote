package std

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fclaude/brote/scheme"
)

func init() { moduleInstallers[Exec] = installExec }

// capWriter keeps what a child writes, up to a limit; past it, it stops the
// child and remembers why.
type capWriter struct {
	buf    bytes.Buffer
	max    int64
	over   bool
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - int64(w.buf.Len()); int64(len(p)) > room {
		w.buf.Write(p[:max(room, 0)])
		w.over = true
		w.cancel()
		return len(p), nil
	}
	return w.buf.Write(p)
}

// commands maps each command a script may start to the file that runs.
type commands struct {
	any   bool
	paths map[string]string // name or absolute path -> file to run
	err   map[string]error  // why an allowed command has no file
}

func newCommands(scope ExecScope) *commands {
	c := &commands{any: scope.Any, paths: map[string]string{}, err: map[string]error{}}
	for _, a := range scope.Allow {
		if filepath.IsAbs(a) {
			c.paths[a] = a
			continue
		}
		p, err := exec.LookPath(a)
		if err != nil {
			c.err[a] = err
			continue
		}
		if abs, e := filepath.Abs(p); e == nil {
			p = abs
		}
		c.paths[a] = p
		// Naming the file that the name resolved to is the same command.
		if _, taken := c.paths[p]; !taken {
			c.paths[p] = p
		}
	}
	return c
}

// resolve returns the file to run for the script's argv[0].
func (c *commands) resolve(name string) (string, error) {
	if c.any {
		return name, nil
	}
	if p, ok := c.paths[name]; ok {
		return p, nil
	}
	if err, ok := c.err[name]; ok {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return "", fmt.Errorf("%s is not a command this host lets scripts run", name)
}

func installExec(s *installer) error {
	scope := s.pol.Exec
	cmds := newCommands(scope)
	base := s.base
	if base == "" {
		base, _ = os.Getwd()
	}
	childEnv := func() []string {
		if scope.Env.Any {
			return os.Environ()
		}
		var out []string
		for _, name := range scope.Env.Allow {
			if v, ok := os.LookupEnv(name); ok {
				out = append(out, name+"="+v)
			}
		}
		return out
	}

	s.add(&scheme.Prim{
		Name: "proc-run",
		Meta: scheme.Meta{
			Doc: "Run a program and wait for it. The argument is a list of strings, the command first; it is started directly, never through a shell, so no argument is interpreted. " +
				"Returns an association list with \"exit-code\" (integer; -1 if a signal ended it), \"stdout\" and \"stderr\" (strings). A nonzero exit is a result, not an error; a command the host did not grant, one that cannot start, a timeout, or output past the host's limit is an error. " +
				"Options is an alist: \"stdin\" (a string to feed it) and \"timeout\" (seconds, at most the host's limit). The program sees only the environment variables the host granted.",
			Params: []scheme.Param{
				{Name: "argv", Type: "list", Doc: "command and arguments", Role: "argv"},
				{Name: "options", Type: "alist", Doc: `"stdin", "timeout"`, Optional: true},
			},
			Host:         true,
			Irreversible: true,
			TaintSource:  scheme.TagShell,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			items, err := c.List(0)
			if err != nil {
				return scheme.Value{}, err
			}
			argv := make([]string, len(items))
			for i, it := range items {
				if it.Kind != scheme.KString {
					return scheme.Value{}, fmt.Errorf("argument 1: element %d: expected a string, got %s", i+1, scheme.Write(it))
				}
				if strings.ContainsRune(it.S, 0) {
					return scheme.Value{}, fmt.Errorf("argument 1: element %d contains a NUL byte", i+1)
				}
				argv[i] = it.S
			}
			if len(argv) == 0 || argv[0] == "" {
				return scheme.Value{}, errors.New("argument 1: expected a command and its arguments")
			}
			var stdin *string
			timeout := scope.Timeout
			if len(c.Args) > 1 {
				opts, err := c.List(1)
				if err != nil {
					return scheme.Value{}, err
				}
				for _, o := range opts {
					if o.Kind != scheme.KPair {
						return scheme.Value{}, fmt.Errorf("options: expected an association list, got %s", scheme.Write(o))
					}
					key, _ := atom(o.P.Car)
					switch key {
					case "stdin":
						if o.P.Cdr.Kind != scheme.KString {
							return scheme.Value{}, errors.New(`options: "stdin" takes a string`)
						}
						stdin = &o.P.Cdr.S
					case "timeout":
						if !o.P.Cdr.IsNumber() || o.P.Cdr.AsFloat() <= 0 {
							return scheme.Value{}, errors.New(`options: "timeout" takes a positive number of seconds`)
						}
						d := time.Duration(o.P.Cdr.AsFloat() * float64(time.Second))
						if d > scope.Timeout {
							return scheme.Value{}, fmt.Errorf(`options: "timeout" %s is more than the %s this host allows`, scheme.Write(o.P.Cdr), scope.Timeout)
						}
						timeout = d
					default:
						return scheme.Value{}, fmt.Errorf("options: unknown option %s (known: stdin, timeout)", scheme.Write(o.P.Car))
					}
				}
			}
			file, err := cmds.resolve(argv[0])
			if err != nil {
				s.report(Exec, "run", strings.Join(argv, " "), err)
				return scheme.Value{}, err
			}

			ctx, cancel := context.WithTimeout(c.Ctx, timeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, file, argv[1:]...)
			cmd.Args[0] = argv[0]
			cmd.Dir = base
			cmd.Env = childEnv()
			cmd.WaitDelay = 2 * time.Second
			ownGroup(cmd)
			if stdin != nil {
				cmd.Stdin = strings.NewReader(*stdin)
			}
			out, errw := &capWriter{max: scope.MaxOutput, cancel: cancel}, &capWriter{max: scope.MaxOutput, cancel: cancel}
			cmd.Stdout, cmd.Stderr = out, errw

			runErr := cmd.Run()
			var exit *exec.ExitError
			switch {
			case out.over || errw.over:
				err = fmt.Errorf("%s: output is more than the %d bytes this host allows", argv[0], scope.MaxOutput)
			case c.Ctx.Err() != nil:
				err = c.Ctx.Err()
			case ctx.Err() != nil:
				err = fmt.Errorf("%s: still running after %s, so it was stopped", argv[0], timeout)
			case runErr != nil && !errors.As(runErr, &exit):
				err = fmt.Errorf("%s: %w", argv[0], runErr)
			}
			s.report(Exec, "run", strings.Join(argv, " "), err)
			if err != nil {
				return scheme.Value{}, err
			}
			code := cmd.ProcessState.ExitCode()
			text := func(b *bytes.Buffer) scheme.Value {
				return scheme.Str(strings.ToValidUTF8(b.String(), "�"))
			}
			return scheme.List(
				scheme.Cons(scheme.Str("exit-code"), scheme.Int(int64(code))),
				scheme.Cons(scheme.Str("stdout"), text(&out.buf)),
				scheme.Cons(scheme.Str("stderr"), text(&errw.buf)),
			), nil
		},
	})
	return nil
}

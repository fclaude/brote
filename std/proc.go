package std

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/fclaude/brote/scheme"
)

// procObj is a program a script started with proc-start and talks to while it
// runs: it reads the program's output a line at a time, writes its input, and
// waits for it. The program's output and input are operating-system pipes,
// so a process's output can be another's input with no copying through the
// interpreter.
type procObj struct {
	name   string // argv[0], for messages
	cmd    *exec.Cmd
	cancel context.CancelFunc // ends the process: its timeout or a kill
	limit  time.Duration
	max    int64
	set    *closerSet

	outMu sync.Mutex // guards the output fields; a read holds it while it waits
	out   *os.File   // the read end of the output pipe; nil once given away or closed
	br    *bufio.Reader
	piped bool // the output goes to another process
	eof   bool

	inMu   sync.Mutex // guards in; separate, so one task writes while another reads
	in     *os.File   // the write end of the input pipe, nil once closed
	inPipe bool       // started with ("stdin" . pipe)

	errw     *capWriter
	done     chan struct{} // closed when the program has exited
	waitErr  error
	timedOut bool

	killMu sync.Mutex
	killed bool

	waitMu   sync.Mutex // one wait at a time; guards the result
	finished bool
	result   scheme.Value
	resErr   error
}

func (p *procObj) Kind() string   { return "process" }
func (p *procObj) String() string { return "#<process " + p.name + ">" }

// kill stops the program (on Unix the processes it started with it too).
func (p *procObj) kill() {
	p.killMu.Lock()
	p.killed = true
	p.killMu.Unlock()
	p.cancel()
}

func (p *procObj) wasKilled() bool {
	p.killMu.Lock()
	defer p.killMu.Unlock()
	return p.killed
}

// Close is what Runtime.Close calls: the program is stopped and its pipes
// closed.
func (p *procObj) Close() error {
	p.kill()
	<-p.done
	p.closePipes()
	return nil
}

func (p *procObj) closePipes() {
	p.outMu.Lock()
	if p.out != nil {
		_ = p.out.Close()
		p.out = nil
	}
	p.eof = true
	p.outMu.Unlock()
	p.inMu.Lock()
	if p.in != nil {
		_ = p.in.Close()
		p.in = nil
	}
	p.inMu.Unlock()
}

// readLine is the next line of output without its ending, false at the end.
// A line longer than max is an error rather than one held whole in memory.
func (p *procObj) readLine() (string, bool, error) {
	if p.br == nil {
		p.br = bufio.NewReader(p.out)
	}
	var line []byte
	for {
		chunk, err := p.br.ReadSlice('\n')
		if int64(len(line)+len(chunk)) > p.max {
			p.kill()
			return "", false, fmt.Errorf("%s: a line of output is more than the %d bytes this host allows", p.name, p.max)
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			s := strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")
			return strings.ToValidUTF8(s, "�"), true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed):
			p.eof = true
			if len(line) == 0 {
				return "", false, nil
			}
			return strings.ToValidUTF8(strings.TrimSuffix(string(line), "\r"), "�"), true, nil
		default:
			return "", false, fmt.Errorf("%s: %w", p.name, err)
		}
	}
}

// wait drains the output nobody read, waits for the program to exit and
// makes the result proc-wait gives, once.
func (p *procObj) wait(ctx context.Context) (scheme.Value, error) {
	p.waitMu.Lock()
	defer p.waitMu.Unlock()
	if p.finished {
		return p.result, p.resErr
	}
	stop := context.AfterFunc(ctx, p.kill)
	defer stop()

	rest := &capWriter{max: p.max, cancel: p.kill}
	p.outMu.Lock()
	if !p.piped && !p.eof && p.out != nil {
		var src io.Reader = p.out
		if p.br != nil {
			src = p.br
		}
		_, _ = io.Copy(rest, src)
		p.eof = true
	}
	p.outMu.Unlock()
	<-p.done
	if err := ctx.Err(); err != nil {
		return scheme.Value{}, err // a later wait gives the result
	}
	p.closePipes()

	var exit *exec.ExitError
	var err error
	switch {
	case rest.over || p.errw.over:
		err = fmt.Errorf("%s: output is more than the %d bytes this host allows", p.name, p.max)
	case p.timedOut:
		err = fmt.Errorf("%s: still running after %s, so it was stopped", p.name, p.limit)
	case p.waitErr != nil && !errors.As(p.waitErr, &exit) && !p.wasKilled():
		err = fmt.Errorf("%s: %w", p.name, p.waitErr)
	}
	p.finished, p.resErr = true, err
	if err == nil {
		text := func(w *capWriter) scheme.Value {
			return scheme.Str(strings.ToValidUTF8(w.buf.String(), "�"))
		}
		p.result = scheme.List(
			scheme.Cons(scheme.Str("exit-code"), scheme.Int(int64(p.cmd.ProcessState.ExitCode()))),
			scheme.Cons(scheme.Str("stdout"), text(rest)),
			scheme.Cons(scheme.Str("stderr"), text(p.errw)),
		)
	}
	p.set.remove(p)
	return p.result, p.resErr
}

// procArg is argument i as a process.
func procArg(c *scheme.Call, i int) (*procObj, error) {
	if p, ok := scheme.ObjectOf(c.Args[i]).(*procObj); ok {
		return p, nil
	}
	return nil, fmt.Errorf("argument %d: expected a process, got %s", i+1, scheme.Write(c.Args[i]))
}

// installProc registers proc-start and the procedures over the process it
// returns, as part of the exec module: they start programs the same grant
// lets proc-run start, under the same limits.
func installProc(s *installer, scope ExecScope, command func(context.Context, []string) (*exec.Cmd, error)) error {
	procP := scheme.Param{Name: "process", Type: "process"}

	s.add(&scheme.Prim{
		Name: "proc-start",
		Meta: scheme.Meta{
			Doc: "Start a program and return at once with a process, which proc-read-line reads a line at a time while it runs, proc-write writes to, and proc-wait waits for. The argument is a list of strings, started directly as proc-run starts it, never through a shell. " +
				"Options is an alist: \"stdin\" is a string to feed it, the symbol pipe for input written with proc-write, or another process, whose output becomes this one's input (a pipeline); without it the input is empty. \"timeout\" is seconds, at most the host's limit. " +
				"The program is stopped when its timeout passes, by proc-kill!, when a read, write or wait on it is cancelled, or when the host closes the runtime.",
			Params: []scheme.Param{
				{Name: "argv", Type: "list", Doc: "command and arguments", Role: "argv"},
				{Name: "options", Type: "alist", Doc: `"stdin", "timeout"`, Optional: true},
			},
			Host:         true,
			Irreversible: true,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			argv, err := argvArg(c)
			if err != nil {
				return scheme.Value{}, err
			}
			timeout := scope.Timeout
			var stdinText *string
			var from *procObj
			pipeIn := false
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
					v := o.P.Cdr
					switch key {
					case "stdin":
						q, isProc := scheme.ObjectOf(v).(*procObj)
						switch {
						case v.Kind == scheme.KString:
							stdinText = &v.S
						case v.Kind == scheme.KSymbol && v.S == "pipe":
							pipeIn = true
						case isProc:
							from = q
						default:
							return scheme.Value{}, fmt.Errorf(`options: "stdin" takes a string, the symbol pipe or a process, got %s`, scheme.Write(v))
						}
					case "timeout":
						if timeout, err = timeoutOption(v, scope.Timeout); err != nil {
							return scheme.Value{}, err
						}
					default:
						return scheme.Value{}, fmt.Errorf("options: unknown option %s (known: stdin, timeout)", scheme.Write(o.P.Car))
					}
				}
			}

			// The process outlives this call, so it does not end with the
			// call's context; it keeps the context's values.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Ctx), timeout)
			cmd, err := command(ctx, argv)
			if err != nil {
				cancel()
				s.report(Exec, "start", strings.Join(argv, " "), err)
				return scheme.Value{}, err
			}
			p := &procObj{name: argv[0], cmd: cmd, cancel: cancel, limit: timeout, max: scope.MaxOutput, set: s.closers, done: make(chan struct{}), inPipe: pipeIn}
			p.errw = &capWriter{max: scope.MaxOutput, cancel: p.kill}
			cmd.Stderr = p.errw

			var closeAfter []*os.File // the child's ends, which the parent must not keep
			fail := func(err error) (scheme.Value, error) {
				cancel()
				for _, f := range closeAfter {
					_ = f.Close()
				}
				if p.out != nil {
					_ = p.out.Close()
				}
				if p.in != nil {
					_ = p.in.Close()
				}
				s.report(Exec, "start", strings.Join(argv, " "), err)
				return scheme.Value{}, err
			}
			outR, outW, err := os.Pipe()
			if err != nil {
				return fail(err)
			}
			p.out, cmd.Stdout = outR, outW
			closeAfter = append(closeAfter, outW)
			switch {
			case stdinText != nil:
				cmd.Stdin = strings.NewReader(*stdinText)
			case pipeIn:
				inR, inW, err := os.Pipe()
				if err != nil {
					return fail(err)
				}
				p.in, cmd.Stdin = inW, inR
				closeAfter = append(closeAfter, inR)
			case from != nil:
				from.outMu.Lock()
				defer from.outMu.Unlock()
				switch {
				case from.piped:
					return fail(fmt.Errorf(`options: "stdin": the output of %s goes to another process already`, from.name))
				case from.br != nil || from.eof || from.out == nil:
					return fail(fmt.Errorf(`options: "stdin": the output of %s has already been read`, from.name))
				}
				cmd.Stdin = from.out
			}
			if err := cmd.Start(); err != nil {
				return fail(fmt.Errorf("%s: %w", argv[0], err))
			}
			for _, f := range closeAfter {
				_ = f.Close()
			}
			if from != nil {
				// The child has the pipe now; when it exits, the writer
				// before it finds the pipe closed, as in a shell pipeline.
				_ = from.out.Close()
				from.out, from.piped = nil, true
			}
			go func() {
				p.waitErr = cmd.Wait()
				p.timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded) && !p.wasKilled()
				cancel()
				close(p.done)
			}()
			if err := s.closers.add(p); err != nil {
				return scheme.Value{}, err // add stopped it
			}
			s.report(Exec, "start", strings.Join(argv, " "), nil)
			return scheme.ObjectValue(p), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "proc-read-line",
		Meta: scheme.Meta{
			Doc:         "The next line of a process's output, without its line ending, waiting for it if the program has not written it yet; (eof-object) once the program has closed its output. A line longer than the host's output limit is an error. If the evaluation is cancelled while it waits, the program is stopped.",
			Params:      []scheme.Param{procP},
			TaintSource: scheme.TagShell,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := procArg(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			p.outMu.Lock()
			defer p.outMu.Unlock()
			switch {
			case p.piped:
				return scheme.Value{}, fmt.Errorf("the output of %s goes to another process", p.name)
			case p.eof || p.out == nil:
				return scheme.EOF(), nil
			}
			stop := context.AfterFunc(c.Ctx, p.kill)
			defer stop()
			line, ok, err := p.readLine()
			if cerr := c.Ctx.Err(); cerr != nil {
				return scheme.Value{}, cerr
			}
			if err != nil {
				return scheme.Value{}, err
			}
			if !ok {
				return scheme.EOF(), nil
			}
			return scheme.Str(line), nil
		},
	})
	s.add(&scheme.Prim{
		Name: "proc-write",
		Meta: scheme.Meta{
			Doc:    "Write a string to the input of a process started with (\"stdin\" . pipe), waiting while the program is not reading. Writing to a program that has exited, or after proc-close-input, is an error. If the evaluation is cancelled while it waits, the program is stopped.",
			Params: []scheme.Param{procP, {Name: "text", Type: "string", Role: "payload"}},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := procArg(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			text, err := c.String(1)
			if err != nil {
				return scheme.Value{}, err
			}
			p.inMu.Lock()
			defer p.inMu.Unlock()
			switch {
			case !p.inPipe:
				return scheme.Value{}, fmt.Errorf(`the input of %s is not a pipe: start it with ("stdin" . pipe)`, p.name)
			case p.in == nil:
				return scheme.Value{}, fmt.Errorf("the input of %s is closed", p.name)
			}
			stop := context.AfterFunc(c.Ctx, p.kill)
			defer stop()
			if _, err := io.WriteString(p.in, text); err != nil {
				if cerr := c.Ctx.Err(); cerr != nil {
					return scheme.Value{}, cerr
				}
				return scheme.Value{}, fmt.Errorf("%s: %w", p.name, unwrapPath(err))
			}
			return scheme.Void, nil
		},
	})
	s.add(&scheme.Prim{
		Name: "proc-close-input",
		Meta: scheme.Meta{
			Doc:    "Close the input of a process started with (\"stdin\" . pipe): the program reads the end of its input. Closing it again does nothing.",
			Params: []scheme.Param{procP},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := procArg(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			p.inMu.Lock()
			defer p.inMu.Unlock()
			if !p.inPipe {
				return scheme.Value{}, fmt.Errorf(`the input of %s is not a pipe: start it with ("stdin" . pipe)`, p.name)
			}
			if p.in != nil {
				err = p.in.Close()
				p.in = nil
			}
			return scheme.Void, err
		},
	})
	s.add(&scheme.Prim{
		Name: "proc-wait",
		Meta: scheme.Meta{
			Doc:         "Wait for a process to exit: an association list with \"exit-code\" (integer; -1 if a signal or proc-kill! ended it), \"stdout\" (the output not read with proc-read-line; empty when it went to another process) and \"stderr\". Waiting again gives the same result. A nonzero exit is a result, not an error; a timeout or output past the host's limit is an error. If the evaluation is cancelled while it waits, the program is stopped.",
			Params:      []scheme.Param{procP},
			TaintSource: scheme.TagShell,
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := procArg(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			return p.wait(c.Ctx)
		},
	})
	s.add(&scheme.Prim{
		Name: "proc-kill!",
		Meta: scheme.Meta{
			Doc:    "Stop a process (on Unix, and the processes it started with it). Stopping one that has exited does nothing.",
			Params: []scheme.Param{procP},
		},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			p, err := procArg(c, 0)
			if err != nil {
				return scheme.Value{}, err
			}
			select {
			case <-p.done:
			default:
				p.kill()
				s.report(Exec, "kill", p.name, nil)
			}
			return scheme.Void, nil
		},
	})
	s.add(&scheme.Prim{
		Name: "proc?",
		Meta: scheme.Meta{Doc: "True for processes.", Params: []scheme.Param{{Name: "x", Type: "any"}}},
		Handler: func(c *scheme.Call) (scheme.Value, error) {
			_, ok := scheme.ObjectOf(c.Args[0]).(*procObj)
			return scheme.Bool(ok), nil
		},
	})
	return nil
}

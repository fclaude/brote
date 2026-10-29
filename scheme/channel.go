package scheme

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

// channelObj is a Scheme channel: a queue of values with room for cap of them
// (none, for a rendezvous), closable, with tasks parked on either end. All its
// fields are guarded by the interpreter's task mutex.
type channelObj struct {
	rt     *taskRuntime
	id     int64
	cap    int
	buf    []Value
	closed bool
	recvq  []*wcase
	sendq  []*wcase
}

func (ch *channelObj) Kind() string   { return "channel" }
func (ch *channelObj) String() string { return fmt.Sprintf("#<channel %d>", ch.id) }
func (rt *taskRuntime) newChannel(cap int) *channelObj {
	return &channelObj{rt: rt, id: rt.ids.Add(1), cap: cap}
}

var (
	errSendClosed  = errors.New("send on a closed channel")
	errRecvClosed  = errors.New("receive on a closed channel")
	errCloseClosed = errors.New("close of a closed channel")
)

// pop takes the first queued case whose waiter is still waiting.
func pop(q *[]*wcase) *wcase {
	for len(*q) > 0 {
		c := (*q)[0]
		(*q)[0] = nil
		*q = (*q)[1:]
		if !c.w.done {
			return c
		}
	}
	return nil
}

// dequeue removes c from whichever queue it is on.
func (ch *channelObj) dequeue(c *wcase) {
	q := &ch.recvq
	if c.send {
		q = &ch.sendq
	}
	for i, x := range *q {
		if x == c {
			*q = append((*q)[:i], (*q)[i+1:]...)
			return
		}
	}
}

// trySend sends if that needs no waiting. rt.mu is held.
func (ch *channelObj) trySend(v Value) (done bool, err error) {
	if ch.closed {
		return true, errSendClosed
	}
	if rc := pop(&ch.recvq); rc != nil {
		ch.rt.fire(rc.w, rc.idx, v, true, nil)
		return true, nil
	}
	if len(ch.buf) < ch.cap {
		ch.buf = append(ch.buf, v)
		return true, nil
	}
	return false, nil
}

// tryRecv receives if that needs no waiting; ok is false for a closed, empty
// channel. rt.mu is held.
func (ch *channelObj) tryRecv() (done bool, v Value, ok bool) {
	if len(ch.buf) > 0 {
		v = ch.buf[0]
		ch.buf[0] = Value{}
		ch.buf = ch.buf[1:]
		if sc := pop(&ch.sendq); sc != nil {
			ch.buf = append(ch.buf, sc.val)
			ch.rt.fire(sc.w, sc.idx, Value{}, true, nil)
		}
		return true, v, true
	}
	if sc := pop(&ch.sendq); sc != nil {
		ch.rt.fire(sc.w, sc.idx, Value{}, true, nil)
		return true, sc.val, true
	}
	if ch.closed {
		return true, Value{}, false
	}
	return false, Value{}, false
}

// close closes the channel and wakes everyone parked on it. rt.mu is held.
func (ch *channelObj) close() error {
	if ch.closed {
		return errCloseClosed
	}
	ch.closed = true
	for rc := pop(&ch.recvq); rc != nil; rc = pop(&ch.recvq) {
		ch.rt.fire(rc.w, rc.idx, Value{}, false, nil)
	}
	for sc := pop(&ch.sendq); sc != nil; sc = pop(&ch.sendq) {
		ch.rt.fire(sc.w, sc.idx, Value{}, false, errSendClosed)
	}
	return nil
}

// selCase is one arm of a select.
type selCase struct {
	ch     *channelObj
	send   bool
	val    Value // what a send offers
	eof    Value // what a receive from a closed channel yields, if hasEOF
	hasEOF bool
}

// selectCaseObj is a case as Scheme holds it, between on-recv and select.
type selectCaseObj struct{ selCase }

func (s *selectCaseObj) Kind() string { return "select case" }
func (s *selectCaseObj) String() string {
	if s.send {
		return "#<select case send>"
	}
	return "#<select case recv>"
}

// doSelect performs one of the cases, waiting for one to be possible unless
// wait is false. Among the cases that are possible now it picks one at random,
// as Go does, so no case starves. idx is -1 when none was and wait is false.
func (rt *taskRuntime) doSelect(c *Call, cases []selCase, wait bool) (idx int, val Value, ok bool, err error) {
	rt.mu.Lock()
	for _, i := range rand.Perm(len(cases)) {
		sc := cases[i]
		if sc.send {
			if done, err := sc.ch.trySend(sc.val); done {
				rt.mu.Unlock()
				return i, True, true, sendErr(c, err)
			}
			continue
		}
		if done, v, ok := sc.ch.tryRecv(); done {
			rt.mu.Unlock()
			return recvResult(c, i, sc, v, ok)
		}
	}
	if !wait {
		rt.mu.Unlock()
		return -1, Value{}, false, nil
	}
	w := newWaiter(c.t)
	for i, sc := range cases {
		wc := &wcase{w: w, idx: i, ch: sc.ch, send: sc.send, val: sc.val}
		w.cases = append(w.cases, wc)
		if sc.send {
			sc.ch.sendq = append(sc.ch.sendq, wc)
		} else {
			sc.ch.recvq = append(sc.ch.recvq, wc)
		}
	}
	rt.park(c.Ctx, w)
	if w.err != nil && !errors.Is(w.err, errSendClosed) {
		return -1, Value{}, false, ctxOrDeadlock(c, w.err)
	}
	if cases[w.idx].send {
		return w.idx, True, true, sendErr(c, w.err)
	}
	return recvResult(c, w.idx, cases[w.idx], w.val, w.ok)
}

// ctxOrDeadlock reports why a wait ended without completing.
func ctxOrDeadlock(c *Call, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return wrap(c.Name, err)
	}
	return err
}

func sendErr(c *Call, err error) error {
	if err != nil {
		return errorf(c.Name, "%v", err)
	}
	return nil
}

// recvResult is the outcome of a receive that completed: ok is false when the
// channel was closed and empty, and v is then the case's eof.
func recvResult(c *Call, i int, sc selCase, v Value, ok bool) (int, Value, bool, error) {
	if ok {
		return i, v, true, nil
	}
	if sc.hasEOF {
		return i, sc.eof, false, nil
	}
	return i, Value{}, false, errorf(c.Name, "%v", errRecvClosed)
}

// after returns a channel that receives #t once, after d.
func (rt *taskRuntime) after(r *root, d time.Duration) *channelObj {
	ch := rt.newChannel(1)
	rt.startTimer(r, d, func() { ch.trySend(True) })
	return ch
}

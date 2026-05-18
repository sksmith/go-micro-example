package amqp

import (
	"context"
	"errors"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// fakeSession is a hand-written stand-in for realSession that lets
// tests script every broker outcome the Publish / Subscribe loops
// handle: confirm-supported vs not, publish error, delivery stream,
// Ack failure, close. The contract is intentionally narrow — only
// what TST-003's acceptance criteria need.
//
// Mutex-guarded: the publish loop runs in a goroutine, the test
// drives input/assertions from another goroutine.
type fakeSession struct {
	mu sync.Mutex

	// confirmErr is what Confirm returns. nil → "confirms supported";
	// non-nil → the loop falls back to its "close the confirm channel"
	// behaviour.
	confirmErr error

	// confirmCh is the channel the publish loop handed us via
	// NotifyPublish. Tests obtain it by calling fakeSession.confirms()
	// and then push amqp.Confirmation values onto it to simulate
	// broker acks/nacks. nil before NotifyPublish has been called.
	confirmCh chan amqp.Confirmation

	// deliveries is what Consume returns. Tests close it to simulate
	// the broker hanging up.
	deliveries chan amqp.Delivery
	consumeErr error

	// ackErr is returned from Ack to drive consumer-side failure
	// logging.
	ackErr error

	// closed and published record what the loop did so tests can
	// assert behaviour after the fact.
	closed     bool
	published  []amqp.Publishing
	ackedTags  []uint64
	confirmAsk bool
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		// confirmCh is populated when NotifyPublish fires. deliveries
		// is the fake broker's outbound stream; tests push onto it
		// directly to simulate inbound messages.
		deliveries: make(chan amqp.Delivery, 4),
	}
}

// confirms returns the channel the publish loop registered via
// NotifyPublish. Blocks (up to 1 s) until that registration has
// happened, so tests don't have to manually synchronise with the
// per-session setup phase.
func (f *fakeSession) confirms() chan amqp.Confirmation {
	for i := 0; i < 200; i++ {
		f.mu.Lock()
		c := f.confirmCh
		f.mu.Unlock()
		if c != nil {
			return c
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func (f *fakeSession) Confirm(_ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmAsk = true
	return f.confirmErr
}

// NotifyPublish in the real *amqp.Channel stores the passed channel
// and returns it; the broker pushes Confirmations onto it later. We
// mirror that here so the publish loop's local `confirm` variable
// stays the channel both sides are touching.
func (f *fakeSession) NotifyPublish(c chan amqp.Confirmation) chan amqp.Confirmation {
	f.mu.Lock()
	f.confirmCh = c
	f.mu.Unlock()
	return c
}

func (f *fakeSession) Publish(_, _ string, _, _ bool, msg amqp.Publishing) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, msg)
	return nil
}

func (f *fakeSession) Consume(_, _ string, _, _, _, _ bool, _ amqp.Table) (<-chan amqp.Delivery, error) {
	if f.consumeErr != nil {
		return nil, f.consumeErr
	}
	return f.deliveries, nil
}

func (f *fakeSession) Ack(tag uint64, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ackedTags = append(f.ackedTags, tag)
	return f.ackErr
}

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// snapshot returns copies of the recorded slices so test assertions
// don't race with concurrent appends from the publish/subscribe loop.
func (f *fakeSession) snapshot() (published []amqp.Publishing, acked []uint64, confirmAsk, closed bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	published = append(published, f.published...)
	acked = append(acked, f.ackedTags...)
	return published, acked, f.confirmAsk, f.closed
}

// scriptedDialer hands out sessions in the order provided. Errors
// in the slice are returned in place of a session for that call
// (simulates dial failures). Calls past the end of the script
// return errSessionFactoryExhausted, which doubles as a test-time
// "scripted nothing left to do" guard.
type scriptedDialer struct {
	mu      sync.Mutex
	results []dialResult
	calls   int
}

type dialResult struct {
	session Session
	err     error
}

func newScriptedDialer(results ...dialResult) *scriptedDialer {
	return &scriptedDialer{results: results}
}

func (s *scriptedDialer) Dial(_ context.Context) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	s.calls++
	if idx >= len(s.results) {
		return nil, errSessionFactoryExhausted
	}
	return s.results[idx].session, s.results[idx].err
}

func (s *scriptedDialer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedDialer) asDialer() Dialer {
	return s.Dial
}

var errSessionFactoryExhausted = errors.New("scripted dialer exhausted (test bug)")

package amqp

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Session is the broker surface that the redial-aware Publish and
// Subscribe loops actually exercise. Extracting it from the
// concrete `*amqp.Channel` / `*amqp.Connection` pair gives tests a
// seam where fakes can script every reconnect / confirm / Ack
// outcome the loops handle in production (TST-003).
//
// The method signatures mirror amqp091's exactly so the real
// implementation can satisfy the interface by embedding the
// channel and the connection without adapter shims.
type Session interface {
	Confirm(noWait bool) error
	NotifyPublish(c chan amqp.Confirmation) chan amqp.Confirmation
	Publish(exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	Ack(tag uint64, multiple bool) error
	Close() error
}

// Dialer builds a fresh Session. Returned errors signal a redial-
// worthy failure (network refused, broker rejected the channel,
// etc.); Redial backs off and retries until the parent context
// cancels.
type Dialer func(ctx context.Context) (Session, error)

// realSession is the production implementation of Session: a real
// AMQP connection + channel pair. Close() closes the *connection*
// (which tears down the channel along with it) rather than just the
// channel, so each `(connection, channel)` lifetime is paired.
type realSession struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func (s realSession) Confirm(noWait bool) error { return s.ch.Confirm(noWait) }

func (s realSession) NotifyPublish(c chan amqp.Confirmation) chan amqp.Confirmation {
	return s.ch.NotifyPublish(c)
}

func (s realSession) Publish(exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	return s.ch.Publish(exchange, key, mandatory, immediate, msg)
}

func (s realSession) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	return s.ch.Consume(queue, consumer, autoAck, exclusive, noLocal, noWait, args)
}

func (s realSession) Ack(tag uint64, multiple bool) error { return s.ch.Ack(tag, multiple) }

func (s realSession) Close() error {
	if s.conn == nil {
		return nil
	}
	return s.conn.Close()
}

// realDialer returns a Dialer that opens a fresh AMQP connection
// against url and yields a realSession wrapping the new
// (connection, channel) pair. Used by RedialURL for production
// wiring; tests use a hand-rolled Dialer that scripts the sequence
// of dial outcomes the loop has to handle.
func realDialer(url string) Dialer {
	return func(_ context.Context) (Session, error) {
		conn, err := amqp.Dial(url)
		if err != nil {
			return nil, err
		}
		ch, err := conn.Channel()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return realSession{conn: conn, ch: ch}, nil
	}
}

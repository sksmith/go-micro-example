package queue

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/config"
	"github.com/sksmith/go-micro-example/core/inventory"
	"github.com/streadway/amqp"
)

type InventoryQueue struct {
	cfg         *config.Config
	inventory   chan<- message
	reservation chan<- message
}

func NewInventoryQueue(ctx context.Context, cfg *config.Config) *InventoryQueue {
	invChan := make(chan message)
	resChan := make(chan message)

	iq := &InventoryQueue{
		cfg:         cfg,
		inventory:   invChan,
		reservation: resChan,
	}

	url := getUrl(cfg)

	go func() {
		publish(redial(ctx, url), invChan)
		ctx.Done()
	}()

	go func() {
		publish(redial(ctx, url), resChan)
		ctx.Done()
	}()

	return iq
}

func getUrl(cfg *config.Config) string {
	rmq := cfg.RabbitMQ
	return fmt.Sprintf("amqp://%s:%s@%s:%s", rmq.User.Value, rmq.Pass.Value, rmq.Host.Value, rmq.Port.Value)
}

func (i *InventoryQueue) PublishInventory(ctx context.Context, productInventory inventory.ProductInventory) error {
	body, err := json.Marshal(productInventory)
	if err != nil {
		return errors.WithMessage(err, "failed to serialize message for queue")
	}
	i.inventory <- message(body)
	return nil
}

func (i *InventoryQueue) PublishReservation(ctx context.Context, reservation inventory.Reservation) error {
	body, err := json.Marshal(reservation)
	if err != nil {
		return errors.WithMessage(err, "error marshalling reservation to send to queue")
	}
	i.reservation <- message(body)
	return nil
}

type ProductQueue struct {
	cfg        *config.Config
	product    <-chan message
	productDlt chan<- message
}

func NewProductQueue(ctx context.Context, cfg *config.Config, handler ProductHandler) *ProductQueue {
	prodChan := make(chan message)
	prodDltChan := make(chan message)

	pq := &ProductQueue{
		cfg:        cfg,
		product:    prodChan,
		productDlt: prodDltChan,
	}

	url := getUrl(cfg)

	go func() {
		for message := range prodChan {
			product := inventory.Product{}
			err := json.Unmarshal(message, &product)
			if err != nil {
				log.Error().Err(err).Msg("error unmarshalling product, writing to dlt")
				pq.sendToDlt(ctx, message)
			}

			handler.CreateProduct(ctx, product)
		}
	}()

	go func() {
		subscribe(redial(ctx, url), prodChan)
		ctx.Done()
	}()

	go func() {
		publish(redial(ctx, url), prodDltChan)
		ctx.Done()
	}()

	return pq
}

type ProductHandler interface {
	CreateProduct(ctx context.Context, product inventory.Product) error
}

func (p *ProductQueue) sendToDlt(ctx context.Context, body []byte) {
	p.productDlt <- message(body)
}

// exchange binds the publishers to the subscribers
const exchange = "pubsub"

// message is the application type for a message.  This can contain identity,
// or a reference to the recevier chan for further demuxing.
type message []byte

// session composes an amqp.Connection with an amqp.Channel
type session struct {
	*amqp.Connection
	*amqp.Channel
}

// Close tears the connection down, taking the channel with it.
func (s session) Close() error {
	if s.Connection == nil {
		return nil
	}
	return s.Connection.Close()
}

// redial continually connects to the URL, exiting the program when no longer possible
func redial(ctx context.Context, url string) chan chan session {
	sessions := make(chan chan session)

	go func() {
		sess := make(chan session)
		defer close(sessions)

		for {
			select {
			case sessions <- sess:
			case <-ctx.Done():
				log.Fatal().Msg("shutting down session factory")
				return
			}

			conn, err := amqp.Dial(url)
			if err != nil {
				log.Fatal().Err(err).Str("url", url).Msg("cannot (re)dial")
			}

			ch, err := conn.Channel()
			if err != nil {
				log.Fatal().Err(err).Msg("cannot create channel")
			}

			if err := ch.ExchangeDeclare(exchange, "fanout", false, true, false, false, nil); err != nil {
				log.Fatal().Err(err).Msg("cannot declare fanout exchange")
			}

			select {
			case sess <- session{conn, ch}:
			case <-ctx.Done():
				log.Info().Msg("shutting down new session")
				return
			}
		}
	}()

	return sessions
}

// publish publishes messages to a reconnecting session to a fanout exchange.
// It receives from the application specific source of messages.
func publish(sessions chan chan session, messages <-chan message) {
	for session := range sessions {
		var (
			running bool
			reading = messages
			pending = make(chan message, 1)
			confirm = make(chan amqp.Confirmation, 1)
		)

		pub := <-session

		// publisher confirms for this channel/connection
		if err := pub.Confirm(false); err != nil {
			log.Info().Msg("publisher confirms not supported")
			close(confirm) // confirms not supported, simulate by always nacking
		} else {
			pub.NotifyPublish(confirm)
		}

		log.Info().Msg("publishing...")

	Publish:
		for {
			var body message
			select {
			case confirmed, ok := <-confirm:
				if !ok {
					break Publish
				}
				if !confirmed.Ack {
					log.Info().Uint64("message", confirmed.DeliveryTag).Str("body", string(body)).Msg("nack")
				}
				reading = messages

			case body = <-pending:
				routingKey := "ignored for fanout exchanges, application dependent for other exchanges"
				err := pub.Publish(exchange, routingKey, false, false, amqp.Publishing{
					Body: body,
				})
				// Retry failed delivery on the next session
				if err != nil {
					pending <- body
					pub.Close()
					break Publish
				}

			case body, running = <-reading:
				// all messages consumed
				if !running {
					return
				}
				// work on pending delivery until ack'd
				pending <- body
				reading = nil
			}
		}
	}
}

// identity returns the same host/process unique string for the lifetime of
// this process so that subscriber reconnections reuse the same queue name.
func identity() string {
	hostname, err := os.Hostname()
	h := sha1.New()
	fmt.Fprint(h, hostname)
	fmt.Fprint(h, err)
	fmt.Fprint(h, os.Getpid())
	return fmt.Sprintf("%x", h.Sum(nil))
}

// subscribe consumes deliveries from an exclusive queue from a fanout exchange and sends to the application specific messages chan.
func subscribe(sessions chan chan session, messages chan<- message) {
	queue := identity()

	for session := range sessions {
		sub := <-session

		if _, err := sub.QueueDeclare(queue, false, true, true, false, nil); err != nil {
			log.Error().Str("queue", queue).Err(err).Msg("cannot consume from exclusive queue")
			return
		}

		routingKey := "application specific routing key for fancy toplogies"
		if err := sub.QueueBind(queue, routingKey, exchange, false, nil); err != nil {
			log.Error().Str("exchange", exchange).Err(err).Msg("cannot consume without a binding to exchange")
			return
		}

		deliveries, err := sub.Consume(queue, "", false, true, false, false, nil)
		if err != nil {
			log.Error().Str("queue", queue).Err(err).Msg("cannot consume from")
			return
		}

		log.Info().Msg("subscribed...")

		for msg := range deliveries {
			messages <- message(msg.Body)
			sub.Ack(msg.DeliveryTag, false)
		}
	}
}

// read is this application's translation to the message format, scanning from
// stdin.
func read(r io.Reader) <-chan message {
	lines := make(chan message)
	go func() {
		defer close(lines)
		scan := bufio.NewScanner(r)
		for scan.Scan() {
			lines <- message(scan.Bytes())
		}
	}()
	return lines
}

// write is this application's subscriber of application messages, printing to
// stdout.
func write(w io.Writer) chan<- message {
	lines := make(chan message)
	go func() {
		for line := range lines {
			fmt.Fprintln(w, string(line))
		}
	}()
	return lines
}

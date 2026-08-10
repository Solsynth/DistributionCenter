package eventbus

import (
	"context"

	"github.com/nats-io/nats.go"
	shared "src.solsynth.dev/sosys/go/pkg/eventbus"
)

type Bus struct {
	*shared.Bus
}

func New(conn *nats.Conn) *Bus {
	if conn == nil {
		return nil
	}
	bus, err := shared.New(conn)
	if err != nil {
		return nil
	}
	return &Bus{Bus: bus}
}

func Connect(url string) (*Bus, error) {
	if url == "" {
		return nil, nil
	}
	bus, err := shared.Connect(url)
	if err != nil {
		return nil, err
	}
	return &Bus{Bus: bus}, nil
}

func (b *Bus) Close() {
	if b != nil && b.Bus != nil {
		b.Bus.Close()
	}
}

func (b *Bus) PublishPublished(ctx context.Context, event ReleaseEvent) error {
	if b == nil || b.Bus == nil {
		return nil
	}
	return b.PublishJetStream(ctx, "distribution.release.published.v1", "distribution_events", event)
}

func (b *Bus) PublishYanked(ctx context.Context, event ReleaseEvent) error {
	if b == nil || b.Bus == nil {
		return nil
	}
	return b.PublishJetStream(ctx, "distribution.release.yanked.v1", "distribution_events", event)
}

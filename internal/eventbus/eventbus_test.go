package eventbus

import (
	"context"
	"testing"

	"src.solsynth.dev/sosys/distribution/internal/service"
)

func TestNilBusIsSafe(t *testing.T) {
	var bus *Bus
	if err := bus.PublishPublished(context.Background(), service.ReleaseEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := bus.PublishYanked(context.Background(), service.ReleaseEvent{}); err != nil {
		t.Fatal(err)
	}
}

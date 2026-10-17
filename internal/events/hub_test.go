package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"bord/internal/api"
	"bord/internal/domain"
)

func TestHubReplayResetAndSlowSubscriber(t *testing.T) {
	hub := NewHub(2, 1)
	defer hub.Close()
	for id := int64(1); id <= 3; id++ {
		if !hub.Publish(api.Event{ID: domain.EventID(id), BoardID: "test", Type: "post.created"}) {
			t.Fatal("publish rejected")
		}
	}
	replay, reset, subscription := hub.Subscribe("test", "", 2)
	defer subscription.Close()
	if reset || len(replay) != 1 || replay[0].ID != 3 {
		t.Fatalf("unexpected replay: %#v reset=%v", replay, reset)
	}
	_, reset, stale := hub.Subscribe("test", "", 1)
	defer stale.Close()
	if !reset {
		t.Fatal("stale subscriber was not reset")
	}
	_, _, slow := hub.Subscribe("test", "", 0)
	if !hub.Publish(api.Event{ID: 4, BoardID: "test"}) || !hub.Publish(api.Event{ID: 5, BoardID: "test"}) {
		t.Fatal("publish failed")
	}
	select {
	case _, open := <-slow.Events():
		if open {
			_, open = <-slow.Events()
		}
		if open {
			t.Fatal("slow subscriber remains open")
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscriber not disconnected")
	}
}

type outboxFake struct {
	events   []api.Event
	marked   []domain.EventID
	failMark bool
}

func (o *outboxFake) Pending(_ context.Context, after domain.EventID, _ int) ([]api.Event, error) {
	result := []api.Event{}
	for _, event := range o.events {
		if event.ID > after {
			result = append(result, event)
		}
	}
	return result, nil
}
func (o *outboxFake) MarkDelivered(_ context.Context, id domain.EventID) error {
	if o.failMark {
		o.failMark = false
		return errors.New("injected")
	}
	o.marked = append(o.marked, id)
	return nil
}

func TestDispatcherRetriesMarkWithoutDuplicateDelivery(t *testing.T) {
	hub := NewHub(10, 2)
	outbox := &outboxFake{events: []api.Event{{ID: 1, BoardID: "test"}}, failMark: true}
	d := Dispatcher{Outbox: outbox, Hub: hub}
	var after domain.EventID
	if err := d.dispatch(context.Background(), &after); err == nil {
		t.Fatal("expected injected failure")
	}
	if err := d.dispatch(context.Background(), &after); err != nil {
		t.Fatal(err)
	}
	if after != 1 || len(outbox.marked) != 1 {
		t.Fatalf("retry did not settle: after=%d marked=%v", after, outbox.marked)
	}
}

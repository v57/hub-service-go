package hubservice

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/v57/channel-go/channelgo"
	"github.com/v57/channel-go/server"
)

// testHub is a channel-go server standing in for hub-server.
func testHub(t *testing.T, ch *channelgo.Channel) int {
	t.Helper()
	srv, err := server.Listen(ch, 0, server.Options{})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	_, port, _ := strings.Cut(srv.Addr(), ":")
	number, _ := strconv.Atoi(port)
	return number
}

func noSign() (string, error) { return "", nil }

func TestOnConnectRunsAfterServiceUpdate(t *testing.T) {
	hub := channelgo.NewChannel()
	updated := make(chan struct{}, 1)
	hub.Post("hub/service/update", func(channelgo.Body) (any, error) {
		select {
		case updated <- struct{}{}:
		default:
		}
		return nil, nil
	})
	hub.Post("hello", func(channelgo.Body) (any, error) { return "world", nil })
	port := testHub(t, hub)

	replies := make(chan any, 1)
	s := New(ServiceOptions{Address: port, Sign: noSign, OnConnect: func(sender *channelgo.Sender) {
		// A blocking send from the hook and from a kept sender both work.
		go func() {
			value, err := sender.Send("hello", nil)
			if err != nil {
				value = err
			}
			replies <- value
		}()
	}})
	s.Post("echo", func(body any) (any, error) { return body, nil })
	s.Start()
	defer s.Stop()

	select {
	case <-updated:
	case <-time.After(3 * time.Second):
		t.Fatal("hub/service/update not received")
	}
	select {
	case value := <-replies:
		if value != "world" {
			t.Fatalf("send from OnConnect sender = %v", value)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnConnect did not run")
	}
	if s.Sender() == nil {
		t.Fatal("Sender() nil while started")
	}
	s.Stop()
	if s.Sender() != nil {
		t.Fatal("Sender() not nil after Stop")
	}
}

func TestStreamContextIsCancelledWithTheStream(t *testing.T) {
	// The hub reads one value from the service's stream and cancels it, from
	// a hub handler (channel-go's server sends only from its own loop).
	hub := channelgo.NewChannel()
	var service *channelgo.Sender // set and used on the hub's loop only
	hub.Post("probe", func(body channelgo.Body) (any, error) {
		values := service.Values("wait", nil)
		defer values.Return()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		item, err := values.Next(ctx)
		if err != nil {
			return nil, err
		}
		return item.Value, nil
	})
	ready := make(chan struct{}, 1)
	hub.Post("hub/service/update", func(body channelgo.Body) (any, error) {
		if service == nil {
			service = body.Sender
		}
		select {
		case ready <- struct{}{}:
		default:
		}
		return nil, nil
	})
	port := testHub(t, hub)

	cancelled := make(chan struct{})
	s := New(ServiceOptions{Address: port, Sign: noSign})
	s.StreamContext("wait", func(ctx context.Context, body any, emit func(any) error) error {
		if err := emit("first"); err != nil {
			return err
		}
		<-ctx.Done()
		close(cancelled)
		return nil
	})
	s.Start()
	defer s.Stop()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("service did not connect")
	}

	// A second client asks the hub to probe the service.
	caller := New(ServiceOptions{Address: port, Sign: noSign})
	caller.Start()
	defer caller.Stop()
	var value any
	var err error
	for range 50 {
		if value, err = caller.Send("probe", nil); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || value != "first" {
		t.Fatalf("probe = %v, %v", value, err)
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("handler context not cancelled after the caller cancelled")
	}
}

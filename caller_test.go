package hubservice

import (
	"context"
	"testing"
	"time"

	"github.com/v57/channel-go/channelgo"
)

func TestRequestFromReadsVectorCaller(t *testing.T) {
	v := loadVectors(t)
	contextValue := map[string]any{"hub": v.Caller.Context["hub"], "service": "abc"}
	request := RequestFrom("body", contextValue)
	caller := request.Caller
	if caller == nil || caller.Key != v.Keys["service"].PublicKey || !caller.Owner || !caller.InGroup("owners") {
		t.Fatalf("caller: %+v", caller)
	}
	if caller.Device == nil || caller.Device.ID != v.Keys["device"].DeviceID || caller.Device.Name != "Mac Studio" {
		t.Fatalf("device: %+v", caller.Device)
	}
	if request.HubKey != v.Keys["hub"].PublicKey || request.Body != "body" {
		t.Fatalf("request: %+v", request)
	}
}

func TestRequestFromWithoutHubContext(t *testing.T) {
	for _, contextValue := range []any{nil, map[string]any{}, map[string]any{"hub": "x"}, map[string]any{"hub": map[string]any{}}} {
		if request := RequestFrom(1, contextValue); request.Caller != nil || request.HubKey != "" {
			t.Fatalf("%v: %+v", contextValue, request)
		}
	}
	anonymous := RequestFrom(nil, map[string]any{"hub": map[string]any{"caller": map[string]any{"groups": []any{}}}})
	if anonymous.Caller == nil || anonymous.Caller.Key != "" || anonymous.Caller.Device != nil || anonymous.Caller.Owner {
		t.Fatalf("anonymous: %+v", anonymous.Caller)
	}
}

func TestRequestFromMergedHub(t *testing.T) {
	request := RequestFrom(nil, map[string]any{"hub": map[string]any{"caller": map[string]any{
		"key": "merging", "via": "merged-hub", "claimed": map[string]any{"key": "far", "owner": true},
	}}})
	if c := request.Caller; c.Via != "merged-hub" || c.Claimed == nil || c.Claimed.Key != "far" || !c.Claimed.Owner || c.Owner {
		t.Fatalf("merged caller: %+v", c)
	}
}

func TestHandlersSeeCallerThroughHub(t *testing.T) {
	// The test hub forwards a call to the service with a caller context, as
	// hub-pro does.
	hub := channelgo.NewChannel()
	var service *channelgo.Sender // set and used on the hub's loop only
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
	hubKey := loadVectors(t).Keys["hub"].PublicKey
	callerContext := map[string]any{"hub": map[string]any{
		"caller": map[string]any{"key": "K", "device": map[string]any{"id": "D", "name": "Phone"}, "groups": []any{"g"}},
		"hubKey": hubKey,
	}}
	hub.Post("probe", func(body channelgo.Body) (any, error) {
		post, err := service.Send("whoami", nil, callerContext)
		if err != nil {
			return nil, err
		}
		values := service.Values("whoami/stream", nil, callerContext)
		defer values.Return()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		item, err := values.Next(ctx)
		if err != nil {
			return nil, err
		}
		return []any{post, item.Value}, nil
	})
	port := testHub(t, hub)

	private, _ := KeyFromFile(loadVectors(t).Keys["service"].KeyFile)
	s := New(ServiceOptions{Address: port, Key: private, HubKey: hubKey, Device: DeviceOptions{Disabled: true}})
	describe := func(request Request) any {
		if request.Caller == nil || request.Caller.Device == nil {
			return "no caller"
		}
		return request.Caller.Key + "@" + request.Caller.Device.Name
	}
	s.PostRequest("whoami", func(request Request) (any, error) { return describe(request), nil })
	s.StreamRequest("whoami/stream", func(ctx context.Context, request Request, emit func(any) error) error {
		return emit(describe(request))
	})
	s.Start()
	defer s.Stop()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("service did not connect")
	}
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
	results, _ := value.([]any)
	if err != nil || len(results) != 2 || results[0] != "K@Phone" || results[1] != "K@Phone" {
		t.Fatalf("probe = %v, %v", value, err)
	}

	// A caller under another hub key (or none pinned) isn't trusted.
	if err := s.ForgetHubKey(); err != nil {
		t.Fatal(err)
	}
	if value, err = caller.Send("probe", nil); err != nil {
		t.Fatal(err)
	}
	if results, _ := value.([]any); len(results) != 2 || results[0] != "no caller" || results[1] != "no caller" {
		t.Fatalf("unpinned probe = %v", value)
	}
}

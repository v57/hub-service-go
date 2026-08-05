package hubservice

import (
	"encoding/json"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

func TestServicesListIncludesRegisteredPaths(t *testing.T) {
	s := New()
	s.Post("echo", func(body any) (any, error) { return body, nil })
	s.Stream("echo/values", func(body any, emit func(any) error) error { return nil })
	s.App(App{Header: AppHeader{Type: "app", Name: "Echo", Path: "echo/ui"}, Body: []any{"hi"}})
	u := s.update()
	if len(u.Services) != 3 {
		t.Fatalf("expected 3 services (post+stream+app path), got %d: %+v", len(u.Services), u.Services)
	}
	if len(u.Apps) != 1 || u.Apps[0].Path != "echo/ui" || u.Apps[0].Type != "app" {
		t.Fatalf("unexpected apps: %+v", u.Apps)
	}
}

func TestSettingsApplyToUpdatePayload(t *testing.T) {
	s := New()
	limit := 5
	s.Post("echo", func(body any) (any, error) { return body, nil },
		ApiSettings{Permissions: &ServicePermissions{Name: "Echo", Allows: boolPtr(true)}, Limit: &limit})
	u := s.update()
	if len(u.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(u.Services))
	}
	svc := u.Services[0]
	if svc.Limit == nil || *svc.Limit != 5 {
		t.Fatalf("expected limit 5, got %v", svc.Limit)
	}
	if svc.Permissions == nil || svc.Permissions.Group != "echo" || svc.Permissions.Name != "Echo" {
		t.Fatalf("permissions group auto-fill failed: %+v", svc.Permissions)
	}
}

func TestProfileOnlyWhenSet(t *testing.T) {
	plain := New()
	if plain.profile != nil {
		t.Fatal("no profile expected")
	}
	named := New(ServiceOptions{Name: "Echo"})
	if named.profile == nil || named.profile.Name != "Echo" {
		t.Fatalf("profile expected with name: %+v", named.profile)
	}
}

func TestUpdateJSONMatchesWireContract(t *testing.T) {
	s := New()
	limit := 5
	allows := true
	s.Post("echo", func(body any) (any, error) { return body, nil },
		ApiSettings{Permissions: &ServicePermissions{Name: "Echo", Allows: &allows}, Limit: &limit})
	raw, err := json.Marshal(s.update())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"services":[{"path":"echo","permissions":{"group":"echo","name":"Echo","allows":true},"limit":5}],"apps":[]}`
	if string(raw) != want {
		t.Fatalf("update payload mismatch:\n got: %s\nwant: %s", raw, want)
	}
}

func TestSendBeforeStartErrors(t *testing.T) {
	s := New()
	if _, err := s.Send("echo", "x"); err == nil {
		t.Fatal("expected error before Start")
	}
	if _, err := s.Values("echo", "x"); err == nil {
		t.Fatal("expected error before Start")
	}
}

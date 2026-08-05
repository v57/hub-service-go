package main

import (
	"fmt"
	"strings"

	"github.com/v57/hub-service-go"
)

func main() {
	upper := func(body any) (any, error) {
		return strings.ToUpper(fmt.Sprint(body)), nil
	}
	service := hubservice.New(hubservice.ServiceOptions{
		Name: "Echo",
		Icon: map[string]any{"symbol": "speaker.wave.2", "text": "E"},
	})
	service.Post("echo", func(body any) (any, error) {
		return body, nil
	})
	service.Post("echo/upper", upper)
	service.App(hubservice.App{
		Header: hubservice.AppHeader{Type: "app", Name: "Echo", Path: "echo/ui"},
		Body: []any{
			"Hello from Go",
			hubservice.TextField("echo", "Text to echo", &hubservice.Action{Path: "echo/upper", Body: "value", Output: "value"}),
			hubservice.Cell("$Echo", "value"),
			hubservice.Button("Upper", hubservice.Action{Path: "echo/upper", Body: "value", Output: "value"}),
		},
		Data: map[string]any{"hello": "world"},
	})
	service.Start()
	fmt.Println("echo service started (hub-lite :1997)")
	select {}
}

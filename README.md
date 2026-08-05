<h1>
  <img alt="Hub Service logo" src="./icon.png" width="70" valign="middle">
  &nbsp;hub-service-go
</h1>

Go port of [hub-service](https://github.com/v57/hub-service) — connect to a
[hub](https://github.com/v57/hub-lite) (default `ws://127.0.0.1:1997`), host
your own post/stream API endpoints, register apps, and call API through the hub.

Lite port: no auth, no keychain, no crypto (hub-pro territory). The wire
contract matches the TS reference (`hub/profile/update`, `hub/service/update`,
AppInterface + element DSL).

## Install

```bash
go get github.com/v57/hub-service-go
```

## Usage

Start a hub first (`new Hub()` from hub-lite, listens on 1997), then:

```go
package main

import (
  "fmt"
  "strings"

  "github.com/v57/hub-service-go"
)

func main() {
  service := hubservice.New(hubservice.ServiceOptions{
    Name: "Echo",
    Icon: map[string]any{"symbol": "speaker.wave.2", "text": "E"},
  })
  service.Post("echo", func(body any) (any, error) { return body, nil })
  service.Post("echo/upper", func(body any) (any, error) {
    return strings.ToUpper(fmt.Sprint(body)), nil
  })
  service.App(hubservice.App{
    Header: hubservice.AppHeader{Type: "app", Name: "Echo", Path: "echo/ui"},
    Body: []any{
      "Hello from Go",
      hubservice.TextField("echo", "Text to echo",
        &hubservice.Action{Path: "echo/upper", Body: "value", Output: "value"}),
      hubservice.Cell("$Echo", "value"),
    },
  })
  service.Start()
  select {}
}
```

Call it through the hub (any language):

```go
response, _ := service.Send("echo/upper", "hello") // "HELLO"
```

## API

- `New(options ...ServiceOptions)` — `Address` (port or ws:// URL; default
  `1997` or `$HUB`), `Name`, `Icon`
- `Post(path, handler func(body any) (any, error), settings ...ApiSettings) *Service`
- `Stream(path, handler func(body any, emit func(any) error) error, settings ...ApiSettings) *Service`
- `App(app App) *Service`
- `Send(path, body, context...) (any, error)` — client call through the hub
- `Values(path, body, context...) (*channelgo.Values, error)` — stream call
- `Start() *Service` — connect; re-announces `hub/service/update` on every reconnect

`ApiSettings` carries `Permissions` (group auto-filled from the first path
segment, TS parity) and `Limit` — wire-parity fields that hub-lite ignores.

## Test

```bash
./scripts/test            # unit tests
go run ./example          # example service (needs a hub on 1997)
# cross-language check (TS):
bun ../hub-service/tests/verify.ts echo echo/upper '"hello"' '"HELLO"'
```

<h1>
  <img alt="Hub Service logo" src="./icon.png" width="70" valign="middle">
  &nbsp;hub-service-go
</h1>

Go port of [hub-service](https://github.com/v57/hub-service) — connect to a
[hub](https://github.com/v57/hub-lite) (default `ws://127.0.0.1:1997`), host
your own post/stream API endpoints, register apps, and call API through the hub.

Works with hub-lite and hub-pro: every (re)connect sends a fresh hub-pro auth
token signed with the service's Ed25519 key (hub-lite ignores it). The wire
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
  `1997` or `$HUB`), `Name`, `Icon`, `Key` (service key; default the
  keychain), `HubKeys` (pin file), `HubKey` (hub key known up front), `Sign`
  (custom v0 token factory; turns off hub key pinning and device mode),
  `Device` (device mode options)
- `Post(path, handler func(body any) (any, error), settings ...ApiSettings) *Service`
- `Stream(path, handler func(body any, emit func(any) error) error, settings ...ApiSettings) *Service`
- `PostRequest(path, handler func(Request) (any, error), settings...)` and
  `StreamRequest(path, handler func(ctx, Request, emit) error, settings...)` —
  handlers that see `Request.Caller` (key, device, groups, owner) and
  `Request.HubKey` from the hub's `context.hub`; `Caller` is nil on hubs that
  send none (hub-lite)
- `App(app App) *Service`
- `Send(path, body, context...) (any, error)` — client call through the hub
- `Values(path, body, context...) (*channelgo.Values, error)` — stream call
- `Start() *Service` — connect with `auth` + `v` headers; re-sends
  `hub/profile/update` (if set) and `hub/service/update` on every reconnect
- `HubKey()` / `ForgetHubKey()` — the pinned hub key for this address
- `Sign() (string, error)` — fresh v0 `key.<pub>.<sig>.<expiry>` token (60 s);
  `SignHub(hubKey)` and `SignToken(key, hubKey, expiry)` sign v1 tokens
- `PublicKey() (string, error)` — the service's SPKI public key (base64), its
  hub-pro identity
- `SetKeyPath(path)` — key file location (default `$HUBKEY`, else `auth` in
  the cwd; created on first use, same format as the TS keychain)

`ApiSettings` carries `Permissions` (group auto-filled from the first path
segment, TS parity) and `Limit` — wire-parity fields that hub-lite ignores.

## Hub-bound tokens

A v1 token signs `hub1:<hub key>:<expiry>`, so it only works on the hub it was
made for (`Docs/Devices/protocol.md`). On its first connect to a hub the
service connects with v0 and only asks `hub/info` for the hub's key. It pins
the key in `hubs.json` beside the key file (`{ "ws://host:port": "<hub SPKI>" }`)
and reconnects with v1 before registering anything. hub-lite doesn't answer
`hub/info`, so services stay on v0 there. A pinned hub that refuses the token
is reported, never silently downgraded; `ForgetHubKey` clears the pin.

## Devices

A device is one OS instance with its own Ed25519 key. On every connect the
service sends a `device` header next to `auth`: a certificate, signed by the
device key, saying this service key runs on this device under an instance
name. A hub that approved the device attaches the connection to it; anything
else stays a plain key connection.

- Device key: `$HUB_DEVICE_KEY`, else `/var/lib/hub/device.key` for uid 0,
  else `~/.hub/device.key` (created on first use, 0600, same layout as `auth`).
  `LoadDeviceKey`, `SetDeviceKeyPath`, `DeviceKey.ID()` and `SafetyCode()`.
- `DeviceOptions`: `Disabled`, `Key`, `Instance` (default the service `Name`,
  else the program name), `Name` (default the host name), `Platform`,
  `Parent` (for containers and VMs), `Lifetime` (at most and by default 7
  days). Certificates are renewed at connect with less than a day left.

## Test

```bash
./scripts/test            # unit tests
go run ./example          # example service (needs a hub on 1997)
# cross-language check (TS):
bun ../hub-service/tests/verify.ts echo echo/upper '"hello"' '"HELLO"'
```

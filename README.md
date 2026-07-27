# hub-service-go

Go Hub Service SDK. Layered on [`channel-go`](../channel-go), provides the same
service identity, registration, RPC, streaming, and network-share publication
behavior as the Bun `hub-service` and Swift `HubService` libraries.

## Status

Conformance-tested scaffold (HN-070 through HN-076). Tagged release tracked by
HN-078.

## Module

```
module github.com/v57/hub-service-go
```

Consumed locally via `replace github.com/v57/channel-go => ../channel-go` until
`channel-go` is published.

## Supported platforms

Same as `channel-go`: Darwin and Linux, `amd64` and `arm64`. Windows is not
supported.

## Packages

| Package | Purpose |
| --- | --- |
| `hubservice` (root) | `Service` connection wrapper, RPC helpers, share publication |
| `identity` | Persistent Ed25519 Hub identity, auth tokens, local-agent challenge signing |
| `network` | Network-share publisher, local-agent attestation, overlay publications |
| `service` | Registration, profile, permission wire types |
| `protocol` | Shared Hub Network wire models (cross-language contract) |

## Key storage

`identity.Load(path)` reads PKCS8-private || SPKI-public base64 material from
`path`. When the file is absent or empty, a fresh Ed25519 keypair is generated
and persisted with file mode `0600`. The on-disk format is byte-identical to
the Bun `hub-service` `auth` keychain file, so a service identity created by
either SDK can be read by the other.

## Public API

### `hubservice.New(Options) (*Service, error)`

Constructs a service. `Options`:

| Field | Description |
| --- | --- |
| `Address` | Hub address: integer port, `host:port`, or `ws://...` URL. Defaults to `1997`. |
| `Name`, `Icon` | Optional profile metadata pushed to Hub on connect. |
| `IdentityPath` | Filesystem path of persistent identity. Generates if absent. |
| `Identity` | Explicit identity. Overrides `IdentityPath`. |
| `Network` | Pre-built network publisher. Defaults to `network.New(NetworkOptions)`. |
| `NetworkOptions` | Configures the default network publisher. |

### Service methods

| Method | Behavior |
| --- | --- |
| `Start()` / `Stop()` | Connect / disconnect. On every (re)connect pushes profile, `hub/service/update`, and republishes declared network shares. |
| `Post(path, fn, Settings)` / `Stream(path, fn, Settings)` | Register RPC handlers; re-registered on reconnect. |
| `App(name, path)` | Register an app route. |
| `Send(path, body, ctx...)` | Blocking request/response. |
| `SendTyped[T](svc, path, body, ctx...)` | Typed response helper. |
| `Request` / `Values` / `Subscribe` | Async, streaming, subscription helpers. |
| `Share` / `PublishShare` / `UpdateShare` / `RemoveShare` / `SharePublications` | Network-share lifecycle. |
| `Network()` / `Identity()` / `Channel()` / `Sender()` | Access underlying objects. |

`Settings.Permissions.Group` defaults to the first path segment when unset
(matching the Bun SDK).

### Network shares

```go
svc, _ := hubservice.New(hubservice.Options{Name: "s3-local", IdentityPath: "auth"})

// Option A: declare before Start — published on connect, republished on reconnect.
svc.Share("s3", network.ProtocolTCP, "127.0.0.1", 9000, &network.Policy{Mode: network.PolicyHub})
svc.Start()

// Option B: publish at runtime once the listener port is known.
publications, _ := svc.PublishShare(ctx, "s3", network.ProtocolTCP, "127.0.0.1", 9000,
    &network.Policy{Mode: network.PolicyGroup, Group: "editors"})
```

Policy modes: `hub`, `group`, `keys`, `private` (default), `disabled`.

Publication flow:

1. Service discovers local `hub-net` agent at the configured Unix socket
   (default `/var/run/hub-net/agent.sock`).
2. Agent issues a nonce via `POST /challenge`.
3. Service signs the nonce with its identity and POSTs `signature` to
   `/attest`.
4. Agent returns a signed attestation binding service key, agent key, share
   name, protocol, target.
5. Service validates the attestation matches the declared share, submits the
   full declared set to Hub via `network/shares/replace`.
6. Hub returns overlay publications (`resourceId`, `overlayAddress`, `port`).

Failure modes surface as `*network.LocalAgentError` with codes:
`agent-unavailable`, `agent-timeout`, `agent-rejected`,
`invalid-agent-response`, `attestation-mismatch`, `attestation-expired`.

Override the default agent flow with `network.Options.AttestationProvider`
(testing, custom agents).

## Examples

`examples/basic` shows minimal identity load, post handler registration, and
connect.

## Conformance

`protocol/testdata/v1/*.json` is the cross-language contract, consumed by:

- Bun `hub-service` `network.contract.test.ts`
- Swift `HubService` parity tests
- Go `protocol` round-trip tests, `network` conformance test

The Go SDK reconstructs the shares-replace wire share and asserts
byte-identical JSON to the fixture.

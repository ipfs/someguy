# AGENTS.md

someguy is a server implementing the [Delegated Routing V1 HTTP API](https://specs.ipfs.tech/routing/http-routing-v1/).
It proxies requests to the Amino DHT and other delegated routing endpoints. It
is a caching proxy, not a libp2p node.

## Build and test

```bash
go build ./...
go test ./...
```

Run `gofmt` and `go vet ./...` before committing.

## Code map

- `main.go`, `server.go`: CLI entry point, host and router wiring.
- `server_routers.go`: router composition (`composableRouter`, `parallelRouter`, `libp2pRouter`, `sanitizeRouter`).
- `server_cached_router.go`, `cached_addr_book.go`: address caching layer.
- `server_dht.go`: DHT setup (standard and accelerated).
- `server_delegated_routing.go`: delegated HTTP routing clients.

## Documentation

- [environment-variables.md](docs/environment-variables.md): all config flags and environment variables
- [peer-address-caching.md](docs/peer-address-caching.md): how `/providers` and `/peers` cache and refresh peer addresses
- [metrics.md](docs/metrics.md): Prometheus metrics
- [tracing.md](docs/tracing.md): OpenTelemetry tracing

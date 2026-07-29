# AGENTS.md

someguy is a server implementing the [Delegated Routing V1 HTTP API](https://specs.ipfs.tech/routing/http-routing-v1/).
It proxies requests to the Amino DHT and other delegated routing endpoints. It
is a caching proxy, not a libp2p node.

## someguy is a proxy: pass through what you do not understand

someguy sits between clients and other routing systems. Anything it does not
recognize belongs to somebody else, so it forwards it untouched rather than
dropping it or normalizing it. someguy is not the only thing that will read
these records.

**Unknown record schemas.** A record whose `Schema` is not `peer` (or the
deprecated `bitswap`) arrives as a `types.UnknownRecord` holding its raw JSON,
and must reach the client with its fields intact. Every place that switches on
schema has to fall through: `sanitizeRouter`, `dnsAddrRouter`, and boxo's own
filter all do. Adding a `default` arm that touches the record, or a case that
rewrites one, breaks forward compatibility for a schema shipped after this
build.

**Unknown multiaddr components.** An address may carry protocols someguy has no
handling for. It must survive with its components in order and unmodified.
`addrSortRank` buckets anything it does not know rather than dropping it, and
`filterPrivateMultiaddr` removes only private addresses. Nothing may rewrite an
address it did not parse for a reason.

Note the limit of this: a protocol name absent from the multiaddr registry
fails to parse in go-multiaddr, so boxo rejects the record before someguy sees
it. someguy can only pass through what it can parse, and widening that is a
go-multiaddr concern, not a someguy one.

`TestUnknownSchemaPassesThrough` and `TestUnknownMultiaddrProtocolPassesThrough`
hold both properties.

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
- [dnsaddr-resolution.md](docs/dnsaddr-resolution.md): why `/dnsaddr` is resolved before `filter-addrs`, and how it is bounded
- [metrics.md](docs/metrics.md): Prometheus metrics
- [tracing.md](docs/tracing.md): OpenTelemetry tracing

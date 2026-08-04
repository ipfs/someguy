# AGENTS.md

someguy is a server implementing the [Delegated Routing V1 HTTP API](https://specs.ipfs.tech/routing/http-routing-v1/).
It proxies requests to the Amino DHT and other delegated routing endpoints. It
is a caching proxy, not a libp2p node.

## Contracts that must not break

Breaking one of these is easy to miss. The streaming and timeout rules below
both broke in production while the test suite and a plain `curl` passed.

**The HTTP API follows the spec.** [Delegated Routing
V1](https://specs.ipfs.tech/routing/http-routing-v1/) defines the response
shapes, status codes, and query parameters. Adding a field, header, or endpoint
it does not define needs an [IPIP](https://specs.ipfs.tech/ipips/) first.
`Cache-Control` values come from `boxo/routing/http/server`. Do not set them in
someguy.

**NDJSON responses stream.** A record reaches the client as soon as someguy has
it. Never add middleware that buffers the response body, and never collect
records before writing them. Compression is the known trap. It withholds small
writes until it has enough bytes to choose an encoding, which turns a stream
into one batch at the end and hides the response headers too.
`TestCompressedNDJSONFlushesEachRecord` guards this. Test with `curl
--compressed`, because plain `curl` requests no compression and streams fine
either way. See [response-streaming.md](docs/response-streaming.md).

**someguy is tuned for browsers.** A browser client has a small request budget
and a person waiting on it. someguy spends its own resources to save the
client's: it resolves addresses server side and shares the answer through its
cache. Weigh any change that pushes work back onto the client against that. See
[who this is tuned for](docs/response-streaming.md#who-this-is-tuned-for).

**The routing timeout stays below client deadlines.** A client that gives up
first loses every record someguy resolved, and reads it as no providers. See
[the timeout budget](docs/response-streaming.md#the-timeout-budget).

**Work that outlives a request stays bounded.** Background lookups keep running
after the client disconnects, which is what keeps the cache warm for the next
request. Anything that spawns them must cap concurrency, or a cheap request
leaves unbounded work behind. See [deliberate
trade-offs](docs/peer-address-caching.md#deliberate-trade-offs).

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
- [peer-address-caching.md](docs/peer-address-caching.md): how `/providers` and `/peers` cache and refresh peer addresses, and the trade-offs behind what someguy deliberately does not do
- [response-streaming.md](docs/response-streaming.md): NDJSON streaming contract, the timeout budget, and traps that break streaming
- [dnsaddr-resolution.md](docs/dnsaddr-resolution.md): why `/dnsaddr` is resolved before `filter-addrs`, and how it is bounded
- [metrics.md](docs/metrics.md): Prometheus metrics
- [tracing.md](docs/tracing.md): OpenTelemetry tracing

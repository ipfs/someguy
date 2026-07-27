# Response Streaming and Timeouts

A `/routing/v1` request can take seconds. someguy sends results as it finds
them rather than waiting for all of them, so a client can start dialing the
first provider while someguy is still resolving the rest.

This document covers the streaming contract, the timeout budget, and the two
traps that break streaming.

## Who this is tuned for

someguy is tuned for browser clients, and a browser works under tighter limits
than a server. A page shares one connection pool across everything it loads, so
every routing lookup is a request it cannot spend on content. Someone is waiting on the result, so a provider that arrives in two
seconds is useful and the same provider after twenty usually is not. A tab has
less memory and CPU than a server, and a service worker running alongside the
page it serves has less still.

Two behaviors follow, and both look wrong from a server's point of view.
Results go out as someguy finds them, so a client can act on the first usable
provider instead of waiting for the slowest lookup in the batch. Providers
whose addresses cannot be resolved are left out rather than returned as bare
peer IDs, because someguy resolves them once and shares the answer through its
cache. See [deliberate
trade-offs](peer-address-caching.md#deliberate-trade-offs).

A client that runs its own DHT and has resources to spare would want the
opposite of both.

## Streaming only happens with NDJSON

A client gets streaming only when it asks for it:

| `Accept` | Behavior |
| --- | --- |
| `application/x-ndjson` | one record per line, flushed as each is found |
| `application/json` | one document, sent after every result is collected |
| missing or `*/*` | treated as `application/json` |

The JSON path reads the whole result set into memory before it writes
anything, so it pays the full lookup latency. Send `Accept:
application/x-ndjson` if you want results early. Helia and verified-fetch do,
through `@helia/delegated-routing-v1-http-api`.

## An open connection means "still working"

someguy holds the response open while background address lookups are still
running. It closes the stream when it has nothing left to resolve.

That gives a client a signal it can use without any extra protocol. While the
connection is open, more records may still arrive. Once it closes, someguy is
done. An empty NDJSON stream that closes immediately means someguy found
nothing, not that it gave up early.

There is no "retry after" hint for streams. NDJSON sends its headers with the
first record, before someguy knows whether later lookups will succeed, so no
header can describe the outcome of a stream. The only channels left would be a
trailing metadata record or an HTTP trailer, and neither is in the [Delegated
Routing V1 spec](https://specs.ipfs.tech/routing/http-routing-v1/).

## The timeout budget

Two clocks run at the same time, and they must not tie.

```
client deadline   |-------------------------------------|
someguy routing   |----------------------------|
                                                ^        ^
                                    someguy flushes      client gives up
```

someguy stops its routing lookups at
[`SOMEGUY_ROUTING_TIMEOUT`](environment-variables.md#someguy_routing_timeout),
and its default is set below the deadline the reference client applies. Helia's
delegated routing client aborts the whole request after 30 seconds, and it
starts counting before someguy does, because the request has to reach someguy
first.

If both used the same value, a slow lookup would end with the client aborting
at the same moment someguy was about to send. Every record someguy resolved
would be lost, and the client would report no providers. Keep the timeout below
the deadline your clients apply, and leave room for network latency.

> [!IMPORTANT]
> Raising `SOMEGUY_ROUTING_TIMEOUT` to or above the client deadline does not
> return more results. It returns fewer, because the client stops listening
> first.

## Trap: middleware that buffers small writes

Streaming breaks if anything between the handler and the socket holds bytes
back. someguy compresses responses, and compression middleware usually waits
for a minimum body size before it decides whether to compress. Until it
decides, a flush does nothing.

Provider records are small, often under 200 bytes. With a minimum size in
place, a provider someguy had already resolved would sit in the buffer until a
later record filled it, or until the handler returned. Response headers were
held back the same way, so the request looked hung rather than slow.

someguy therefore compresses from the first byte (`MinSize(0)` in
`newCompressionAdapter`). `TestCompressedNDJSONFlushesEachRecord` guards this.
It asserts that the first record is readable while the handler is still
writing, and that responses are still compressed, so the test cannot be
satisfied by turning compression off.

Note that only clients sending `Accept-Encoding: gzip` were affected. A plain
`curl` requests no compression, so it streamed correctly the whole time. Test
streaming with `curl --compressed`, or the trap stays invisible.

## Trap: a client that fans out on every record

someguy returns providers as it finds them, so results arrive early and in
pieces. A client that starts a fresh `/routing/v1/peers/{peerid}` request for
every record it sees can issue dozens of lookups for a single content request.

Nothing in the browser stops this. The six-connection limit applies to
HTTP/1.1 only. Over HTTP/2 the cap is whatever the server advertises, and
`delegated-ipfs.dev` advertises 100 concurrent streams. Requests from a
service worker have no tab attached, so Chrome's per-tab request scheduler
does not throttle them at all.

Each of those lookups costs someguy a DHT walk, and a failed walk caches
nothing, so the next client repeats it. Bound the concurrency client side and
prefer the addresses someguy already returned.

## Related

- [peer-address-caching.md](peer-address-caching.md): where the addresses in a
  response come from, and the trade-offs behind them
- [environment-variables.md](environment-variables.md): `SOMEGUY_ROUTING_TIMEOUT`
  and the rest of the configuration
- [metrics.md](metrics.md): what to watch in production

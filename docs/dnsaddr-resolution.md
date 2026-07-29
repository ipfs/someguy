# DNSADDR Resolution

A provider record can carry a `/dnsaddr` address, which names a hostname whose
DNS TXT record holds the real addresses:

```console
$ dig +short TXT _dnsaddr.bitswap.example.net
"dnsaddr=/dns4/bitswap.example.net/tcp/3000/ws/p2p/Qma8ddFEQWEU8ijWvdxXm3nxU7oHsRtCykAaVz8WUYhiKn"
```

## Why it has to be resolved before filtering

`filter-addrs` matches on the protocol components present in an address. A
`/dnsaddr` has exactly one component, `dnsaddr`, and no transport, so the filter
can neither match it nor exclude it. That breaks in both directions:

| request | unresolved `/dnsaddr` | correct answer |
| --- | --- | --- |
| `?filter-addrs=ws` on a peer whose TXT record is `ws` | dropped | kept |
| `?filter-addrs=!quic-v1` on a peer whose TXT record is `quic-v1` | kept | dropped |

A client cannot detect either case. It asked for peers it can dial and got a
peer it cannot, or it asked to exclude a transport and got it anyway.

When the request filters, resolving must **replace** the `/dnsaddr` rather than
sit alongside it. Keeping both leaves the second row broken, because the
surviving `/dnsaddr` does not match the excluded transport and so keeps the
record alive.

## When someguy resolves

Controlled by
[`SOMEGUY_DNSADDR_RESOLUTION`](environment-variables.md#someguy_dnsaddr_resolution),
which defaults to `append`. Each mode is named after what a request without a
filter does with the `/dnsaddr`:

| mode | request sends `filter-addrs` | request sends no filter |
| --- | --- | --- |
| `append` (default) | replaced by the resolved addresses | resolved addresses added, `/dnsaddr` kept |
| `replace` | replaced by the resolved addresses | replaced by the resolved addresses |
| `filtered` | replaced by the resolved addresses | left alone, no lookup |
| `never` | left alone, no lookup | left alone, no lookup |

A filtered request is replaced in every resolving mode, because only a
filtered request can be skewed by a surviving `/dnsaddr`: a filter cannot
match one, so keeping it would hand back a record the client asked to exclude.
Nothing filters an unfiltered request, so the default returns both: the client
can dial the resolved addresses now and re-resolve the `/dnsaddr` later, when
the operator has changed it.

That matters because `/dnsaddr` is an indirection on purpose. An operator uses
it to change their address set without republishing provider records, and a
resolved address is a snapshot from when someguy answered. Replacing it is
worth it for a filtered request, which has already said it only wants
addresses it can use. Set `replace` if you would rather every response carried
only resolved addresses: responses shrink and no client has to speak DNS, but
a client that holds a record longer than someguy's DNS cache TTL cannot
re-resolve the hostname from the response alone. Set `filtered` if you would
rather someguy did no DNS work for requests that did not ask for filtering.

One filter value is the exception: `dnsaddr` names a protocol component, so a
positive `?filter-addrs=dnsaddr` does match a `/dnsaddr`, and the client
sending it is asking for the indirections themselves. Then someguy keeps the
`/dnsaddr` alongside whatever it resolves. A `/dnsaddr` naming a different
peer is still dropped, a check that costs no lookup. A negative `!dnsaddr`
replaces as usual, which is exactly the exclusion it asks for.

## Bounds

Provider records are published by anyone, so the hostnames someguy is asked to
look up are attacker-influenced. Resolution is bounded on five axes:

- **Per request.** One request triggers at most `MaxDNSAddrLookupsPerRequest`
  DNS lookups; names answered from the cache are free. Past the cap the address
  is passed through unresolved, which is the behavior from before this existed.
  Without this, one cheap request could name thousands of hostnames and make
  someguy a relay for a DNS flood.
- **Per record.** Resolution adds at most `MaxDNSAddrResolvedPerRecord`
  addresses to one record, the same bound go-libp2p puts on its dial path, so a
  record fanning out through nested `/dnsaddr` cannot balloon the response. A
  `/dnsaddr` whose expansion was cut short by any of these bounds keeps its
  original alongside whatever fit, so the client retains the indirection.
- **Per lookup.** Each TXT lookup has its own short timeout. Resolution happens
  while the response streams, so this timeout is the delay one unreachable
  nameserver adds to the first byte the client sees, and together with the
  per-request lookup cap it bounds the total delay resolution can add to one
  response. The lookup runs detached from the request that triggered it and is
  shared with every request waiting on the same name: a client that disconnects
  mid-lookup cannot cache its cancellation as a failure, and a popular name is
  queried once, not once per waiting request.
- **Recursion and breadth.** A `/dnsaddr` may resolve to another `/dnsaddr`.
  someguy follows at most `DNSAddrRecursionLimit` hops, matching go-libp2p's
  dial path, and threads a per-record output limit through the recursion. Depth
  alone is not enough: a TXT record that lists itself expands as
  `fan^depth`, and re-entering an already-cached name costs no lookup, so a
  limit applied to the finished result would still let one request build
  millions of addresses first.
- **Caching.** Resolved and failed lookups are both cached, failures for
  longer, so a repeated or dead hostname is not looked up again on every
  request. A lookup that merely timed out is retried sooner than one that
  failed outright. madns discards the DNS TTL, so these are fixed values rather
  than the record's own. Hostname case and a `/p2p` suffix do not split the
  cache: one name has one entry.

Three more rules apply. Addresses whose `/p2p` component names a different peer
are discarded, because a TXT record can list addresses for several peers and
someguy is resolving on behalf of one. The private-address filter runs again
over the result, because `manet` classifies `/dnsaddr/anything` as public while
the addresses behind it may be private. And only a bare `/dnsaddr/<host>` is
looked up at all: madns matches published entries against whatever follows the
host, real entries end in `/p2p/<id>`, so any other shape can only resolve to
nothing and is passed through without spending a lookup.

Cache keys normalize the hostname and nothing else, over ASCII only. Lowering a
whole multiaddr would fold case-sensitive components onto one key, and Unicode
case mapping folds a few non-ASCII runes onto ASCII, which would let one record
cache a failure under another name's key.

If any part of an expansion is missing, someguy keeps the original `/dnsaddr`:
a failed lookup, a request out of lookup budget or already gone, a truncated
expansion. The client keeps the indirection the missing addresses live behind,
and a DNS outage degrades to the old behavior instead of dropping providers.

## Address order

Within a record, addresses are sorted by how directly a client can dial them:
IP addresses first, then DNS names, then `/dnsaddr` indirections, then
everything else, with `/p2p-circuit` relay addresses last. A client that dials
in listed order tries the cheapest route first and falls back to a relay only
as a last resort.

This order applies to every record someguy serves while resolution is enabled.
With `SOMEGUY_DNSADDR_RESOLUTION=never`, only records from the DHT keep it
(they pass the same sort while being sanitized); records from delegated
routers are returned as received.

## What this does not fix

Resolution makes the filter correct. It does not make an address usable. A TXT
record advertising `/ws` rather than `/tls/ws` still cannot be dialed from an
HTTPS page, so a browser client can correctly receive a provider it still cannot
reach. That is for the provider operator to fix.

## Related

- [ipfs/specs#542](https://github.com/ipfs/specs/issues/542): the IPIP proposing
  this behavior for all Delegated Routing implementations
- [environment-variables.md](environment-variables.md#someguy_dnsaddr_resolution)
- [metrics.md](metrics.md)

# Someguy Environment Variables

The environment variables below override `someguy`'s built-in defaults.

- [Configuration](#configuration)
  - [`SOMEGUY_LISTEN_ADDRESS`](#someguy_listen_address)
  - [`SOMEGUY_DHT`](#someguy_dht)
  - [`SOMEGUY_CACHED_ADDR_BOOK`](#someguy_cached_addr_book)
  - [`SOMEGUY_CACHED_ADDR_BOOK_RECENT_TTL`](#someguy_cached_addr_book_recent_ttl)
  - [`SOMEGUY_CACHED_ADDR_BOOK_ACTIVE_PROBING`](#someguy_cached_addr_book_active_probing)
  - [`SOMEGUY_CACHED_ADDR_BOOK_STALE_PROBING`](#someguy_cached_addr_book_stale_probing)
  - [`SOMEGUY_PROVIDER_ENDPOINTS`](#someguy_provider_endpoints)
  - [`SOMEGUY_PEER_ENDPOINTS`](#someguy_peer_endpoints)
  - [`SOMEGUY_IPNS_ENDPOINTS`](#someguy_ipns_endpoints)
  - [`SOMEGUY_AUTOCONF`](#someguy_autoconf)
  - [`SOMEGUY_AUTOCONF_URL`](#someguy_autoconf_url)
  - [`SOMEGUY_AUTOCONF_REFRESH`](#someguy_autoconf_refresh)
  - [`SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS`](#someguy_http_block_provider_endpoints)
  - [`SOMEGUY_HTTP_BLOCK_PROVIDER_PEERIDS`](#someguy_http_block_provider_peerids)
  - [`SOMEGUY_LIBP2P_LISTEN_ADDRS`](#someguy_libp2p_listen_addrs)
  - [`SOMEGUY_LIBP2P_CONNMGR_LOW`](#someguy_libp2p_connmgr_low)
  - [`SOMEGUY_LIBP2P_CONNMGR_HIGH`](#someguy_libp2p_connmgr_high)
  - [`SOMEGUY_LIBP2P_CONNMGR_GRACE_PERIOD`](#someguy_libp2p_connmgr_grace_period)
  - [`SOMEGUY_LIBP2P_MAX_MEMORY`](#someguy_libp2p_max_memory)
  - [`SOMEGUY_LIBP2P_MAX_FD`](#someguy_libp2p_max_fd)
- [Logging](#logging)
  - [`GOLOG_LOG_LEVEL`](#golog_log_level)
  - [`GOLOG_LOG_FMT`](#golog_log_fmt)
  - [`GOLOG_FILE`](#golog_file)
  - [`GOLOG_TRACING_FILE`](#golog_tracing_file)
- [Tracing](#tracing)
  - [`SOMEGUY_TRACING_AUTH`](#someguy_tracing_auth)
  - [`SOMEGUY_SAMPLING_FRACTION`](#someguy_sampling_fraction)

## Configuration

### `SOMEGUY_LISTEN_ADDRESS`

The address to listen on.

Default: `127.0.0.1:8190`

### `SOMEGUY_DHT`

Controls DHT client mode: `standard`, `accelerated`, `disabled`

Default: `accelerated`

### `SOMEGUY_CACHED_ADDR_BOOK`

Enables the cached address book. When disabled, Someguy omits cached addresses from `FindProviders` results for peers lacking multiaddrs.

Default: `true`

### `SOMEGUY_CACHED_ADDR_BOOK_RECENT_TTL`

TTL for recently connected peers' multiaddrs in the cached address book. Applies only when `SOMEGUY_CACHED_ADDR_BOOK` is enabled.

Default: `48h`

### `SOMEGUY_CACHED_ADDR_BOOK_ACTIVE_PROBING`

Enables active probing of cached peers to keep their multiaddrs up to date. Applies only when `SOMEGUY_CACHED_ADDR_BOOK` is enabled.

Default: `true`

### `SOMEGUY_CACHED_ADDR_BOOK_STALE_PROBING`

Some faulty third-party DHT peers never expire old observed addresses for other peers. This causes peers with dynamic ports (e.g. UPnP on consumer routers) or changing IPs (roaming, ISP changes) to accumulate dead addresses over time, making them effectively unreachable.

When enabled, someguy detects first-encounter peers whose address sets look suspicious (multiple ports per IP, or more than 3 IPs per address family) and probes each unique address with an ephemeral libp2p handshake to filter out dead ones before returning results.

Only applies if `SOMEGUY_CACHED_ADDR_BOOK` is enabled.

Default: `true`

### `SOMEGUY_PROVIDER_ENDPOINTS`

Comma-separated list of [Delegated Routing V1](https://specs.ipfs.tech/routing/http-routing-v1/) endpoints for provider lookups.

Supports two URL formats:
- Base URL without path: `https://example.com`
- Full URL with path: `https://example.com/routing/v1/providers`

The `auto` placeholder (default) resolves to endpoints from the network configuration at [`SOMEGUY_AUTOCONF_URL`](#someguy_autoconf_url).

Default: `auto`

### `SOMEGUY_PEER_ENDPOINTS`

Comma-separated list of Delegated Routing V1 endpoints for peer routing.

URL formats: same as [`SOMEGUY_PROVIDER_ENDPOINTS`](#someguy_provider_endpoints) (use `/routing/v1/peers` path).

Default: `auto`

### `SOMEGUY_IPNS_ENDPOINTS`

Comma-separated list of Delegated Routing V1 endpoints for IPNS records.

URL formats: same as [`SOMEGUY_PROVIDER_ENDPOINTS`](#someguy_provider_endpoints) (use `/routing/v1/ipns` path).

Default: `auto`

### `SOMEGUY_AUTOCONF`

Enables automatic configuration (autoconf) of delegated routing endpoints and bootstrap peers.

When enabled, Someguy replaces the `auto` placeholder in endpoint configuration with network-recommended values fetched from the autoconf URL.

Default: `true`

### `SOMEGUY_AUTOCONF_URL`

URL to fetch autoconf data from. Defaults to the service that provides configuration for [IPFS Mainnet](https://docs.ipfs.tech/concepts/glossary/#mainnet).

Default: `https://conf.ipfs-mainnet.org/autoconf.json`

### `SOMEGUY_AUTOCONF_REFRESH`

How often to refresh the autoconf data. The configuration is cached and updated at this interval.

Default: `24h`

### `SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS`

Comma-separated list of [HTTP trustless gateways](https://specs.ipfs.tech/http-gateways/trustless-gateway/) that Someguy probes to synthesize provider records.

When a configured gateway responds with HTTP 200 to a `HEAD /ipfs/{cid}?format=raw` request, `FindProviders` returns a provider record that contains the matching PeerID from `SOMEGUY_HTTP_BLOCK_PROVIDER_PEERIDS` and the gateway endpoint as a multiaddr with the `/tls/http` suffix.

> [!IMPORTANT]
> When creating a synthetic `/routing/v1` for your gateway, and not a general-purpose routing endpoint, set `SOMEGUY_DHT=disabled` and `SOMEGUY_PROVIDER_ENDPOINTS=""` to disable default DHT and HTTP routers and exclusively use the explicitly defined `SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS`.

Default: none

### `SOMEGUY_HTTP_BLOCK_PROVIDER_PEERIDS`

Comma-separated list of [multibase-encoded peerIDs](https://github.com/libp2p/specs/blob/master/peer-ids/peer-ids.md#string-representation) that Someguy embeds in synthetic provider records for the HTTP endpoints in `SOMEGUY_HTTP_BLOCK_PROVIDER_ENDPOINTS`. The order of PeerIDs must match the order of endpoints.

If you configure provider endpoints without PeerIDs, Someguy generates synthetic PeerIDs automatically, deterministically derived from SHA-256 hashes of the endpoint URLs. These PeerIDs exist only for routing-system compatibility; HTTP trustless gateways never use them for cryptographic operations or peer authentication.

Default: none

### `SOMEGUY_LIBP2P_LISTEN_ADDRS`

Comma-separated libp2p listen multiaddresses. Someguy binds port `4004` on IPv4 and IPv6 across libp2p's default transports. To see the exact defaults built into this release, run `someguy start --help`.

### `SOMEGUY_LIBP2P_CONNMGR_LOW`

Minimum number of libp2p connections to keep.

Default: 100

### `SOMEGUY_LIBP2P_CONNMGR_HIGH`

Maximum number of libp2p connections to keep.

Default: 3000

### `SOMEGUY_LIBP2P_CONNMGR_GRACE_PERIOD`

Minimum libp2p connection TTL.

Default: 1m

### `SOMEGUY_LIBP2P_MAX_MEMORY`

Maximum memory to use for libp2p.

Default: 0 (85% of the system's available RAM)

### `SOMEGUY_LIBP2P_MAX_FD`

Maximum number of file descriptors used by libp2p node.

Default: 0 (50% of the process' limit)

## Logging

### `GOLOG_LOG_LEVEL`

Sets the log level globally or per subsystem. Levels:

* `debug`
* `info`
* `warn`
* `error`
* `dpanic`
* `panic`
* `fatal`

Specify per-subsystem levels as `subsystem=level`. Combine a global level with any number of per-subsystem levels by separating them with commas.

Default: `error`

Example:

```console
GOLOG_LOG_LEVEL="error,someguy=debug" someguy
```

### `GOLOG_LOG_FMT`

Sets the log message format. Supported values:

- `color`: human-readable, colorized (ANSI) output
- `nocolor`: human-readable, plain-text output
- `json`: structured JSON

For example, to log structured JSON (for easier parsing):

```bash
export GOLOG_LOG_FMT="json"
```
The logging format defaults to `color` when the output is a terminal, and
`nocolor` otherwise.

### `GOLOG_FILE`

Writes logs to the given file. Defaults to stderr.

### `GOLOG_TRACING_FILE`

Writes tracing events to the given file. Tracing is disabled by default.

Warning: tracing affects performance.

## Tracing

See [tracing.md](tracing.md).

### `SOMEGUY_TRACING_AUTH`

Setting a non-empty value enables on-demand per-request tracing.

To honor a `Traceparent` or `Tracestate` header, Someguy requires the request to carry an `Authorization` header whose value matches `SOMEGUY_TRACING_AUTH`.

### `SOMEGUY_SAMPLING_FRACTION`

Fraction of routing requests to sample (0 to 1). Applied independently of `Traceparent`-based sampling.

Default: `0`

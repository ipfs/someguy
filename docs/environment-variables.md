# Someguy Environment Variables

`someguy` ships with some implicit defaults that can be adjusted via env variables below.

- [Configuration](#configuration)
  - [`SOMEGUY_LISTEN_ADDRESS`](#someguy_listen_address)
  - [`SOMEGUY_ACCELERATED_DHT`](#someguy_accelerated_dht)
  - [`SOMEGUY_PROVIDER_ENDPOINTS`](#someguy_provider_endpoints)
  - [`SOMEGUY_PEER_ENDPOINTS`](#someguy_peer_endpoints)
  - [`SOMEGUY_IPNS_ENDPOINTS`](#someguy_ipns_endpoints)
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
  - [`SOMEGUY_SAMPLING_FRACTION`](#someguy_sampling_fraction)
  - [`SOMEGUY_TRACING_AUTH`](#someguy_tracing_auth)

## Configuration

### `SOMEGUY_LISTEN_ADDRESS`

The address to listen on.

Default: `127.0.0.1:8190`

### `SOMEGUY_ACCELERATED_DHT`

Whether or not the Accelerated DHT is enabled or not.

Default: `true`

### `SOMEGUY_PROVIDER_ENDPOINTS`

Comma-separated list of other Delegated Routing V1 endpoints to proxy provider requests to.

Default: `https://cid.contact`

### `SOMEGUY_PEER_ENDPOINTS`

Comma-separated list of other Delegated Routing V1 endpoints to proxy peer requests to.

Default: none

### `SOMEGUY_IPNS_ENDPOINTS`

Comma-separated list of other Delegated Routing V1 endpoints to proxy IPNS requests to.

Default: none

### `SOMEGUY_LIBP2P_LISTEN_ADDRS`

Multiaddresses for libp2p host to listen on (comma-separated).

Default: `someguy start --help`

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

Specifies the log-level, both globally and on a per-subsystem basis. Level can
be one of:

* `debug`
* `info`
* `warn`
* `error`
* `dpanic`
* `panic`
* `fatal`

Per-subsystem levels can be specified with `subsystem=level`.  One global level
and one or more per-subsystem levels can be specified by separating them with
commas.

Default: `error`

Example:

```console
GOLOG_LOG_LEVEL="error,someguy=debug" someguy
```

### `GOLOG_LOG_FMT`

Specifies the log message format.  It supports the following values:

- `color` -- human readable, colorized (ANSI) output
- `nocolor` -- human readable, plain-text output.
- `json` -- structured JSON.

For example, to log structured JSON (for easier parsing):

```bash
export GOLOG_LOG_FMT="json"
```
The logging format defaults to `color` when the output is a terminal, and
`nocolor` otherwise.

### `GOLOG_FILE`

Sets the file to which the logs are saved. By default, they are printed to the standard error output.

### `GOLOG_TRACING_FILE`

Sets the file to which the tracing events are sent. By default, tracing is disabled.

Warning: Enabling tracing will likely affect performance.

## Tracing

See [tracing.md](tracing.md).

### `SOMEGUY_TRACING_AUTH`

Optional, setting to non-empty value enables on-demand tracing per-request.

The ability to pass `Traceparent` or `Tracestate` headers is guarded by an
`Authorization` header. The value of the `Authorization` header should match
the value in the `SOMEGUY_TRACING_AUTH` environment variable.

### `SOMEGUY_SAMPLING_FRACTION`

Optional, set to 0 by default.

The fraction (between 0 and 1) of requests that should be sampled.
This is calculated independently of any Traceparent based sampling.

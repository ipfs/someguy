## Someguy metrics

Someguy exposes a Prometheus endpoint at `http://127.0.0.1:8190/debug/metrics/prometheus` by default.

The endpoint includes the default [Prometheus Go client metrics](https://prometheus.io/docs/guides/go-application/) plus the Someguy-specific metrics listed below.

### Delegated HTTP Routing (`/routing/v1`) server

`boxo/routing/http/server` (the `/routing/v1` handler) exports metrics with the `delegated_routing_server_` prefix:

- `delegated_routing_server_http_request_duration_seconds_[bucket|sum|count]{code,handler,method}`: histogram of HTTP request latency
- `delegated_routing_server_http_response_size_bytes_[bucket|sum|count]{code,handler,method}`: histogram of HTTP response size

### Delegated HTTP Routing (`/routing/v1`) client

When Someguy aggregates other `/routing/v1` endpoints, `boxo/routing/http/client` exports metrics with the `someguy_` prefix:

- `someguy_routing_http_client_latency_[bucket|sum|count]{code,error,host,operation}`: histogram of operation latency
- `someguy_routing_http_client_length_[bucket|sum|count]{host,operation}`: histogram of response collection size

### Someguy caches

- `someguy_cached_addr_book_probe_duration_seconds_[bucket|sum|count]`: histogram of peer-probing duration in seconds
- `someguy_cached_addr_book_probed_peers{result}`: counter of probed peers, labeled `online` or `offline`
- `someguy_cached_addr_book_peer_state_size`: gauge of peers currently tracked in peer state
- `someguy_cached_router_peer_addr_lookups{cache,origin}`: counter of peer address-info lookups per origin and cache state


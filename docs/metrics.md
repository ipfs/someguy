## someguy metrics

By default, a prometheus endpoint is exposed by someguy at `http://127.0.0.1:8190/debug/metrics/prometheus`.

It includes default [Prometheus Glient metrics](https://prometheus.io/docs/guides/go-application/) + someguy-specific listed below.

### Delegated HTTP Routing (`/routing/v1`) server

Metric from `boxo/routing/http/server` (used for `/routing/v1` handler) are exposed with `delegated_routing_server_` prefix:

- `delegated_routing_server_http_request_duration_seconds_[bucket|sum|count]{code,handler,method}` - histogram: the latency of the HTTP requests
- `delegated_routing_server_http_response_size_bytes_[bucket|sum|count]{code,handler,method}` - histogram: the size of the HTTP responses

### Delegated HTTP Routing (`/routing/v1`) client

If someguy is set up as an aggregating proxy for multiple other `/routing/v1` endpoints,
metrics from `boxo/routing/http/client` are exposed with `someguy_` prefix:

- `someguy_routing_http_client_latency_[bucket|sum|count]{code,error,host,operation}` - Histogram: the latency of operations by the routing HTTP client:
- `someguy_routing_http_client_length_[bucket|sum|count]{host,operation}` - Histogram: the number of elements in a response collection

### Someguy Caches

- `someguy_cached_addr_book_probe_duration_seconds_[bucket|sum|count]` - Histogram: duration of peer probing operations in seconds
- `someguy_cached_router_peer_addr_lookups{cache,origin}` - Counter: number of peer addr info lookups per origin and cache state
- `someguy_cached_addr_book_peer_state_size` - Gauge: number of peers object currently in the peer state


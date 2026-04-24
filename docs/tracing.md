## Tracing

Someguy follows the [Open Telemetry] specifications across the stack. Where possible, it reads the environment variables defined in the [OpenTelemetry Environment Variable Specification]. Tracing builds on [Boxo Tracing].

> [!NOTE]
> Unlike the [general tracing in boxo][Boxo Tracing], Someguy traces only inbound HTTP routing requests, not background processes.

### Fractional sampling

To sample a fraction of requests, set [`SOMEGUY_SAMPLING_FRACTION`](environment-variables.md#someguy_sampling_fraction) to a value between `0` and `1`.

### Per-request

Per-request tracing activates when [`SOMEGUY_TRACING_AUTH`](environment-variables.md#someguy_tracing_auth) is set and the request carries both an `Authorization` header matching that value and a valid `Traceparent` header.

### Per-request tracing example

```console
$ export SOMEGUY_TRACING_AUTH=CHANGEME-tracing-auth-secret # use value from Someguy config
$ export CID=bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi
$ curl -H "Authorization: $SOMEGUY_TRACING_AUTH" -H "Traceparent: 00-$(openssl rand -hex 16)-00$(openssl rand -hex 7)-01" http://127.0.0.1:8190/routing/v1/providers/$CID -v -o /dev/null
...
> Authorization: CHANGEME-tracing-auth-secret
> Traceparent: 00-b617dc6b6e302ccbabe0115eac80320b-00033792c7de8fc6-01
...
```

Search for `trace_id = b617dc6b6e302ccbabe0115eac80320b` to locate the trace.

[Boxo Tracing]: https://github.com/ipfs/boxo/blob/main/docs/tracing.md
[Open Telemetry]: https://opentelemetry.io/
[OpenTelemetry Environment Variable Specification]: https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/configuration/sdk-environment-variables.md
[Trace Context]: https://www.w3.org/TR/trace-context

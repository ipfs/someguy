## Tracing

Tracing across the stack follows, as much as possible, the [Open Telemetry]
specifications. Configuration environment variables are specified in the
[OpenTelemetry Environment Variable Specification] where possible. The
[Boxo Tracing] documentation is the basis for tracing here.

> [!NOTE]
> A major distinction from the more [general tracing enabled in boxo][Boxo Tracing] is that when
> tracing is enabled it is restricted to flows through HTTP Gateway requests, rather
> than also included background processes.

### Fractional Sampling

To sample a % of requests set [`SOMEGUY_SAMPLING_FRACTION`](environment-variables.md#someguy_sampling_fraction) to a value between `0` and `1`.

### Per Request

Per-request tracing is possible when a non-empty [`SOMEGUY_TRACING_AUTH`](environment-variables.md#someguy_tracing_auth) is set in Someguy and when there are both valid
[Authorization](headers.md#authorization) and [`Traceparent`](headers.md#traceparent) HTTP headers passed in the request.

### Per-request tracing example:

```console
$ export SOMEGUY_TRACING_AUTH=CHANGEME-tracing-auth-secret # use value from Someguy config
$ export CID=bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi
$ curl -H "Authorization: $SOMEGUY_TRACING_AUTH" -H "Traceparent: 00-$(openssl rand -hex 16)-00$(openssl rand -hex 7)-01" http://127.0.0.1:8090/routing/v1/providers/$CID -v -o /dev/null
...
> Authorization: CHANGEME-tracing-auth-secret
> Traceparent: 00-b617dc6b6e302ccbabe0115eac80320b-00033792c7de8fc6-01
...
````

Now you can search for `trace_id = b617dc6b6e302ccbabe0115eac80320b` to find the trace.

[Boxo Tracing]: https://github.com/ipfs/boxo/blob/main/docs/tracing.md
[Open Telemetry]: https://opentelemetry.io/
[OpenTelemetry Environment Variable Specification]: https://github.com/open-telemetry/opentelemetry-specification/blob/main/specification/configuration/sdk-environment-variables.md
[Trace Context]: https://www.w3.org/TR/trace-context

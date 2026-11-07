# Local observability kit

Core exports Prometheus at `/metrics`. Worker exports when `AUTHOVA_WORKER_METRICS_LISTEN` is set. This kit targets the isolated localhost demo on 18180/18181, never the existing 18080 demo. It does not change any production deployment or install an Agent.

```sh
python3 scripts/passkey-demo.py start
docker --context desktop-linux compose -f deploy/observability/compose.yaml up -d
```

Open Prometheus at http://localhost:19091. The images are pinned to the locally inspected official image digests, with Collector 0.162.0. To export real runtime traces to this local Collector, set `AUTHOVA_TRACE_OTLP_ENDPOINT=http://localhost:14318/v1/traces` and `AUTHOVA_TRACE_SAMPLE_RATIO=0.01` in the chosen Core/Worker environment before starting them. The helper supports these two explicit overrides. Existing processes must be stopped and rebuilt through their owned helper; do not kill unrelated processes.

Import `grafana-dashboard.json` into your existing Grafana and select its Prometheus datasource. Set deployment to `authova-local-passkey`. The JSON is an import artifact, not evidence of a rendered Grafana dashboard. No Grafana server or account is created by this kit.

## Semantics

- HTTP histograms use finite route classes, method and status class. Request IDs, App/user IDs, URL parameters and credential data are never metric labels.
- KDF and DB histograms include admitted work and the actual operation. Admission errors are counted separately. They do not measure just the cryptographic primitive or query execution.
- Delivery processing spans provider call and outbox acknowledgement. Outcomes distinguish accepted, retry, expired, acknowledgement failure and cancellation. Provider acceptance is not proof that a human received a code.
- Outbox values are snapshots of the shared queue. Use `max`, not `sum`, across Core/Worker replicas of the same database. Counts are capped at 10000 to bound database work. `authova_outbox_snapshot_capped{state=...}=1` means the count is a lower bound.
- Oldest due time means overdue `available_at` of a currently pending row. Backoff changes it; it is not original message age. Check snapshot_valid and last_success before interpreting zero.
- Rejection counters distinguish App concurrency, shared process capacity and shared rate/challenge quota. Use App scoped sanitized security logs to attribute noisy traffic.
- Traces include HTTP route/status, generated request ID, opaque App ID, DB mode and sanitized delivery IDs. No raw SQL, request body, response body, cookies, Authorization headers, provider tokens or destinations are recorded.
- The initial implementation creates local trace roots and child spans within Core/Worker, including provider HTTP work. It does not claim automatic BFF-to-Core or third-party distributed trace propagation. Public inbound trace headers are not trusted to select sampling or identity.
- Tracing is optional. Exporter is asynchronous with a 256-span queue, batches of 64 and a 2-second export timeout; queue overflow may drop spans. It does not block authentication. Audit persistence is separate from sampled telemetry.

## Production configuration

`AUTHOVA_TRACE_OTLP_ENDPOINT` must be an HTTPS collector URL in production, with no userinfo, query or fragment. `AUTHOVA_TRACE_SAMPLE_RATIO` is a finite 0..1 number, default 0.01. Use a private collector/reverse proxy for deployment credentials and TLS. Standard OTLP headers may be supplied by the deployment environment; never put bearer secrets in the endpoint URL.

Keep `/metrics` and Worker metrics on private networking; the public ingress must deny `/metrics`. Protect telemetry storage and dashboards with deployment/App access controls. The example collector is for local inspection and uses the basic debug exporter. Configure a real authenticated trace backend before production; memory_limiter and batch are included according to the [Collector configuration guidance](https://opentelemetry.io/docs/collector/configuration/).

The alert budgets are initial operational defaults, not certified SLOs: token p95 500ms, 1% server-error ratio, overdue delivery 60 seconds and sustained DB occupancy 90%. Tune them from deployment requirements and load evidence. Alertmanager routing, paging, dependency exporters, telemetry retention, remote storage HA and real collector outage drills remain deployment responsibilities.

## Verification

```sh
docker --context desktop-linux run --rm --network none \
  --entrypoint /bin/promtool -v "$PWD/deploy/observability:/etc/authova:ro" \
  -w /etc/authova prom/prometheus@sha256:efd719c99d83b060d9daefdcf00360461adf279f45ef5391f8d111892118753e \
  test rules alerts.test.yaml
```

Go observability tests check real protobuf OTLP export, failure status, omission of sensitive fields, exporter unavailability, Prometheus text/gzip parsing and outbox poll shutdown/freshness. The Collector configuration is also validated using its pinned binary. These are local functional checks, not a production monitoring acceptance or capacity certification.

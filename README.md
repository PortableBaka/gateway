# gateway

A reverse-proxy API gateway written in Go, built as a hands-on tour through the pieces a production edge service actually needs: load balancing, health checking, circuit breaking, retries, rate limiting, auth, TLS, metrics, tracing, structured logging, graceful shutdown, and config hot-reload — no framework, just the standard library plus a handful of well-chosen dependencies.

## Features

- **Reverse proxy** over `net/http/httputil.ReverseProxy`, one handler per configured route.
- **Load balancing** — four strategies: `round_robin`, `weighted` (smooth WRR, the nginx algorithm), `least_load` (fewest in-flight requests), `random`.
- **Health checking** — active (periodic probes) and passive (live request failures), both feeding the same per-upstream state with flap-resistant thresholds.
- **Circuit breaker** per upstream — closed / open / half-open, with a bounded single trial request during recovery.
- **Retries** with exponential backoff, restricted to idempotent methods (GET/HEAD/OPTIONS).
- **Rate limiting** — per-client token bucket, keyed by IP.
- **Auth** — API key (`X-API-Key`), constant-time comparison, opt-in per route.
- **TLS** termination.
- **Prometheus metrics** (`/metrics`) — request counts, latency histograms, upstream health gauges.
- **Distributed tracing** (OpenTelemetry) — spans for both the inbound request and each outbound upstream call, correlated with logs via `trace_id`.
- **Structured JSON logging** (`log/slog`) — one line per request, with `request_id` and (when tracing is on) `trace_id`.
- **Panic recovery** — a panicking handler returns a 500 instead of taking down the process.
- **Graceful shutdown** — in-flight requests finish before the process exits.
- **Config hot-reload** — `SIGHUP` reloads and atomically swaps the router with zero dropped in-flight requests; an invalid config on reload is rejected and the previous one keeps serving.
- **`pprof`** on a separate, opt-in debug port.

## Quick start

```sh
cp config.example.yaml config.yaml   # or just `make run`, which does this for you
go run ./cmd/gateway
```

`config.yaml` is your local copy (gitignored — edit it freely, including adding real secrets if you ever point this at something real). `config.example.yaml` is the checked-in template.

The example config expects two upstreams on `localhost:9001` and `localhost:9002`. For a quick throwaway backend:

```sh
python3 -m http.server 9001 &
python3 -m http.server 9002 &
```

Then:

```sh
curl http://localhost:8081/healthz
curl -H "X-API-Key: test-key-123" http://localhost:8081/users/
```

## Configuration

Config is YAML, loaded from `config.yaml` by default. Override the path with the `GATEWAY_CONFIG_PATH` environment variable. All fields have sane defaults — an empty `config.yaml` with just `routes` and `upstreams` is a valid minimal config.

### `server`

| Field | Default | Description |
|---|---|---|
| `addr` | `:8080` | Listen address. |
| `read_timeout` / `write_timeout` | `10s` | Standard `http.Server` timeouts. |
| `shutdown_timeout` | `15s` | Max time to wait for in-flight requests during graceful shutdown. |
| `api_keys` | *(none)* | Valid `X-API-Key` values, checked gateway-wide for any route with `auth.required: true`. |
| `tls.cert_file` / `tls.key_file` | *(none)* | Both must be set together, or both left empty (plain HTTP). |
| `debug_addr` | *(disabled)* | If set (e.g. `:6060`), serves `net/http/pprof` on this separate port — never the public one. |
| `tracing.enabled` | `false` | Opt-in OpenTelemetry tracing. |
| `tracing.endpoint` | `localhost:4318` | `"stdout"` for zero-infrastructure local verification, or an OTLP/HTTP endpoint (e.g. Jaeger). |
| `tracing.service_name` | `gateway` | Reported in exported spans. |
| `rate_limit.requests_per_second` | `10` | Per-client (by IP) steady-state rate. |
| `rate_limit.burst` | `20` | Per-client burst allowance. |
| `rate_limit.cleanup_interval` | `5m` | How often idle client entries are swept. |
| `rate_limit.max_idle` | `10m` | How long a client can be idle before its entry is evicted. |

### `routes[]`

| Field | Default | Description |
|---|---|---|
| `path_prefix` | *(required)* | Requests under this prefix (and the bare prefix itself) route here. |
| `strategy` | `round_robin` | `round_robin` \| `weighted` \| `least_load` \| `random`. |
| `timeout` | `10s` | Per-request deadline, covering all retry attempts combined. |
| `upstreams[].url` / `upstreams[].weight` | weight `1` | Backend targets; weight only matters for the `weighted` strategy. |
| `auth.required` | `false` | Gate this route behind `server.api_keys`. |
| `retry.max_attempts` | `3` | Only idempotent methods (GET/HEAD/OPTIONS) actually retry. |
| `retry.base_backoff` | `50ms` | Doubles each attempt, capped at 500ms. |
| `health_check.path` | `/` | Active probe path. |
| `health_check.interval` | `5s` | Time between active probes. |
| `health_check.timeout` | `2s` | Per-probe timeout. |
| `health_check.healthy_threshold` / `unhealthy_threshold` | `2` / `2` | Consecutive successes/failures before flipping state (flap resistance). |
| `breaker.failure_threshold` | `5` | Consecutive failures before the circuit opens. |
| `breaker.cooldown` | `10s` | Time in the open state before a single half-open trial is allowed. |

## Endpoints

- `GET /healthz` — liveness, no auth.
- `GET /metrics` — Prometheus exposition format.
- `GET /debug/pprof/*` — only on `server.debug_addr`, if set; not reachable on the main port.
- Everything else routes through your configured `routes[]`.

## Running with Docker

```sh
docker compose up --build
```

Brings up the gateway plus two mock upstream containers (`mock-a`, `mock-b`). This uses `config.docker.yaml` (via `GATEWAY_CONFIG_PATH`), which points at the Compose service names rather than `localhost` — containers can't reach each other via `localhost`, that only resolves to the container itself.

```sh
curl http://localhost:8081/healthz
curl -H "X-API-Key: test-key-123" http://localhost:8081/users/
```

Or just the image:

```sh
make docker-build   # docker build -t gateway .
```

The image is a `CGO_ENABLED=0` static binary on `distroless/static:nonroot` — tens of MB, no shell, runs as a non-root user by default.

## Hot reload

Edit `config.yaml`, then:

```sh
kill -HUP <gateway-pid>
```

New requests immediately see the updated config; requests already in flight keep running against whichever handler generation they started on, so nothing gets dropped mid-request. An invalid config is rejected (logged, not applied) and the previous one keeps serving.

## Observability

- **Logs**: structured JSON on stdout, one line per request (`request_id`, method, path, status, duration, and `trace_id` when tracing is enabled).
- **Metrics**: `curl localhost:8081/metrics` — `gateway_http_requests_total`, `gateway_http_request_duration_seconds`, `gateway_upstream_healthy`.
- **Traces**: set `tracing.enabled: true` with `endpoint: "stdout"` to see spans printed locally with zero setup, or point `endpoint` at an OTLP/HTTP collector (e.g. `jaegertracing/all-in-one`) and view the trace tree — gateway span plus a child span per upstream call (one per retry attempt) — in its UI.
- **Profiling**: set `debug_addr`, then `go tool pprof http://localhost:6060/debug/pprof/heap` (or `goroutine`, `profile`, etc.).

## Testing

```sh
make test          # go test ./... -race -cover
make vet           # go vet ./...
```

CI (`.github/workflows/ci.yml`) runs vet, race-enabled tests, a build, and a Docker build on every push/PR.

## Project structure

```
cmd/gateway/          entrypoint: config load, dependency wiring, lifecycle
internal/
  balancer/            load-balancing strategies
  breaker/             circuit breaker
  config/              YAML config schema, defaults, validation
  gateway/             builds the routed http.Handler from config
  health/              active + passive upstream health checking
  metrics/             Prometheus instrumentation
  middleware/          request ID, logging, recovery, rate limit, auth
  proxy/               per-route reverse proxy: balancing, retries, breaker, health wiring
  tracing/             OpenTelemetry setup
```

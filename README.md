# gateway

## Getting started

```
cp config.example.yaml config.yaml   # or just `make run`, which does this for you
go run ./cmd/gateway
```

`config.yaml` is gitignored — it's your local copy, edit it freely. `config.example.yaml` is the checked-in template.

For Docker Compose (gateway + two mock upstreams): `docker compose up --build`. That setup uses `config.docker.yaml` instead, which points at the Compose service names rather than `localhost`.

# GophProfile

Avatar upload/serve service. Users upload a photo once via REST; external platforms fetch the avatar and pre-generated thumbnails by ID. Built on Go + chi, PostgreSQL (metadata), MinIO/S3 (files), and RabbitMQ (async thumbnail generation).

## Docs

- [Architecture & K8s topology](docs/architecture.md) — Mermaid diagrams of the runtime, sequence flows, code layering
- [OpenAPI 3.0 spec](docs/openapi.yaml) — paste into [editor.swagger.io](https://editor.swagger.io) for interactive docs
- [Raw Kubernetes manifests](deploy/k8s/README.md)
- [Helm chart](deploy/helm/gophprofile/README.md)

## Quickstart

Three supported deploy paths. Pick one.

### 1. Docker Compose (local dev)

Brings up the app + Postgres + MinIO + RabbitMQ + the full observability stack (Prometheus, Jaeger, Grafana, Loki, OTel Collector, Alertmanager):

```sh
docker-compose up -d --build
docker-compose down
```

The server listens on `https://localhost:8080` with the dev cert under [`crt/`](crt/).

### 2. Run Go binaries directly

Useful when iterating on code. Bring up infra via Compose first (`docker-compose up -d postgres minio rabbitmq`), then:

```sh
# Server (TLS required — uses ListenAndServeTLS)
go run cmd/server/main.go \
  -d="host=localhost user=postgres_user password=postgres_password dbname=postgres_db sslmode=disable" \
  -c="crt/server.crt" -k="crt/server.key" \
  -minio-endpoint="localhost:9002" -minio-access-key="minio_user" \
  -minio-secret-key="minio_password" -minio-bucket="goph-profile"

# Worker
go run cmd/worker/main.go \
  -d="host=localhost user=postgres_user password=postgres_password dbname=postgres_db sslmode=disable" \
  -rabbit-url="amqp://guest:guest@localhost:5672/" \
  -minio-endpoint="localhost:9002" -minio-access-key="minio_user" \
  -minio-secret-key="minio_password" -minio-bucket="goph-profile"
```

Migrations apply automatically on server startup from `./migrations`. Run from the repo root so the relative path resolves.

### 3. Kubernetes (Helm)

End-to-end install on Rancher Desktop:

```sh
# Build images into Rancher Desktop's runtime
docker context use rancher-desktop
export DOCKER_API_VERSION=1.43
docker build --build-arg CMD=server  -t docker.io/library/gophprofile-server:dev  .
docker build --build-arg CMD=worker  -t docker.io/library/gophprofile-worker:dev  .
docker build --build-arg CMD=migrate -t docker.io/library/gophprofile-migrate:dev .

# Infra (Postgres, MinIO, RabbitMQ) — raw manifests
kubectl apply -f deploy/k8s/00-namespace.yaml
kubectl apply -f deploy/k8s/infra/

# App
helm install gophprofile deploy/helm/gophprofile -n gophprofile --wait

curl -i http://gophprofile.localhost/health
```

For the raw-manifest variant (no Helm) and production overlay, see the [K8s README](deploy/k8s/README.md) and [chart README](deploy/helm/gophprofile/README.md).

## API

Full spec at [`docs/openapi.yaml`](docs/openapi.yaml). All requests need the `X-User-ID` header (regex `^[a-zA-Z0-9._\-@:]+$`, ≤255 chars). Cross-user access is rejected with `403`.

| Method | Path | Purpose |
|---|---|---|
| `GET`    | `/health`                            | Liveness + dependency check |
| `POST`   | `/api/v1/avatars`                    | Upload a JPEG/PNG/WebP, ≤10 MiB |
| `GET`    | `/api/v1/avatars/{avatar_id}`        | Fetch metadata + thumbnail keys |
| `DELETE` | `/api/v1/avatars/{avatar_id}`        | Soft-delete (worker cleans S3) |
| `GET`    | `/api/v1/users/{user_id}/avatars`    | List a user's avatars |

### Smoke tests

Direct against the Go binary (TLS):

```sh
# Happy path
curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" \
  -F "image=@test-data/example1.jpg" https://localhost:8080/api/v1/avatars

# Through the K8s ingress (Traefik on Rancher Desktop)
curl -v -H "X-User-ID: test-user-1" \
  -F "image=@test-data/example1.jpg" http://gophprofile.localhost/api/v1/avatars

# Delete
curl -X DELETE -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" \
  https://localhost:8080/api/v1/avatars/<avatar_id>
```

Negative cases (each maps to a documented error):

```sh
# 400 UserIDHeaderNotFound
curl -v --cacert crt/ca.crt -F "image=@test-data/example1.jpg" \
  https://localhost:8080/api/v1/avatars

# 400 MissingFileField (wrong form field name)
curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" \
  -F "file=@test-data/example1.jpg" https://localhost:8080/api/v1/avatars

# 400 UnsupportedMediaType (magic-byte check, not Content-Type sniffing)
echo "not an image" > /tmp/fake.png && curl -v --cacert crt/ca.crt \
  -H "X-User-ID: test-user-1" -F "image=@/tmp/fake.png" \
  https://localhost:8080/api/v1/avatars

# 413 FileTooLarge
dd if=/dev/urandom of=/tmp/big.bin bs=1m count=15 && curl -v --cacert crt/ca.crt \
  -H "X-User-ID: test-user-1" -F "image=@/tmp/big.bin" \
  https://localhost:8080/api/v1/avatars

# 400 ExpectedMultipartFormData
curl -v --cacert crt/ca.crt -H "X-User-ID: test-user-1" \
  -H "Content-Type: application/json" -d '{"foo":"bar"}' \
  https://localhost:8080/api/v1/avatars
```

## Tests

```sh
# Unit + integration (skips generated mocks, model, cmd entrypoints)
go test $(go list ./... | grep -v -E '/mocks|/model$|/cmd/') \
  -coverprofile=coverage.out && go tool cover -func=coverage.out | grep total

# Single package
go test ./internal/storage -run TestName -v
```

Storage and filestorage interfaces have `//go:generate mockery` directives — regenerate with `go generate ./...` after changing them.

## Layout

```
cmd/
├── server/      HTTP API + RabbitMQ publisher
├── worker/      thumbnail consumer
└── migrate/     standalone migration runner (Helm pre-install hook)
internal/
├── services/    HTTP handlers + chi routing (server.go, handlers.go)
├── storage/     PostgresStorage (golang-migrate, otelsql)
├── filestorage/ MinioStorage (Upload/Download/Delete)
├── broker/      RabbitMQ Publisher + Consumer
├── observability/  OTel + Prometheus init, custom metrics
├── config/      flags + env parsing
└── errs/        canonical error values returned to clients
deploy/
├── k8s/         raw manifests
└── helm/gophprofile/  Helm chart (templates + per-env values)
docs/            architecture diagram + OpenAPI spec
migrations/      golang-migrate SQL files
crt/             dev TLS material
```

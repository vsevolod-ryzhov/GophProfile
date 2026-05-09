# GophProfile — Architecture

A high-level view of the runtime, with Kubernetes components called out explicitly. Diagrams are [Mermaid](https://mermaid.js.org/)

## System overview (Kubernetes)

```mermaid
flowchart LR
    subgraph "Internet"
        Client["Client / external platform<br/>HTTP"]
    end

    subgraph kube_system["Namespace: kube-system"]
        Traefik["Traefik IngressClass<br/>(or ingress-nginx)"]
    end

    subgraph monitoring["Namespace: monitoring"]
        Prom["Prometheus<br/>(kube-prometheus-stack)"]
        Otel["OTel Collector<br/>OTLP :4317/:4318"]
        Jaeger["Jaeger / Loki / Grafana<br/>Alertmanager"]
    end

    subgraph gp_ns["Namespace: gophprofile"]
        direction TB

        subgraph app["Application (Helm release)"]
            Ingress["Ingress<br/>gophprofile.localhost"]
            SvcSrv["Service: gophprofile-server<br/>ClusterIP :80 → :8080<br/>:9464 metrics"]
            SvcWrk["Service: gophprofile-worker<br/>Headless :9464"]
            Server["Deployment: gophprofile-server<br/>HPA 2–10, probes, RO rootfs<br/>UID 65532"]
            Worker["Deployment: gophprofile-worker<br/>HPA 1–5, RO rootfs<br/>UID 65532"]
            MigJob[("Job: gophprofile-migrate<br/>Helm pre-install/-upgrade hook")]
            CM["ConfigMap × 2"]
            Sec["Secret"]
            SA["ServiceAccount × 2<br/>(automount=false)"]
            HPA["HPA × 2"]
            NP["NetworkPolicy × 6<br/>default-deny + allow"]
            SM["ServiceMonitor × 2"]
        end

        subgraph infra["Infra (raw manifests)"]
            PG[("PostgreSQL<br/>:5432")]
            S3[("MinIO / S3<br/>:9000<br/>bucket: goph-profile")]
            MQ[("RabbitMQ<br/>:5672<br/>queues: uploads, deletes")]
        end
    end

    Client -->|HTTPS| Traefik
    Traefik -->|HTTP :80| Ingress
    Ingress --> SvcSrv
    SvcSrv -->|:8080| Server
    SvcWrk -.->|:9464 scrape target| Worker

    Server -->|SQL| PG
    Server -->|S3 PutObject<br/>GetObject<br/>DeleteObject| S3
    Server -->|publish<br/>uploads / deletes| MQ

    Worker -->|consume| MQ
    Worker -->|S3 Get/Put/Delete<br/>thumbnails| S3
    Worker -->|SQL update| PG

    MigJob -->|migrate.Up| PG

    Server -.->|OTLP traces+metrics| Otel
    Worker -.->|OTLP traces+metrics| Otel
    Prom -->|GET /metrics| SvcSrv
    Prom -->|GET /metrics| SvcWrk
    SM -. selects .-> SvcSrv
    SM -. selects .-> SvcWrk

    classDef ns fill:#f5f5f5,stroke:#666,stroke-width:1px;
    classDef helm fill:#e8f4fd,stroke:#1976d2;
    classDef raw fill:#fff3e0,stroke:#f57c00;
    classDef obs fill:#f3e5f5,stroke:#7b1fa2;
    class app helm
    class infra raw
    class monitoring obs
    class kube_system ns
```

### Legend

- **Solid arrows** — synchronous request/response (HTTP, SQL, S3, AMQP).
- **Dashed arrows** — out-of-band telemetry / scraping / selectors.
- **Cylinder shapes** — stateful components (DB, queue, object store).
- **Blue cluster** — packaged by the Helm chart at `deploy/helm/gophprofile/`.
- **Orange cluster** — local-only infra (raw manifests under `deploy/k8s/infra/`).
  Replace with a managed Postgres / S3 / RabbitMQ in production.
- **Purple cluster** — observability stack, lives in its own namespace.

## Avatar upload — sequence

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant Ing as Ingress (Traefik)
    participant S as gophprofile-server
    participant DB as PostgreSQL
    participant Obj as MinIO/S3
    participant MQ as RabbitMQ
    participant W as gophprofile-worker

    C->>Ing: POST /api/v1/avatars (multipart, X-User-ID)
    Ing->>S: HTTP :8080
    S->>S: validate magic bytes & MIME
    S->>DB: INSERT avatar (status=processing)
    S->>Obj: PutObject (original)
    S->>DB: UPDATE s3_key
    S->>MQ: publish uploads { avatar_id }
    S-->>C: 201 Created { id }

    Note over MQ,W: async thumbnail pipeline
    MQ->>W: deliver uploads
    W->>Obj: GetObject (original)
    W->>W: imaging Resize 100×100, 300×300
    W->>Obj: PutObject (thumbnails)
    W->>DB: UPDATE thumbnail_keys, status=ready
    W->>MQ: ack
```

## Layering (code-level)

```mermaid
flowchart TB
    subgraph cmd["cmd/"]
        srv_main["server/main.go"]
        wrk_main["worker/main.go"]
        mig_main["migrate/main.go<br/>(Helm hook)"]
    end

    subgraph svc["internal/services"]
        Server["Server + handlers<br/>chi router"]
    end

    subgraph io["I/O layer"]
        Storage["internal/storage<br/>PostgresStorage"]
        FileStore["internal/filestorage<br/>MinioStorage"]
        Broker["internal/broker<br/>Publisher / Consumer"]
    end

    subgraph cross["cross-cutting"]
        Obs["internal/observability<br/>OTel + Prometheus"]
        Cfg["internal/config<br/>flags + env"]
    end

    srv_main --> Server
    srv_main --> Storage
    srv_main --> FileStore
    srv_main --> Broker
    srv_main --> Obs
    srv_main --> Cfg

    wrk_main --> Storage
    wrk_main --> FileStore
    wrk_main --> Broker
    wrk_main --> Obs
    wrk_main --> Cfg

    mig_main --> Storage

    Server --> Storage
    Server --> FileStore
    Server --> Broker
```

`cmd/*/main.go` are composition roots — they own concrete implementations and
wire them through the interface boundaries (`storage.Storage`,
`filestorage.FileStorage`, broker `Publisher`/`Consumer`). All three commands
share the same `internal/storage` package, which is why the Helm migration
hook can call `storage.ApplyMigrationsDSN` directly.

## Key flows

| Flow | Path |
|---|---|
| Health check | Client → Traefik → Ingress → Server `/health` → Postgres ping |
| Upload | Client → Server → DB (insert) → MinIO (put) → DB (update key) → RabbitMQ (publish) |
| Thumbnail | RabbitMQ → Worker → MinIO (get/put thumbs) → DB (update keys + status=ready) |
| Delete | Client → Server → DB (soft-delete) → RabbitMQ (publish deletes) → Worker → MinIO (drop original + thumbs) |
| Metrics scrape | Prometheus → Service `:9464/metrics` (server + worker) |
| Traces / metrics push | Server / Worker → OTel Collector :4317 (OTLP gRPC) |
| Migration | `helm install/upgrade` → migrate Job (`cmd/migrate`) → Postgres |

## Security boundaries

- **Pod-level:** non-root (UID 65532), read-only rootfs, drop ALL caps,
  seccomp `RuntimeDefault`, no SA token projected.
- **Network-level:** default-deny in `gophprofile`, then targeted allow rules
  (DNS → kube-system, HTTP-in from ingress controller, metrics-in from monitoring,
  egress to in-ns Postgres/MinIO/RabbitMQ, OTLP egress to monitoring).
- **Cluster-level:** dedicated `ServiceAccount` per workload, no `Role`/`RoleBinding`
  granted (least privilege = no API access).

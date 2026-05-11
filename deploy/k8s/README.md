# GophProfile — Kubernetes manifests

Raw manifests for deploying the app to a local Rancher Desktop cluster.

> The raw manifests here remain as a Helm-free fallback

## Layout

```
deploy/k8s/
├── 00-namespace.yaml
├── 10-configmap.yaml          # non-secret app config (server + worker)
├── 11-secret.yaml             # DSN, S3 keys, RabbitMQ URL (dev plaintext)
├── 20-server-deployment.yaml  # HTTP :8080, metrics :9464, probes
├── 21-server-service.yaml     # ClusterIP :80 → :8080, metrics :9464
├── 22-server-ingress.yaml     # gophprofile.localhost → server (Traefik)
├── 30-worker-deployment.yaml  # thumbnail worker, metrics :9464
├── 31-worker-service.yaml     # headless Service for ServiceMonitor scraping
├── 40-hpa.yaml                # HPA for server (CPU 70% / mem 80%) and worker (CPU 70%)
├── 50-servicemonitor.yaml     # Prometheus Operator ServiceMonitor (server + worker)
├── 51-grafana-dashboards.yaml # Grafana dashboards (sidecar auto-discovers via `grafana_dashboard: "1"`)
├── 60-serviceaccount.yaml     # dedicated SAs (no API access, token automount disabled)
├── 70-networkpolicy.yaml      # default-deny + targeted allow rules
└── infra/                     # local-only Postgres, MinIO, RabbitMQ
    ├── postgres.yaml
    ├── minio.yaml
    └── rabbitmq.yaml
```

In-app TLS is disabled here — `SERVER_CERT`/`SERVER_KEY` are not set, so the server listens on plain HTTP. TLS belongs at the Ingress in K8s.

## Prerequisites

- Rancher Desktop with Kubernetes enabled (Traefik IngressClass ships by default)
- `kubectl` configured against the `rancher-desktop` context
- Docker / nerdctl available locally

## Build images into the Rancher Desktop runtime

```sh
docker context use rancher-desktop # run this if you are using docker desktop app
docker context ls                  # confirm rancher-desktop has the *
```

```sh
# Pin the API version if local docker CLI is newer than RD's dockerd
export DOCKER_API_VERSION=1.43

docker build --build-arg CMD=server -t gophprofile-server:dev -t docker.io/library/gophprofile-server:dev .
docker build --build-arg CMD=worker -t gophprofile-worker:dev -t docker.io/library/gophprofile-worker:dev .

# Verify they actually landed in the RD VM (not just the host CLI cache)
rdctl shell -- sudo docker images | grep gophprofile
```

## Deploy

```sh
kubectl apply -f deploy/k8s/00-namespace.yaml
kubectl apply -f deploy/k8s/infra/
kubectl apply -f deploy/k8s/
kubectl -n gophprofile rollout status deploy/gophprofile-server
kubectl -n gophprofile rollout status deploy/gophprofile-worker
```

The server runs migrations from the `./migrations` directory baked into the image (the Dockerfile copies them), so no separate migration job is needed yet.

## Smoke test

```sh
# /etc/hosts mapping is optional — Traefik on Rancher Desktop binds *.localhost
curl -i http://gophprofile.localhost/health

# Or via port-forward without ingress:
kubectl -n gophprofile port-forward svc/gophprofile-server 8080:80
curl -i http://localhost:8080/health
```

## Verify probes, HPA, graceful shutdown

Probes:

```sh
kubectl -n gophprofile describe pod -l app=gophprofile-server | grep -E "Liveness|Readiness"
```

HPA (needs `metrics-server` — Rancher Desktop ships it by default):

```sh
kubectl -n gophprofile get hpa
kubectl -n gophprofile describe hpa gophprofile-server
```

Graceful shutdown — start a slow request, delete the pod mid-flight, confirm the request still completes (no 502/connection-reset). With `replicas: 2` + `maxUnavailable: 0`, a rolling restart should cause zero failed requests:

```sh
# In one terminal, generate steady traffic against /health via the ingress:
while true; do curl -s -o /dev/null -w "%{http_code}\n" http://gophprofile.localhost/health; done

# In another, roll the deployment:
kubectl -n gophprofile rollout restart deploy/gophprofile-server
kubectl -n gophprofile rollout status deploy/gophprofile-server
```

## Monitoring with Prometheus Operator

The app now exposes Prometheus metrics on `:9464/metrics` (server and worker). The `ServiceMonitor` resource in `50-servicemonitor.yaml` tells Prometheus which Services to scrape, so the operator's CRDs must be installed first.

Install kube-prometheus-stack (one-time):

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack --namespace monitoring --create-namespace
```

```sh
kubectl apply -f deploy/k8s/50-servicemonitor.yaml
kubectl -n gophprofile get servicemonitor

# Hit the metrics endpoint directly to confirm the app is exporting:
kubectl -n gophprofile port-forward svc/gophprofile-server 9464:9464 &
curl -s http://localhost:9464/metrics | grep avatars_

# Open the Prometheus UI and check Status → Targets for the gophprofile jobs:
kubectl -n monitoring port-forward svc/kube-prometheus-stack-prometheus 9090:9090
# → http://localhost:9090/targets
```

## Grafana dashboards

`51-grafana-dashboards.yaml` ships the three project dashboards (`red`, `kpis`, `resources`) as a ConfigMap in the `monitoring` namespace. The kube-prometheus-stack Grafana sidecar watches for ConfigMaps labelled `grafana_dashboard: "1"` and imports them automatically.

```sh
kubectl apply -f deploy/k8s/51-grafana-dashboards.yaml
kubectl -n monitoring get cm -l grafana_dashboard=1
```

Open Grafana → Dashboards → Browse, the three dashboards appear under the default folder:

```sh
kubectl -n monitoring port-forward svc/kube-prometheus-stack-grafana 3000:80
# default creds: admin / prom-operator
# or get it with: kubectl -n monitoring get secret kube-prometheus-stack-grafana -o jsonpath='{.data.admin-password}' | base64 -d; echo
```

The JSON source of truth lives in `grafana/dashboards/`. Regenerate the manifest after edits:

```sh
kubectl create configmap gophprofile-grafana-dashboards \
  --namespace=monitoring \
  --from-file=red.json=grafana/dashboards/red.json \
  --from-file=kpis.json=grafana/dashboards/kpis.json \
  --from-file=resources.json=grafana/dashboards/resources.json \
  --dry-run=client -o yaml > deploy/k8s/51-grafana-dashboards.yaml
# then re-add the `grafana_dashboard: "1"` label under metadata.
```

## Hardening

**ServiceAccounts & RBAC.** The app does not call the Kubernetes API, so each workload gets a dedicated `ServiceAccount` with no `Role`/`RoleBinding` attached and `automountServiceAccountToken: false` set on both the SA and the pod spec — no SA token is ever projected into the container.

**SecurityContext.** Both Deployments enforce:

- `runAsNonRoot: true`, `runAsUser/Group: 65532` (matches the distroless `nonroot` image)
- `readOnlyRootFilesystem: true`
- `allowPrivilegeEscalation: false`
- `capabilities.drop: ["ALL"]`
- `seccompProfile: RuntimeDefault`

Verify:

```sh
kubectl -n gophprofile get pod -l app=gophprofile-server -o jsonpath='{.items[0].spec.securityContext}{"\n"}'
kubectl -n gophprofile exec deploy/gophprofile-server -- id       # uid=65532
kubectl -n gophprofile exec deploy/gophprofile-server -- touch /x # read-only fs → error
```

**NetworkPolicy.** `70-networkpolicy.yaml` applies a default-deny baseline and opens only what's needed:

| Policy | Selector | Allows |
|---|---|---|
| `default-deny` | all pods in ns | nothing (ingress + egress) |
| `allow-dns-egress` | all pods | egress UDP/TCP 53 → kube-system |
| `server-ingress` | `app=gophprofile-server` | TCP 8080 from kube-system / ingress-nginx, TCP 9464 from monitoring |
| `worker-ingress` | `app=gophprofile-worker` | TCP 9464 from monitoring |
| `app-egress-to-infra` | server + worker | TCP to in-ns postgres:5432, minio:9000, rabbitmq:5672 |
| `app-egress-to-otel` | server + worker | TCP 4317/4318 → monitoring (OTLP) |
| `infra-ingress-from-app` | postgres / minio / rabbitmq | from server + worker pods |

## Tear down

```sh
kubectl delete namespace gophprofile
```

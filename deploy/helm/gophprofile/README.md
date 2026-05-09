# gophprofile (Helm chart)

Packages the GophProfile server, worker, and supporting resources (ServiceAccounts, ConfigMap, Secret, Service, Ingress, HPA, ServiceMonitor, NetworkPolicies) + a pre-install/pre-upgrade migration Job.

## Layout

```
deploy/helm/gophprofile/
├── Chart.yaml
├── values.yaml          # dev defaults (Rancher Desktop, Traefik, plaintext Secret)
├── values-prod.yaml     # prod overlay (registry images, external Secret, nginx + TLS)
└── templates/
    ├── _helpers.tpl
    ├── NOTES.txt
    ├── serviceaccount.yaml
    ├── configmap.yaml
    ├── secret.yaml             # only rendered when secret.create=true
    ├── server-deployment.yaml
    ├── server-service.yaml
    ├── ingress.yaml
    ├── worker-deployment.yaml
    ├── worker-service.yaml
    ├── hpa.yaml
    ├── servicemonitor.yaml     # toggled via serviceMonitor.enabled
    ├── networkpolicy.yaml      # toggled via networkPolicy.enabled
    └── migrate-job.yaml        # helm.sh/hook: pre-install,pre-upgrade
```

## Build images

The chart references three images: server, worker, and migrate. All three build from the same `Dockerfile` via the `CMD` build-arg.

```sh
docker context use rancher-desktop
export DOCKER_API_VERSION=1.43

docker build --build-arg CMD=server  -t docker.io/library/gophprofile-server:dev  .
docker build --build-arg CMD=worker  -t docker.io/library/gophprofile-worker:dev  .
docker build --build-arg CMD=migrate -t docker.io/library/gophprofile-migrate:dev .
```

## Install (dev — Rancher Desktop)

```sh
# Infra (Postgres, MinIO, RabbitMQ) still applied as raw manifests:
kubectl apply -f deploy/k8s/00-namespace.yaml
kubectl apply -f deploy/k8s/infra/

helm install gophprofile deploy/helm/gophprofile --namespace gophprofile

helm test gophprofile --namespace gophprofile     # optional, no tests yet
```

The pre-install Job runs `cmd/migrate` against `DATABASE_DSN` from the rendered Secret and exits. Once it succeeds, server/worker rollouts proceed.

## Upgrade

```sh
helm upgrade gophprofile deploy/helm/gophprofile -n gophprofile
```

The pre-upgrade hook re-runs migrations (idempotent — `migrate.Up()` is a no-op when there's nothing new). The server's startup-time `applyMigrations` remains in place as a safety net for environments that bypass the chart.

## Production overlay

```sh
helm upgrade --install gophprofile deploy/helm/gophprofile \
  -n gophprofile \
  -f deploy/helm/gophprofile/values-prod.yaml \
  --set image.tag=0.1.0
```

## Useful overrides

1. `--set image.tag=0.1.0` bump the image tag for all three images 
2. `--set ingress.enabled=false` skip the Ingress (use port-forward) 
3. `--set serviceMonitor.enabled=false` skip ServiceMonitor (no Prometheus Operator installed) 
4. `--set networkPolicy.enabled=false` skip NetworkPolicies (CNI doesn't enforce) 
5. `--set migrate.enabled=false` rely on server startup migrations only 
6. `--set secret.existingSecret=my-secret --set secret.create=false` use a pre-existing Secret 

## Uninstall

```sh
helm uninstall gophprofile -n gophprofile
```
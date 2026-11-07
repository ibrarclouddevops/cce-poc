# Online Boutique — GitOps branch

This branch describes **what should run** in the CCE cluster.
Argo CD reads it and makes the cluster match. Change a file here → the cluster changes.

> Code lives on `main`. This branch only holds deployment settings.

## What's in here

```
root-app.yaml            ← the ONLY file you apply by hand (starts everything)
argocd/
  project.yaml           ← the rules: our repo only, online-boutique namespace only
  apps/                  ← one Argo CD app per service (12 files)
services/
  <service>/
    kustomization.yaml   ← list of files + the VERSION (image tag)
    deployment.yaml      ← how to run it (image, CPU/memory, health checks)
    service.yaml         ← its address inside the cluster
```

Every file starts with a short comment explaining what it does.

## Database

`services/postgres/` runs **PostgreSQL** with a 10Gi persistent disk. Data survives restarts.

| Table (database `boutique`) | Owner | Contents |
|---|---|---|
| `cart_items` | cartservice | every shopper's cart |
| `products` | productcatalogservice | the products for sale (filled from `products.json` on first start) |

The database is created by PostgreSQL on first start; the tables are created by the two
services. Argo CD is set to never delete the database.

Look inside:

```bash
kubectl -n online-boutique exec -it postgres-0 -- psql -U boutique -d boutique
#   \dt                       list tables
#   SELECT * FROM products;   see products
#   SELECT * FROM cart_items; see carts
```

## How a new version gets deployed (automatic)

```
push code to main
  → pipeline builds the image and pushes it to SWR       (…/frontend:abc1234)
  → pipeline changes newTag in services/frontend/kustomization.yaml on this branch
  → Argo CD sees the change and deploys it
```

You'll see these as commits named `deploy(<service>): <tag>`.

## Common tasks

| I want to… | Do this on this branch |
|---|---|
| Roll back a service | `git revert` its `deploy(<service>): …` commit and push |
| Pin a specific version | Edit `newTag` in `services/<service>/kustomization.yaml` |
| Change replicas / CPU / env vars | Edit `services/<service>/deployment.yaml` |
| Stop the load generator | Delete `argocd/apps/loadgenerator.yaml` |
| Add a service | Add `services/<new>/` and `argocd/apps/<new>.yaml` |

Don't fix things with `kubectl edit` — Argo CD will change them back to match git.

## First-time setup

```bash
# 1. Install Argo CD on the cluster
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml

# 2. Create the database password (kept out of git on purpose)
kubectl create namespace online-boutique
kubectl -n online-boutique create secret generic postgres-credentials \
  --from-literal=password="$(openssl rand -base64 24)"

# 3. Start everything
kubectl apply -n argocd -f root-app.yaml
```

Before going live:
- **Public access:** fill in `services/frontend/elb-patch.yaml` and enable it in `services/frontend/kustomization.yaml`.
- **Image login:** pods use the `default-secret` pull secret (CCE normally creates it).
- **Docker Hub images:** `postgres` and `loadgenerator` use public images; mirror them to SWR if nodes have no internet.
- **Disk:** the database disk uses storage class `csi-disk` (check with `kubectl get storageclass`).

## Check your changes locally

```bash
for d in services/*/; do kubectl kustomize "$d" > /dev/null && echo "ok $d"; done
```

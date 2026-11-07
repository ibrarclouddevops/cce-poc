# Online Boutique — GitOps branch

This branch describes **what should run** in the CCE cluster.
Argo CD reads it and makes the cluster match. Change a file here → the cluster changes.

> Code lives on `main`. This branch only holds deployment settings.

## What's in here

```
root-app.yaml              ← the ONLY file you apply by hand (starts everything)
argocd/
  project.yaml             ← rules for the shop: our repo, online-boutique namespace only
  platform-project.yaml    ← rules for cluster tools (cert-manager, gateway)
  apps/                    ← one Argo CD app per shop service
  platform/                ← Argo CD apps for the cluster tools (installed with Helm)
  repositories/            ← tells Argo CD where the NGINX Helm chart lives
platform/
  cert-manager/            ← Helm settings for cert-manager
  cert-manager-config/     ← Let's Encrypt issuers (Cloudflare DNS)
  nginx-gateway-fabric/    ← Helm settings for NGINX Gateway Fabric
  gateway/                 ← public Gateway, certificate, redirects, argocd route
services/
  <service>/
    kustomization.yaml     ← list of files + the VERSION (image tag)
    deployment.yaml        ← how to run it (image, CPU/memory, health checks)
    service.yaml           ← its address inside the cluster
  frontend/httproute.yaml  ← https://shop.antonx.com
```

Every file starts with a short comment explaining what it does.

## Domains and HTTPS

| Address | Goes to | Certificate |
|---|---|---|
| https://shop.antonx.com | the shop (frontend) | Let's Encrypt `*.antonx.com` |
| https://argocd.antonx.com | Argo CD dashboard | same certificate |
| http://… | redirected to https:// | — |

How it fits together:

```
visitor → Cloudflare DNS → Huawei ELB (one public IP)
        → NGINX Gateway Fabric (Gateway "public", does HTTPS)
        → HTTPRoute → frontend / argocd-server
cert-manager → Let's Encrypt (proves ownership via a Cloudflare DNS record)
             → secret antonx-com-tls → used by the Gateway, renewed automatically
```

The back-end services have **no** public address on purpose; only the website and Argo CD do.

Installed with Helm through Argo CD (versions pinned in `argocd/platform/`):
cert-manager v1.21.3, NGINX Gateway Fabric 2.8.0, Gateway API v1.6.3.
NGINX Gateway Fabric needs **Kubernetes 1.32 or newer** on the cluster.

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

# 2. Let Argo CD run behind the Gateway (the Gateway does HTTPS)
kubectl -n argocd patch configmap argocd-cmd-params-cm --type merge -p '{"data":{"server.insecure":"true"}}'
kubectl -n argocd rollout restart deployment argocd-server

# 3. Secrets that are kept out of git on purpose
kubectl create namespace online-boutique
kubectl -n online-boutique create secret generic postgres-credentials \
  --from-literal=password="$(openssl rand -base64 24)"

kubectl create namespace cert-manager
kubectl -n cert-manager create secret generic cloudflare-api-token \
  --from-literal=api-token='<CLOUDFLARE_TOKEN>'   # Cloudflare: 'Edit zone DNS' token for antonx.com

# 4. Start everything
kubectl apply -n argocd -f root-app.yaml

# 5. When the Gateway has a public IP, add DNS records in Cloudflare
kubectl -n nginx-gateway get svc     # EXTERNAL-IP of the gateway's LoadBalancer
#    A  shop    -> <EXTERNAL-IP>   (DNS only / grey cloud)
#    A  argocd  -> <EXTERNAL-IP>   (DNS only / grey cloud)

# 6. Check the certificate
kubectl -n nginx-gateway get certificate     # READY should become True
```

Before going live:
- **ELB settings:** uncomment and fill in the `patches:` block in `platform/nginx-gateway-fabric/values.yaml`
  (existing ELB ID or auto-create), otherwise the gateway's public IP stays `<pending>`.
- **Image login:** pods use the `default-secret` pull secret (CCE normally creates it).
- **Public images:** `postgres`, `loadgenerator`, cert-manager (quay.io) and NGINX Gateway Fabric (ghcr.io)
  are pulled from the internet; mirror them to SWR if nodes have no internet.
- **Disk:** the database disk uses storage class `csi-disk` (check with `kubectl get storageclass`).
- **Testing certificates:** set `issuerRef.name: letsencrypt-staging` in `platform/gateway/certificate.yaml`
  first if you want to avoid Let's Encrypt rate limits while experimenting.

## Check your changes locally

```bash
for d in services/*/ platform/cert-manager-config platform/gateway; do kubectl kustomize "$d" > /dev/null && echo "ok $d"; done
```

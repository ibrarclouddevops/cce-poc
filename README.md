# Online Boutique — GitOps (Argo CD on Huawei CCE)

This branch is the **desired state** of the Online Boutique deployment on the
`cce-poc` CCE cluster (Indus Cloud). Argo CD watches this branch and keeps the
cluster in sync with it. Application source code and the image build pipelines
live on `main`.

```
main branch                          gitops branch (this one)              CCE cluster
-----------                          ------------------------              -----------
src/<service>  ──► build-<service>   apps/<service>/overlays/cce-poc  ◄──  Argo CD
                   pipeline ──► SWR    images: newTag: "<sha>"         ──►  online-boutique ns
                                (bump tag here to deploy)
```

## Layout

```
bootstrap/
  root-app.yaml                 # "app of apps": the only file applied by hand
argocd/
  projects/online-boutique.yaml # AppProject: allowed repo, namespace and resource kinds
  applications/<service>.yaml   # one Argo CD Application per service (12)
apps/
  <service>/
    base/                       # plain Kubernetes manifests (environment-agnostic)
    overlays/cce-poc/           # namespace, SWR image + tag, pull secret, CCE specifics
```

| Application | Wave | Image |
|---|---|---|
| redis-cart | 0 | `redis:alpine` (Docker Hub) |
| adservice, cartservice, checkoutservice, currencyservice, emailservice, paymentservice, productcatalogservice, recommendationservice, shippingservice | 1 | `swr.pk-icln-1.indus-cloud.com/cce-poc/<service>` |
| frontend | 2 | `swr.pk-icln-1.indus-cloud.com/cce-poc/frontend` |
| loadgenerator (optional) | 3 | `swr.pk-icln-1.indus-cloud.com/cce-poc/loadgenerator` |

Sync waves set the creation order when bootstrapping: project → data → backends → frontend → tooling.

## Why one Application per service

- **Independent lifecycle**: each service syncs, self-heals and rolls back on its own;
  a broken `cartservice` doesn't mark the whole shop as degraded.
- **Matches the build pipelines**: one pipeline and one Application per service.
- **Clear ownership and RBAC**: teams can be scoped to individual apps.
- **Still one-click**: the root app creates all of them, so bootstrapping is a single apply.

## Bootstrap

Prerequisites: Argo CD installed in the `argocd` namespace of the CCE cluster.

```bash
kubectl apply -n argocd -f bootstrap/root-app.yaml
```

The root app creates the `online-boutique` AppProject and the 12 Applications, which
then deploy into the `online-boutique` namespace (created automatically).

## Deploying a new version

1. Merge to `main` → `build-<service>` pushes `…/<service>:<short-sha>` to SWR.
2. On this branch, update the tag in `apps/<service>/overlays/cce-poc/kustomization.yaml`:
   ```bash
   cd apps/<service>/overlays/cce-poc && kustomize edit set image <service>=swr.pk-icln-1.indus-cloud.com/cce-poc/<service>:<short-sha>
   ```
3. Commit and push. Argo CD rolls it out automatically.

**Rollback** = `git revert` the tag-bump commit (or use *History and rollback* in the Argo CD UI
for a temporary rollback; selfHeal will move it back to git on the next sync).

## Before the first sync — checklist

- [ ] **Image pulls from SWR**: overlays reference `imagePullSecrets: default-secret`, which CCE
      creates in each namespace. If it's missing, create it:
      `kubectl -n online-boutique create secret docker-registry default-secret --docker-server=swr.pk-icln-1.indus-cloud.com --docker-username=<user> --docker-password=<pass>`
- [ ] **Registry TLS**: the SWR endpoint currently serves a certificate without its hostname.
      CCE nodes in the same region normally pull without issue; if pods show `x509` errors,
      ask Indus Cloud to fix the certificate.
- [ ] **Public images**: `redis:alpine` and `busybox` come from Docker Hub. If nodes have no
      internet access, mirror them into SWR and add an `images:` override in the overlay.
- [ ] **Frontend ELB**: fill in `apps/frontend/overlays/cce-poc/elb-patch.yaml` and enable it in
      that overlay's `kustomization.yaml`, or `frontend-external` stays `<pending>`.

## Validate locally

```bash
for d in apps/*/overlays/cce-poc; do kubectl kustomize "$d" > /dev/null && echo "ok $d"; done
```

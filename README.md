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
    serviceaccount.yaml  ← its identity
```

Every file starts with a short comment explaining what it does.

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

# 2. Start everything
kubectl apply -n argocd -f root-app.yaml
```

Before going live:
- **Public access:** fill in `services/frontend/elb-patch.yaml` and enable it in `services/frontend/kustomization.yaml`.
- **Image login:** pods use the `default-secret` pull secret (CCE normally creates it).
- **Docker Hub images:** `redis-cart` and `loadgenerator` use public images; mirror them to SWR if nodes have no internet.

## Check your changes locally

```bash
for d in services/*/; do kubectl kustomize "$d" > /dev/null && echo "ok $d"; done
```

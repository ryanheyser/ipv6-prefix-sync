# ipv6-prefix-sync

Static Go binary (stdlib only, `FROM scratch`, ~6 MB) that keeps the
`flux-system/cluster-ipv6-prefix` ConfigMap (`MLB_V6_PREFIX`) set to the MetalLB
/64, the last /64 of the delegated /60 that this node sees. When the value
changes, it asks Flux to reconcile. It runs as a CronJob with `hostNetwork: true`,
and the manifest is `network-system/metallb/ipv6-prefix-sync.yaml` in homelab-ops.

Env: `DRY_RUN` (`true`/`false`), `FLUX_KUSTOMIZATIONS` (default
`network-system,default`), `IF_INET6_PATH` (for tests).

```sh
go test ./...
docker buildx build --platform linux/amd64,linux/arm64 -t ghcr.io/ryanheyser/ipv6-prefix-sync:dev .
```

Pushing to `main` or a `v*` tag builds and pushes a multi-arch image to GHCR
(see `.github/workflows/image.yml`). Make the GHCR package public, or add an
imagePullSecret to the CronJob.

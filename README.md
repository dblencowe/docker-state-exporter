# docker-state-exporter

Prometheus exporter for Docker container state - health, status, restart count, lifecycle timestamps. Pairs with cAdvisor (which doesn't expose any of those).

Fork of [karugaru/docker_state_exporter](https://github.com/karugaru/docker_state_exporter). Upstream is dead and the bundled Docker SDK is too old to talk to current daemons:

```
client version 1.41 is too old. Minimum supported API version is 1.44
```

This fork pins a current SDK and turns on API version negotiation, so it works against any reasonable daemon. Same metric names and labels as upstream, so existing dashboards keep working.

## Run it

```bash
docker run -d --name docker-state-exporter \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  --group-add "$(stat -c '%g' /var/run/docker.sock)" \
  -p 8080:8080 \
  ghcr.io/dblencowe/docker-state-exporter:latest
```

The `--group-add` matters: the image is distroless `nonroot` (UID 65532), so it needs the host's docker GID to read the socket. Skip it and the first scrape will tell you `permission denied`.

Compose:

```yaml
services:
  docker-state-exporter:
    image: ghcr.io/dblencowe/docker-state-exporter:latest
    restart: unless-stopped
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    group_add:
      - "999"   # host's docker GID; check with: getent group docker
    ports: ["8080:8080"]
```

Images are multi-arch (`linux/amd64`, `linux/arm64`, `linux/arm/v7`) and signed with cosign keyless.

## Endpoints

- `GET /metrics` - Prometheus scrape (path overridable via `-metrics-path`)
- `GET /-/healthy` - liveness; returns `up` if the process is breathing
- `GET /-/ready` - readiness; pings the daemon, returns `503` if it can't reach it

## Metrics

All emitted as gauges, all labelled with `id`, `name`, `image`, `container_hostname`, the optional `host`, and one `container_label_*` per container label.

| Name | Description |
|------|-------------|
| `container_state_health_status{status="..."}` | `1` for the active health state, `0` for the others (`none`, `starting`, `healthy`, `unhealthy`) |
| `container_state_status{status="..."}` | `1` for the active container state, `0` for the others (`paused`, `restarting`, `running`, `removing`, `dead`, `created`, `exited`) |
| `container_state_oomkilled` | `1` if killed by the OOM killer |
| `container_state_startedat` | Unix seconds - last start time |
| `container_state_finishedat` | Unix seconds - last stop time |
| `container_state_created` | Unix seconds - creation time |
| `container_restartcount` | Restart count |

`container_label_*` keys are lower-cased and non-alphanumerics get replaced with `_`. There's a built-in deny-list for Compose's `config-hash` and `oneoff` labels - both rotate on every rebuild and would otherwise nuke your TSDB.

The `host` label is empty by default (omitted entirely) so adding it later doesn't break series identity. Set `-host-label` once you start scraping multiple Docker hosts into one Prometheus.

## Flags

| Flag | Default | |
|------|---------|---|
| `-listen-address` | `:8080` | |
| `-metrics-path` | `/metrics` | |
| `-cache-ttl` | `1s` | how long to reuse one `docker inspect` snapshot across scrapes |
| `-host-label` | *(empty)* | sets the `host` label; empty omits it |
| `-log-level` | `info` | `debug` / `info` / `warn` / `error` |
| `-log-format` | `json` | `json` or `text` |
| `-version` | | prints version + commit + build date |

## Why one inspect call per cache TTL

Every Prometheus scrape would otherwise trigger one `ContainerList` plus one `ContainerInspect` *per container*. Default Prometheus scrapes every 15s; if you've got 50 containers and three Prometheus instances scraping you, the docker daemon eats the load. The 1s cache means each scrape sees fresh-enough data without that fanout. Bump it on dense hosts.

## Develop

Go 1.25+ (required by the Docker SDK transitive deps). Source lives in `cmd/docker-state-exporter/`.

```bash
make build         # local binary
make test          # unit tests
make test-race     # tests + race detector (needs CGO)
make lint          # golangci-lint
make docker        # local single-arch image
make docker-multi  # buildx multi-arch
```

Tests use a fake `DockerClient` (the four-method interface in `docker.go`) - no daemon needed.

## Releases

Tags are driven by [release-please](https://github.com/googleapis/release-please) reading [Conventional Commits](https://www.conventionalcommits.org/) on `main`. A merged `feat:` or `fix:` opens a release PR with a generated changelog and a version bump; merging the PR cuts the tag. Tag pushes trigger [`docker.yml`](.github/workflows/docker.yml), which builds the multi-arch image, pushes to GHCR, and signs with cosign.

Verify a published image:

```bash
cosign verify ghcr.io/dblencowe/docker-state-exporter:vX.Y.Z \
  --certificate-identity-regexp '^https://github.com/dblencowe/docker-state-exporter/' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com'
```

## License

MIT - see [LICENSE](LICENSE).

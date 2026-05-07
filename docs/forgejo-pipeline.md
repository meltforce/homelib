# Forgejo Equivalent of the Build Pipeline

Sketch of what a Forgejo-Actions equivalent of `.github/workflows/build.yml`
would look like once homelib is migrated to the in-tailnet Forgejo at
`git.coydog-fence.ts.net`. Forgejo Actions is GitHub-Actions-compatible at
the syntax level, so the differences are mostly about runners, secret
storage, and which actions are available.

## Workflow file

Path: `.forgejo/workflows/build.yml` (Forgejo also accepts `.gitea/workflows/`
and `.github/workflows/` for compat, but `.forgejo/` is the convention).

```yaml
name: Build

on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  build:
    runs-on: docker-buildx          # label of a self-hosted runner
    container:
      image: ghcr.io/catthehacker/ubuntu:act-22.04
    steps:
      - uses: actions/checkout@v4

      - uses: docker/setup-buildx-action@v3

      - uses: docker/login-action@v3
        with:
          username: meltforce
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Build and push
        uses: docker/build-push-action@v5
        with:
          context: .
          push: true
          tags: meltforce/homelib:edge
          build-args: |
            VERSION=edge-${{ gitea.sha }}
          # type=gha is GitHub-only; pick one of these instead:
          cache-from: type=registry,ref=meltforce/homelib:cache
          cache-to:   type=registry,ref=meltforce/homelib:cache,mode=max
```

### Diffs vs. the GitHub workflow

| Aspect | GitHub Actions | Forgejo Actions |
|---|---|---|
| Trigger event vars | `${{ github.sha }}` | `${{ gitea.sha }}` (or `github.sha` works in compat mode) |
| Runner | `ubuntu-latest` (GitHub-hosted) | self-hosted label (`docker-buildx`, `linux`, …) |
| Docker action ecosystem | works directly | most `docker/*` v3+ actions work; some marketplace-only actions don't |
| Build cache | `type=gha` | `type=registry` (push to a separate tag in the same Docker Hub repo) **or** `type=local,src=/var/cache/buildkit` if the runner mounts a persistent volume |
| Release action | `softprops/action-gh-release@v2` | switch to Forgejo's REST `POST /api/v1/repos/{owner}/{repo}/releases` (a generic `actions/forgejo-release@v1` exists; otherwise a small `curl`) |
| Identity federation (OIDC) | `tailscale/github-action@v4` with GitHub OIDC issuer | not needed in this trimmed pipeline; but if reintroduced, Forgejo also issues OIDC tokens (issuer `https://git.coydog-fence.ts.net`) and Tailscale's identity-federation trust would have to add that issuer |

## The runner: what to provision

Forgejo Actions needs at least one **self-hosted runner** (Forgejo-Runner,
formerly act_runner). It is a Go binary that long-polls the Forgejo API
and runs jobs in Docker containers.

### Sizing & placement

For the homelib pipeline (Go build + multi-arch Docker buildx):

| Resource | Recommended | Notes |
|---|---|---|
| CPU | 2–4 vCPU | Go compile is the hot path; `CGO_ENABLED=0` so no toolchain pain |
| RAM | 4 GiB | buildx layer cache + Go compile |
| Disk | 20 GiB | Docker layer cache, BuildKit blobs, repo checkouts |
| OS | Debian/Ubuntu LXC, or a small VM | LXC is fine; needs nesting=1 if Docker-in-LXC |
| Network | tailnet member | so the runner can pull from `git.coydog-fence.ts.net` and push to `dockhand`-managed targets if you ever re-enable an in-tailnet deploy step |

Realistically a **single 2 vCPU / 4 GiB LXC** on `dude` or any other
hypervisor handles the homelib build comfortably (Docker Hub push is
the long pole, ~30s with cache). Add more later if other repos start
running CI on the same runner.

### Setup outline

1. Create LXC `forgejo-runner` (privileged or with `lxc.apparmor.profile: unconfined` + nesting if you want Docker-in-LXC; or use a thin VM).
2. Install Docker (`apt install docker-ce`) — runs jobs as containers.
3. Install Forgejo-Runner:
   ```bash
   ARCH=amd64
   wget https://code.forgejo.org/forgejo/runner/releases/download/v6.5.0/forgejo-runner-6.5.0-linux-${ARCH}
   install -m 755 forgejo-runner-6.5.0-linux-${ARCH} /usr/local/bin/forgejo-runner
   ```
4. Register against the Forgejo instance:
   ```bash
   forgejo-runner register \
     --instance https://git.coydog-fence.ts.net \
     --token <runner-token-from-admin-ui> \
     --name forgejo-runner-01 \
     --labels docker-buildx,docker,linux/amd64 \
     --no-interactive
   ```
   The token comes from **Site Administration → Actions → Runners → Create new runner** (or per-repo / per-org scoped tokens).
5. Configure runner (`/etc/forgejo-runner/config.yaml`):
   - `container.docker_host: -` (use system Docker socket)
   - `container.privileged: true` if buildx with QEMU multi-arch is needed; otherwise leave off
   - `cache.enabled: true` and pick a `cache.dir` on persistent disk so `actions/cache` works across runs
6. systemd unit:
   ```ini
   [Unit]
   Description=Forgejo Runner
   After=docker.service
   [Service]
   ExecStart=/usr/local/bin/forgejo-runner daemon -c /etc/forgejo-runner/config.yaml
   WorkingDirectory=/var/lib/forgejo-runner
   Restart=on-failure
   User=forgejo-runner
   [Install]
   WantedBy=multi-user.target
   ```
7. Push a workflow change → check the run page on Forgejo → confirm the runner picks up the job.

### Secrets

Forgejo stores Actions secrets at three scopes: org, repo, and per-environment.
Move the existing GitHub secret into the repo:

- `DOCKERHUB_TOKEN` — repo-scoped secret on `meltforce/homelib`.

The previously needed `TS_OAUTH_CLIENT_ID`, `TS_AUDIENCE`, `DEPLOY_HOST`,
and the `DEPLOY_PATH` variable are obsolete once the deploy step lives
outside CI (which is the current setup on GitHub too).

If you want Forgejo to keep secrets in setec instead of in its own DB,
the runner can mount a setec-CLI binary and resolve secrets at job time
— but that's additional plumbing and only worth it if you're standardizing
on setec for everything. Forgejo's built-in secret store is fine for a
homelab.

## Migration order suggestion

1. Provision the runner, register against `git.coydog-fence.ts.net`, run a
   throwaway `hello-world` workflow on a dummy repo. Confirms runner→Forgejo
   plumbing in isolation.
2. Push-mirror `meltforce/homelib` from GitHub to Forgejo — Forgejo's UI
   has a one-click migration that includes mirroring. Keep GitHub as
   read-only during transition.
3. Add `.forgejo/workflows/build.yml` (the version above), test with a
   no-op commit, confirm the image lands on Docker Hub.
4. Once confirmed: switch `origin` on dev machines to Forgejo, archive
   the GitHub repo (or keep as a public mirror).
5. Re-introduce automated deploy as a Forgejo Actions step **inside the
   tailnet** — the runner is already a tailnet node, so it can SSH to
   `homelib-lxc` directly without OIDC federation gymnastics. Use a
   tailnet-scoped SSH key stored as a Forgejo secret, or a setec lookup
   inside the job.

## Things that disappear vs. GitHub

- No more GitHub→Tailscale OIDC trust (the broken piece this whole
  rewrite avoids).
- No more `tailscale/github-action@v4` step — the runner is permanently
  on the tailnet.
- No GitHub Releases — replaced by Forgejo Releases (`POST /api/v1/repos/{owner}/{repo}/releases`).
- No GitHub Actions cache — replaced by Docker Hub registry-cache or a
  local persistent BuildKit volume on the runner.

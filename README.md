# miabi CLI

The imperative client for a [Miabi](https://github.com/miabi-io/miabi) control panel.
Drive the deploy flow from a terminal or CI — `miabi apps deploy web --tag $SHA --wait`
updates the image, deploys, blocks until the deployment is terminal, and **exits
non‑zero on failure**.

It is also the tool that **installs and manages a Miabi host**: `miabi setup` stands the
stack up on a machine and `miabi upgrade` moves it forward. Those commands talk to the
local Docker socket, not to a panel — see [Managing a host](#managing-a-host).


## Install

**Homebrew** (macOS & Linux) — from the [miabi-io/homebrew-tap](https://github.com/miabi-io/homebrew-tap):

```bash
brew install miabi-io/tap/miabi
```

<details>
<summary>…or tap first</summary>

Homebrew 6 requires third-party taps to be trusted before the short form works:

```bash
brew tap miabi-io/tap
brew trust miabi-io/tap
brew install miabi
```
</details>

**Go:**

```bash
go install github.com/miabi-io/cli@latest   # installs the `miabi` binary
```

**Prebuilt binary** — download from the [GitHub Releases](https://github.com/miabi-io/cli/releases/latest)
page, or grab the archive for your platform directly (Linux x86_64 shown):

```bash
VERSION=0.4.0
curl -fsSL "https://github.com/miabi-io/cli/releases/download/v${VERSION}/miabi_${VERSION}_linux_amd64.tar.gz" \
  | tar -xz miabi && sudo mv miabi /usr/local/bin/
miabi --version
```

(Swap `linux_amd64` for `linux_arm64`, `darwin_amd64`, `darwin_arm64`, or use the `.zip` for Windows.)

**Docker** — no install at all; handy in CI. Published to Docker Hub and mirrored to GHCR
(`ghcr.io/miabi-io/cli`):

```bash
# check the connection
docker run --rm -e MIABI_SERVER -e MIABI_TOKEN miabi/cli:latest whoami

# deploy from a pipeline — exits non-zero if the rollout fails
docker run --rm -e MIABI_SERVER -e MIABI_TOKEN \
  miabi/cli:latest apps deploy web --tag "$GIT_SHA" --wait

# mount a manifest to apply it declaratively
docker run --rm -e MIABI_SERVER -e MIABI_TOKEN -v "$PWD:/work" -w /work \
  miabi/cli:latest apply -f stack.yaml
```

## Authenticate

The CLI resolves its context as **flags → env → `~/.miabi/config.yaml`**:

```bash
export MIABI_SERVER=https://miabi.example.com
export MIABI_TOKEN=mb_…            # an API key with the `deploy` scope

# …or persist it:
miabi --server "$MIABI_SERVER" --token "$MIABI_TOKEN" login
```

`~/.miabi/config.yaml` (written by `login` / `workspace switch`, mode `0600`) holds
one or more named **contexts** — each a server + token + bound workspace/app — and the
active one:

```yaml
current: acme-prod
contexts:
  acme-prod:
    server:
      url: https://miabi.example.com
      ca: /etc/ssl/miabi-ca.pem   # optional: trust a private CA
      insecure_skip: false        # optional: skip TLS verification
    token: mb_…
    workspace: { id: 42, name: acme-prod, display_name: Acme Prod }
  staging:
    server: { url: https://staging.example.com }
    token: mb_…
```

### Contexts (switch between servers)

`miabi login` writes a context (named by `--context`, else the server host). Switch
between them, or target one for a single command:

```bash
miabi login --context prod    --server https://miabi.example.com --token mb_…
miabi login --context staging --server https://staging.example.com --token mb_…

miabi context ls              # list contexts (→ marks the current)
miabi context use staging     # switch the active context
miabi --context prod apps ls  # run one command against another context
miabi context current         # print the active context name
miabi context delete staging  # remove a context
```

TLS trust per context: `--certificate-authority <ca.pem>` (env `MIABI_CA`) and
`--insecure-skip-tls-verify` (env `MIABI_INSECURE_SKIP_TLS_VERIFY`). Older flat/
single-server config files are migrated into a `default` context automatically.

## Commands

Every app command lives under `apps` and takes the app as its **first
argument** — or you can bind a default once with `miabi use <app>` and omit it:

```
miabi whoami                       # identity, scopes, active workspace + app
miabi workspace ls|show|switch     # set the active workspace context (alias: ws)
miabi use web                      # bind a default app (per workspace)

miabi apps ls                      # list applications (→ marks the bound app)
miabi apps create web (--image miabi/guestbook [--tag 1.0] | --git-repo <url> [--git-ref main]) [--port 3000] [--use]
miabi apps deploy      [web] --tag $SHA [--strategy rolling] [--wait] [--timeout 10m]
miabi apps start|stop|restart [web]               # control the app's container
miabi apps deployments [web]                      # deploy history — the NUMBER column
miabi apps logs        [web] [--follow] [--tail 200]      # current logs (‑‑follow to stream)
miabi apps logs        [web] --deployment 7               # a deployment's build logs
miabi apps set-source  [web] (--image nginx --tag 1.27 | --git-repo <url> --git-ref main)
                                                  # switch image <-> git in place; keeps domains,
                                                  # env, volumes, databases and history
miabi apps resync-pipeline [web]                  # reload the repo's pipelines.yaml (adopt or sync)
miabi apps status      [web] [--deployment 7]
miabi apps releases    [web]
miabi apps rollback    [web] (--to <version> | --to-previous) [--yes]
miabi apps env ls      [web]                              # secret values are masked
miabi apps env set     [web] KEY=VALUE [--secret]
miabi apps env set     [web] KEY --from-file f [--secret] # value from a file/stdin — no shell history
miabi apps env import  [web] --from-file .env [--secret]  # "-" reads stdin

miabi apply  -f stack.yaml [--prune] [--dry-run]  # declarative: converge to a manifest bundle
miabi delete -f stack.yaml [--dry-run]            # delete exactly the resources the bundle names
```

### Managing a host

`setup`, `upgrade` and `stack …` are the exception to "API client": they act on the
**machine they run on**, through its Docker socket, and never call the HTTP API. This is
what `curl -fsSL https://get.miabi.io | sudo bash` installs and then runs.

```
miabi setup [--domain miabi.example.com] [--image miabi/miabi:1.7.3] [--yes]
                                              # install, or converge an existing install
miabi upgrade [component] [--version 1.8.0 | --image <ref>] [--yes]
                                              # roll forward; rolls back if it never gets healthy
miabi stack status                            # what runs, its health, drift from the manifest
miabi stack restart [component] [--yes]       # restart in place, re-reading on-disk config
miabi stack uninstall [--volumes] [--yes]     # --volumes also destroys the database
miabi stack migrate-config                    # /etc/miabi/stack.yaml -> /etc/miabi/miabi.yaml
```

All of them need **root** and a Linux or macOS Docker host. `setup` is idempotent: re-run
it and the stack converges to `/etc/miabi/miabi.yaml`, keeping the stored secrets. `-f,
--file` points at a different manifest.

`setup` and `upgrade` install the **latest published Miabi release**, looked up when the
command runs. The version is not baked into the binary: the CLI releases on its own
cadence, so a stamp would pin every install to whatever was current when the CLI was
built, and an older CLI could never install today's Miabi. To choose a version yourself
— or to install with no network at all:

- `--version 1.8.0` (a leading `v` is fine) swaps **only the tag**, keeping the current
  registry and repository — so it stays correct on a private registry, and on components
  that are not `miabi/miabi`: `miabi upgrade miabi-gateway --version 0.14.0`.
- `--image <ref>` replaces the reference outright. The two are mutually exclusive.

A floating tag (`latest`, `edge`, `main`, …) **warns**, and not on style grounds: the
rollout skips its automatic rollback when the previous reference equals the new one, so a
failed `:latest` upgrade has nothing to return to — and drift against it cannot be
detected, so the next upgrade reports "already at" and does nothing.

### Databases

Managed database instances (PostgreSQL, MySQL, MariaDB, Redis, MongoDB, libSQL)
and the logical databases hosted on them. Instances are addressed by **slug**
(or numeric id):

```
miabi db ls                                   # list instances
miabi db engines                              # engines + default versions
miabi db create shop --engine postgres [--version 16] [--size-mb 2048] [--node <id>]
miabi db get shop
miabi db start|stop|restart shop
miabi db logs shop [--follow] [--tail 200]
miabi db credentials shop                     # reveal admin connection (admin)
miabi db upgrade shop --to 17 [--stop-apps]
miabi db rm shop [--yes]
# logical databases on an instance:
miabi db databases shop                       # list
miabi db databases create shop app_prod [--app web]   # optionally attach to an app
miabi db databases connection shop app_prod   # reveal connection (admin)
miabi db databases rm shop app_prod [--yes]
```

### Secrets

The workspace **vault**: values encrypted at rest, write-only over the API, and
referenced from an app's env as `${{ secrets.NAME }}`. Secrets are addressed by
**name** (or numeric id). Supply a value with `--from-file` or stdin to keep it
out of your shell history.

```
miabi secrets ls                              # list secrets (no values)
miabi secrets get API_KEY                     # details: description, version, created/updated
miabi secrets set API_KEY --from-file api.key # create, or rotate if it exists
cat api.key | miabi secrets set API_KEY --from-file -
miabi secrets set API_KEY --description "..." # keep the value, edit metadata
miabi secrets reveal API_KEY                  # print the value (admin; audited)
miabi secrets usage API_KEY                   # apps referencing it
miabi secrets rm API_KEY [--yes]
```

`[web]` is optional when an app is bound with `miabi use`. Deployments and
releases are addressed by their **per-app number/version** (the `NUMBER` /
`VERSION` columns), not the global platform id. Shell completion (`miabi
completion <shell>`) tab-completes app slugs.

### Declarative apply

`miabi apply` converges a workspace to a bundle of `miabi.io/v1` manifests (the same
contract GitOps uses). See [`docs/stack.yaml`](docs/stack.yaml) for a complete, valid
example (volume, generated secret, Postgres, app with mounts + `{{ .databases.* }}` /
`{{ .secrets.* }}` interpolation, domain, route):

```bash
miabi apply -f docs/stack.yaml --dry-run  # preview the plan (+ creates, ~ updates, - deletes)
miabi apply -f app.yaml -f db.yaml        # multiple files → one bundle
cat stack.yaml | miabi apply -f -         # stdin
miabi apply -f docs/stack.yaml --prune    # also delete managed resources absent from the bundle
```

It prints the plan, applies it, and exits non-zero if any resource fails to converge.

`miabi delete -f` is the inverse: it removes exactly the resources the bundle names
(in dependency-safe order, dependents first), skipping any that don't exist:

```bash
miabi delete -f docs/stack.yaml --dry-run   # preview what would be deleted
miabi delete -f docs/stack.yaml             # delete them
```

Each document is `{ apiVersion: miabi.io/v1, kind, metadata: { name }, spec: { … } }`.
Kinds: `Application`, `Stack`, `Database`, `Volume`, `Secret`, `Route`, `Domain`,
`Project`. Names match `^[a-z0-9][a-z0-9-]*$` (Domain names are FQDNs); use a
hyphen-free name for anything referenced via dotted `{{ .secrets.<name> }}` /
`{{ .databases.<name>.* }}` interpolation.

- The app argument is a **slug** (or numeric id); the workspace comes from
  `--workspace`, the active workspace, or a workspace‑bound token.
- `-o json|yaml` (or the `--json` shorthand) gives `jq`/`yq`‑friendly output;
  human tables otherwise. Color auto‑disables off a TTY, with `--no-color`, or
  when `NO_COLOR` is set.
- `--verbose` logs every HTTP request to stderr.

## AI agents (MCP)

`miabi mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io) server so an
AI agent (Claude Desktop, Claude Code, Cursor, …) can inspect and operate your panel. It
turns each tool call into one authenticated `/api/v1` request — the agent inherits your
token, workspace, and RBAC. **No model runs inside `miabi`;** you bring your own AI client.

It exposes three MCP surfaces:

- **Tools** — `list`/`get` apps, deployments, releases, databases, and secret *names*.
  **Read-only by default**; `--allow-write` adds the mutating tools (`deploy_app`,
  `restart_app`, `start_app`, `stop_app`, `rollback_app`), annotated as destructive so
  clients prompt before calling them. Secret *values* are never returned.
- **Resources** — apps and deployments as `miabi://workspaces/{ws}/apps/{app}[/deployments/{n}]`
  URIs an agent can attach as context.
- **Prompts** — ready-made diagnostics: `diagnose_deployment`, `app_health`,
  `workspace_overview`.

Transport is **stdio** by default; pass `--http 127.0.0.1:8765` to serve over HTTP instead
(loopback-only origins are enforced to prevent DNS-rebinding).

```bash
# Register with Claude Code (read-only), using your current login/context:
claude mcp add miabi -- miabi mcp

# Allow the agent to deploy, restart and roll back:
claude mcp add miabi -- miabi mcp --allow-write
```

For **Claude Desktop**, add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "miabi": {
      "command": "miabi",
      "args": ["mcp"],
      "env": { "MIABI_SERVER": "https://panel.example.com", "MIABI_TOKEN": "…" }
    }
  }
}
```

The server picks up the same connection config as every other command (`--server`/`--token`
flags, `MIABI_*` env, or a saved context), and defaults tool calls to your active workspace
when the agent doesn't name one.

## CI example (GitHub Actions)

```yaml
- run: |
    go install github.com/miabi-io/cli@latest
    miabi apps deploy web --tag "${{ github.sha }}" --wait
  env:
    MIABI_SERVER:   ${{ vars.MIABI_SERVER }}
    MIABI_TOKEN: ${{ secrets.MIABI_DEPLOY_TOKEN }}
```

`--wait` makes the step fail when the deployment fails.

## Notes on the current API

A few server capabilities the long‑term design assumes are not yet in the panel; the
CLI adapts client‑side so it works against today's API:

- **`current` addressing** isn't in the URL scheme yet; the CLI addresses a
  workspace by its **name** in the URL and resolves an **app slug → numeric id**
  before each call.
- **Server‑side `wait`** isn't available, so `--wait` **polls** the deployment status
  until it is terminal.
- **`--image`** override and an **`Idempotency‑Key`** for retry‑safe deploys depend on
  upcoming machine‑API work; `--tag` (the common CI flow) is supported today.


---

## License

Apache License 2.0

## Copyright

Copyright (c) 2026 Jonas Kaninda
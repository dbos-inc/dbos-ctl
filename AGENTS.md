# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

**This is a living design doc.** The Roadmap below is the source of truth for what is built and what is not. When you land code, update the roadmap checkboxes and correct any section that has drifted — a stale description here is worse than no description.

## Repository state

`dbos-cli` is a Go CLI that wraps the **DBOS Conductor v2 API**. As of the last update it is still an unmodified `cobra-cli init` scaffold (single commit, "scaffold"): only `main.go`, `cmd/root.go`, and `cmd/version.go` exist, and their `Short`/`Long` strings are Cobra's placeholder text. Replace that boilerplate as commands land rather than copying it into new files.

Licensed MIT (`LICENSE`, copyright DBOS, Inc., matching `~/ts-transact/LICENSE`); this repo is intended to be public, so keep README and help text publishable.

**The invoked command is `dbos`**, not `dbos-cli`. The repo, module (`github.com/dbos-inc/dbos-cli`), and built artifact keep the `dbos-cli` name; cobra's `Use:` in `cmd/root.go` must say `dbos`, and all docs and examples use `dbos`.

> ⚠️ **Known name collision.** The DBOS Python SDK ships its own `dbos` console script. A user with both installed gets whichever comes first on `PATH`, with no warning and no error — the failure looks like "my flags stopped working". Mitigations to weigh when packaging: make `dbos version` unambiguous about which binary it is, and document the conflict in install instructions. Revisit if the two CLIs ever need to coexist by design.

## Commands

```bash
go build -o dbos-cli .         # build (binary is gitignored)
go run . version               # run a subcommand without building
go test ./...                  # unit tests only — no Docker required
go test -run TestName ./cmd    # single test
go vet ./... && gofmt -l .

make generate                  # regenerate the API client from the vendored spec
make spec                      # re-vendor the spec from a local conductor checkout
make test-integration          # container-backed tests (see Testing)
```

`go.mod` declares `go 1.26` — a *minimum*, not a build pin. Under the default `GOTOOLCHAIN=auto` an older local toolchain silently downloads a matching one; under `GOTOOLCHAIN=local` (common in CI images and air-gapped builds) it's a hard error. Pin an exact patch version in CI/Docker config, not in `go.mod`.

## The three deployment modes

This is the central complexity. Conductor runs three ways, and **the mode changes which endpoints exist**, not merely how requests are authenticated:

| Mode | Base URL | Auth | Org | Identity endpoints |
|---|---|---|---|---|
| `selfhosted` (no auth) | `http://host:8090` | none | always `local` | **absent** |
| `oauth` (self-hosted + OIDC) | `http://host:8090` | user JWT or `dbos_` key | real | `/v2/users*` |
| `cloud` | `https://cloud.dbos.dev/conductor` | Auth0 JWT or `dbos_` key | real | **cloud `/v1alpha1/user*`** |

Evidence, in the sibling `~/conductor` checkout:

- `controllers/apiv2/router.go:140-145` registers identity, role, and audit routes **only** when `cfg.OAuthEnabled`. In no-auth mode `/v2/users*`, `/v2/orgs/{org}` (get/update), `join`, `secrets`, `members`, `roles`, and `audit-logs` do not exist.
- `main.go:160-201` (`EnsureLocalOrgAndUser`) hardcodes org and user `local` when OAuth is off.
- `middleware/apikey_authentication.go` keys off the `dbos_` prefix on the bearer token to distinguish API keys from JWTs; the API-key middleware is only registered when OAuth is on.

Consequence for the CLI: a command that hits a route absent in the current mode must fail with a mode-aware message ("not available without OAuth"), never a raw 404.

## Cloud specifics

- **Proxying is a pure prefix strip.** `~/cloud/controllers/public/applications/route.go:373-375` maps `/conductor/v2/orgs/{org}/…` → `/v2/orgs/{org}/…`, org name verbatim. So cloud mode is just base URL + `/conductor`, and the generated `/v2/orgs/{orgName}/...` paths work unchanged. The proxy group is only `/conductor/v2/orgs/:orgName/*path`, which is exactly why `/v2/users*` is unreachable on cloud.
- **Cloud's user endpoints replace them, and are quirky.** Hand-write this client; cloud's own swagger (`~/cloud/docs/public/public_swagger.json`) is stale and documents the wrong paths for these very endpoints.
  - `GET /v1alpha1/user/profile` → `models.ProfileUserResponse` (`~/cloud/models/users.go:27-35`) with **PascalCase** JSON tags (`Name`, `Email`, `Organization`, `SubscriptionPlan`, `CreatedAt`).
  - `PUT /v1alpha1/user` → snake_case request; the response body is a **raw UUID string, not JSON** (`~/cloud/controllers/public/users/users.go:82-83`).
  - Auth0 JWT only — `dbos_` keys are rejected on `/v1alpha1/*`.
  - Conductor's equivalent `UserProfile` is camelCase with `orgName`/`orgId`. `internal/identity` normalizes the two shapes.
- **Errors** are RFC 7807 `application/problem+json` (`ErrorModel`: `title`/`status`/`detail`/`errors`/`instance`). A past-limit org gets `403` + header `X-DBOS-Error: past_limit`, passed through unchanged by the proxy (`route.go:124-140`) — surface it as an actionable upgrade message.
- **Auth0 values** (`~/ts-transact/packages/dbos-cloud/users/authentication.ts:7-11`): domain `login.dbos.dev` (prod) / `dbos-inc.us.auth0.com` (staging), client ID `6p7Sjxf13cyLMkdwn14MxlH7JdhILled` / `G38fLmVErczEo9ioCFjVIHea6yd0qMZu`, audience `dbos-cloud-api`, device authorization grant.

## Code generation

The Conductor client is **generated, not hand-written**. Do not edit anything in `internal/api/`.

Conductor emits a 3.0 spec directly — `dbos-conductor openapi -spec-version 3.0` (`~/conductor/main.go:254`). Verified output: OpenAPI 3.0.3, 49 paths / 57 operations / 55 schemas, clean camelCase `operationId`s, proper `nullable: true`, and zero `oneOf`/`anyOf`/`allOf`/`discriminator`. The default (3.1) output is *not* used — `oapi-codegen` is 3.0-only.

- Generator: `oapi-codegen`, added as a `go tool` dependency in `go.mod` (matching the pattern conductor uses for goimports/golangci-lint) so `make generate` needs no external toolchain.
- Config: `generate: {models: true, client: true}`, package `api`.
- The spec is **vendored** at `internal/api/openapi-3.0.json` and generated code is **committed**. CI runs `make generate && git diff --exit-code` so spec drift fails loudly — the guard against the v2 API still moving pre-ship.
- **Target the released v2 API only.** The CLI ships after conductor v2 does, so there is exactly one API version to support — no compatibility shims, no version negotiation, no pinning to a spec revision. During the interim, conductor's v2 work sits on `devhawk/v2-api` and cloud's proxy on `devhawk/conductor-v2-api`; `make spec` reads whatever the local checkout has, so keep it on the right branch until they merge, then regenerate from `main` and forget the branches ever existed. The CI drift check is the guard that this actually happened.
- Known cosmetic wart: huma stamps a read-only `$schema` property on ~40 schemas, so generated structs carry an unused `Schema *string`. Ignore it.
- The stray `openapi.json` at the repo root is a gitignored 3.1 artifact predating this pipeline; it is not used by the build.

## Architecture

Standard Cobra layout: `main.go` calls `cmd.Execute()`, which runs the package-level `rootCmd`. Each subcommand lives in its own file under `cmd/` as a package-level `*cobra.Command` var, registered by that file's `init()` via `rootCmd.AddCommand(...)` — there is no central registration list. Flags are declared in the same `init()`; persistent flags belong on `rootCmd`. `Execute()` exits 1 on error and Cobra prints the error, so `RunE` functions should return errors rather than printing and exiting.

```
cmd/                 root, version, login, logout, profile, config, workflow, queue, app
internal/
  api/               vendored spec + generated client — do not hand-edit
  cloud/             hand-written /v1alpha1/user client
  identity/          Provider interface unifying conductor and cloud user ops
  config/            ~/.dbos/config.yaml profiles
  creds/             ~/.dbos/credentials.json (0600)
  auth/              OIDC discovery + device flow + refresh
  client/            profile -> configured api.ClientWithResponses
  output/            table (text/tabwriter) and JSON rendering
```

**Config and credentials.** A profile is `{name, mode, url, org, app?, oidc?: {issuer, audience, clientID}}` plus a `current` pointer, in `~/.dbos/config.yaml` (YAML because humans edit it and comments survive; `gopkg.in/yaml.v3`, not viper). Precedence: flag > env (`DBOS_PROFILE`, `DBOS_URL`, `DBOS_ORG`, `DBOS_APP`, `DBOS_TOKEN`) > profile. Cloud profiles default the Auth0 values above so no hand-written OIDC block is needed.

Credentials live in `~/.dbos/credentials.json` keyed by profile, mode `0600` (JSON because it is machine-written only). **Decided: keep a read-only fallback** to the TS CLI's cwd-relative `./.dbos/credentials`, so an existing `dbos-cloud` login carries over without re-authenticating. Read that file, never write it — the CLI surface is deliberately incompatible with the TS CLI, but the credential *format* compatibility is worth the ~30 lines. Its cwd-relative location is not reproduced for our own file; it breaks the moment you `cd`.

**Auth.** One code path serves both `oauth` and `cloud` modes: OIDC discovery at `{issuer}/.well-known/openid-configuration` → device authorization grant → poll the token endpoint honoring `interval`/`expires_in` → store. Refresh when a refresh token exists, otherwise re-prompt. `dbos_`-prefixed tokens skip the flow and are sent as-is. `internal/client` sets `Server` to `url` (self-hosted) or `url + "/conductor"` (cloud) and attaches a `RequestEditorFn` for the bearer header — omitted entirely in `selfhosted` mode.

**Identity.**

```go
type Provider interface {
    Profile(ctx) (*Profile, error)
    Register(ctx, RegisterInput) (string, error)
}
```

conductor impl → generated `GetCurrentUser`/`RegisterUser`; cloud impl → the hand-written `/v1alpha1` client; selfhosted impl → static `{Name: "local", OrgName: "local"}` with `Register` returning a clear unsupported error.

## Command surface

The tables below map **every** one of the v2 API's 57 operations to a command. The TypeScript `dbos-cloud` CLI (`~/ts-transact/packages/dbos-cloud/cli.ts:644-800`) is a reference for *which* workflow operations matter in practice, not a compatibility target: **the CLI surface is deliberately not compatible with it** — no aliases, no inherited flag names. Pick the better name. This CLI still does **not** replace `dbos-cloud` for app deploy, Postgres provisioning, dashboards, or env vars.

Status column: **v1** = first release, **v1.1** = fast follow, **defer** = build only on demand.

### Conventions

- **Bulk is variadic, not a separate verb.** `dbos workflow cancel ID1 ID2 ID3` calls `/cancel` for one ID and `/bulk-cancel` for several — the API split shouldn't leak into the CLI. A literal `-` reads IDs from stdin, so `dbos workflow list -o json | jq -r '.[].workflowId' | dbos workflow cancel -` works. `--children` maps to `cancelChildren`/`deleteChildren`.
- **Mutating commands take IDs positionally.** No `-w/--workflowid` flag.
- **Mode gating.** Commands marked *OAuth* do not exist in `selfhosted` mode (`controllers/apiv2/router.go:140-145`) — fail with the mode-aware message, not a 404. `audit list` additionally requires a Teams plan (`requireTeamPlan`, `controllers/apiv2/auth.go`).

**Global (persistent) flags**, declared once on `rootCmd`: `-o/--output`, `-a/--app`, `--org`, `--url`, `--profile`. App name resolves `-a/--app` → `DBOS_APP` → profile default → error naming the flag.

### Output

Response shapes across the 57 operations: **19 return arrays**, **18 return single objects**, **20 return no body**. `internal/output` is built around that split, not around one generic renderer.

| `-o` | Behavior | Status |
|---|---|---|
| `table` (default) | Aligned columns (`text/tabwriter`) for array responses; **key/value detail view** for single objects — `Workflow` has 29 fields and is unreadable as columns | v1 |
| `json` | Raw API shape, never truncated | v1 |
| `ids` | Bare identifiers, one per line | v1 |
| `csv` | Report-shaped endpoints: `workflow stats`, `step-stats`, `audit list`, `app metrics` | v1.1 |

`ids` exists to make the variadic/stdin design self-sufficient: `dbos workflow list -o ids | dbos workflow cancel -` with no `jq` in the pipeline.

Deliberately **not** supported: `yaml` (a dependency for no config-file workflow that would justify it) and Go templates / `--format` (`json` + `jq` covers it far more cheaply). Extra columns are `--columns`, not an `-o wide` format.

Rules that hold regardless of format:

- **The 20 no-body operations print nothing on success and exit 0.** Confirmations go to **stderr** so they cannot pollute a pipe.
- **Seven operations return a single scalar** — `forkWorkflow`/`triggerSchedule` → `workflowId`, `createToken` → `token`, `generateSecret` → `secret`, `exportWorkflow` → `serializedWorkflow`, `registerUser` → `id`, plus `backfillSchedule`/`bulkForkWorkflowsFromFailure` → `workflowIds[]`. Print the bare value(s), unlabeled and undecorated, so `$(dbos schedule trigger nightly)` is directly usable.
- **`workflow export` emits the serialized payload verbatim** regardless of `-o`; it is a data blob, not a rendered object.
- **`table` truncates `input`/`output` payloads** (present only with `--load-input`/`--load-output`); `json` never truncates.
- **Color off when stdout is not a TTY**, and honor `NO_COLOR`. Never change the *format* based on TTY — silently reshaping output under a pipe breaks scripts.

**Shorthands are a global namespace — check for collisions before adding one.** `-o` is `--output`, so paging is `--limit`/`--offset` with no shorthand on offset. `-v` is left free for `--verbose` and must **not** be taken by `--app-version`. Cobra fails at startup on a duplicate shorthand, so a collision is caught, but only once that command runs.

### Workflows

`GET .../workflows` accepts only `status`, `workflowName`, `limit`, `offset`, `sortDesc`, `loadInput`, `loadOutput`. Every other filter (user, start/end time, workflow IDs, app version) exists only on `POST .../workflows/search`, so `list` is backed by `search`.

| Command | Operation | Status |
|---|---|---|
| `workflow list` | `searchWorkflows` | v1 |
| `workflow get <id>` | `getWorkflow` | v1 |
| `workflow steps <id>` | `listWorkflowSteps` | v1 |
| `workflow cancel <id...>` | `cancelWorkflow` / `bulkCancelWorkflows` | v1 |
| `workflow resume <id...>` | `resumeWorkflow` / `bulkResumeWorkflows` (`--queue`) | v1 |
| `workflow restart <id>` | `restartWorkflow` | v1 |
| `workflow fork <id...>` | `forkWorkflow` / `bulkForkWorkflowsFromFailure` | v1 |
| `workflow delete <id...>` | `deleteWorkflow` / `bulkDeleteWorkflows` | v1 |
| `workflow events <id>` | `listWorkflowEvents` | v1 |
| `workflow notifications <id>` | `listWorkflowNotifications` | v1.1 |
| `workflow streams <id>` | `listWorkflowStreams` | v1.1 |
| `workflow export <id>` | `exportWorkflow` (`--children`) | v1.1 |
| `workflow import` | `importWorkflow` | v1.1 |
| `workflow stats` | `getWorkflowAggregates` | v1.1 |
| `workflow step-stats` | `getStepAggregates` | v1.1 |

Flags for `list`, named for this CLI rather than inherited: `-l/--limit`, `--offset`, `--id` (repeatable), `-u/--user`, `-s/--status`, `-n/--name`, `--since`, `--until`, `--app-version`, `--desc`, `--queued`, `--queue <name>`. `--since`/`--until` should accept both ISO 8601 and relative durations (`--since 1h`), mapping to the body's `startTime`/`endTime`.

`fork` carries the bulk body's options as flags: `--from-step`, `--from-step-name`, `--from-last-step`, `--from-last-failure`, `--app-version`, `--queue`, `--queue-partition-key`.

**export/import should round-trip.** `exportWorkflow` returns `{serializedWorkflow}` and `importWorkflow` takes `{serializedWorkflow}`. Have `export` emit the inner value verbatim and `import` re-wrap it, so `dbos workflow export X | dbos workflow import` just works — copying a workflow between environments is the whole point of the pair.

**`stats` is the highest-value addition with no TS equivalent.** `getWorkflowAggregates` takes the full search filter set plus `groupBy{Status,WorkflowName,QueueName,ExecutorId,AppVersion}`, `select{Count,MaxQueueWaitMs,MaxTotalLatencyMs,MinCreatedAt}`, and `timeBucketSizeMs`. Surface as `--group-by status,workflowName` + `--bucket 5m` + `--select count,maxLatency` rather than one flag per boolean.

### Apps, queues, schedules

| Command | Operation | Status |
|---|---|---|
| `app list` | `listApps` | v1 |
| `app get <name>` | `getApp` | v1 |
| `app versions <name>` | `listAppVersions` | v1 |
| `app executors <name>` | `listExecutors` | v1 |
| `app metrics <name>` | `listMetrics` (`--start-time`/`--end-time`) | v1.1 |
| `app set-version <name> <ver>` | `setLatestAppVersion` | v1.1 |
| `app update <name>` | `updateApp` | v1.1 |
| `app register <name>` | `registerApp` | defer |
| `app delete <name>` | `deleteApp` | defer |
| `queue list` | `listQueues` | v1 |
| `queue get <name>` | `getQueue` | v1 |
| `schedule list` | `listSchedules` | v1 |
| `schedule get <name>` | `getSchedule` | v1 |
| `schedule pause\|resume <name>` | `pauseSchedule` / `resumeSchedule` | v1 |
| `schedule trigger <name>` | `triggerSchedule` → prints new `workflowId` | v1 |
| `schedule backfill <name>` | `backfillSchedule` (`--start-time`/`--end-time` required) | v1.1 |
| `alert list\|create\|delete` | `listAlertingRules` / `createAlertingRule` / `deleteAlertingRule` | v1.1 |

**Resolved naming.** `queue list`/`queue get` are unambiguously about queue *definitions* (`GET .../queues`). Enqueued workflows are `workflow list --queued` (`search` with `queuesOnly: true`), optionally narrowed by `--queue <name>` — they're workflows, so they belong under `workflow`. There is no `workflow queue list` subcommand; the TS CLI's nesting was the thing that made the two concepts collide.

`app update` is the tuning surface (`executorTimeoutSecs`, `gcRowsThreshold`, `gcTimeThresholdMs`, `globalTimeoutMs`, `privateMode`) — worth having for self-hosted operators. `app register` takes only `privateMode`; apps normally self-register through the SDK, which is why it's deferred.

### Tokens, org, roles

| Command | Operation | Status | Mode |
|---|---|---|---|
| `token list` | `listTokens` | v1 | all |
| `token create <name>` | `createToken` (`--app`, `--permission`) | v1 | all |
| `token delete <name>` | `deleteToken` | v1 | all |
| `login` / `logout` / `profile` | device flow / `getCurrentUser` | v1 | all |
| `register` | `registerUser` | v1 | OAuth, cloud |
| `org get` / `org update` | `getOrg` / `updateOrg` | v1.1 | OAuth |
| `org members` | `listMembers` | v1.1 | OAuth |
| `org remove-member <user>` | `removeMember` | v1.1 | OAuth |
| `org join <org>` | `joinOrg` | v1.1 | OAuth |
| `org invite` | `generateSecret` → the secret `register` consumes | v1.1 | OAuth |
| `org grant-role <user> <role>` | `grantRole` | v1.1 | OAuth |
| `role list\|create\|delete` | `listRoles` / `createRole` / `deleteRole` | v1.1 | OAuth |
| `role permissions` | `listPermissions` | v1.1 | OAuth |
| `audit list` | `listAuditLogs` | v1.1 | OAuth + Teams |

**`token create` deserves priority.** It returns `{token, tokenName}` — the plaintext `dbos_` key, shown once — and scopes it with `appNames` and `permissions`. That makes the CLI self-sufficient for CI setup: log in interactively once, mint a scoped key, drop it in a secret. It works in every mode because `RegisterTokenRoutes` is registered unconditionally (`controllers/apiv2/router.go:146`).

## Testing

Container-backed tests use **testcontainers-go** (core + `modules/postgres`, both v0.43.0). Tests are tiered because the upper tiers need secrets and network:

**Tier 1 — unit, no Docker.** `go test ./...`. Config precedence, credential round-trip and file mode, TS-credentials fallback parsing, device-flow polling against an `httptest` OIDC server, error mapping (problem+json / 401 / past-limit / mode-missing-route). This tier must stay green with no Docker daemon.

**Tier 2 — Keycloak container, no secrets.** Real device-flow tests against a real OIDC provider. **There is no official testcontainers Keycloak module** — use `testcontainers.GenericContainer` with `quay.io/keycloak/keycloak`, command `start-dev --import-realm`, and a realm fixture committed at `internal/auth/testdata/dbos-realm.json` defining a public client with `oauth2.device.authorization.grant.enabled: "true"` plus a test user. (`github.com/stillya/testcontainers-keycloak` v0.3.8 exists as a third-party fallback; hand-rolling is ~30 lines and avoids the dependency.) Owning the realm fixture here means CLI auth tests do **not** depend on conductor's realm, whose only client (`~/conductor/docker/keycloak/dbos-realm.json`, `dbos-console`) does not enable the device grant.

**Tier 3 — conductor + Postgres containers.** The CLI never talks to Postgres; it exists only to back conductor. Its `docker/entrypoint.sh` runs migrations automatically. Wait on `/healthz`. Covers the real command surface end to end in `selfhosted` mode.

Source the conductor container **two ways, selected by env**, so the eventual switch is config rather than a rewrite:

- `CONDUCTOR_IMAGE` set → pull that image. Preferred once available.
- otherwise → `FromDockerfile{Context: $CONDUCTOR_DIR, Dockerfile: "docker/Dockerfile"}`, default `~/conductor`. Required today.

> A public image exists — **`dbosdev/conductor`** on Docker Hub (multi-arch amd64/arm64, tags `latest`/`0`/`0.15`/`0.15.0` back to `0.6.0`) — but **no published tag contains the v2 API**: every tag is built from `main`, where `controllers/apiv2` and the `openapi` subcommand do not exist. The publish workflow (`.github/workflows/docker-publish.yml`) is `workflow_dispatch`-only and has never produced a `dev-*` tag. Once v2 merges and a release is cut, set `CONDUCTOR_IMAGE=dbosdev/conductor:<tag>` and drop the checkout dependency from CI entirely. Before then, manually dispatching that workflow on the v2 branch would publish `dev-<shortsha>` and unblock image-based testing early.

> **Gate:** conductor **panics without `DBOS_CONDUCTOR_LICENSE_KEY`** (`~/conductor/config/config.go:206-208`). Use the **local** key, not the cloud one: `validateLicenseKey` short-circuits to "pro" when the key's SHA-256 matches a hardcoded entry in `localKeyHashes`, so validation is entirely offline. Any other key triggers a startup call to `https://cloud.dbos.dev/v1alpha1/conductor-api-keys/check` on every container boot. Skip the tier when the key or `CONDUCTOR_DIR` is unset — never fail the suite for a missing secret (fork PRs don't get secrets).

**Secrets.** Same pattern as the conductor repo: a gitignored `.env` locally, a GitHub Actions secret in CI.

- Local: `cp .env.example .env` and fill in `DBOS_CONDUCTOR_LICENSE_KEY` — the value is the `CONDUCTOR_LOCAL_LICENSE_KEY` line in `~/conductor/.env`. The integration helper loads `.env` from the repo root via `github.com/joho/godotenv` (a test-only dependency), which does not override variables already set in the environment — so both `make test-integration` and a bare `go test -tags integration ./...` work. Keep `.env` in godotenv format (`KEY=value`, no `export` prefix); conductor's own `.env` uses the `export` form because it's meant to be `source`d.
- CI: `DBOS_CONDUCTOR_LICENSE_KEY: ${{ secrets.CONDUCTOR_LOCAL_LICENSE_KEY }}`, reusing conductor's secret name (`~/conductor/.github/workflows/test.yml:56`).
- `.env` is gitignored here and in conductor. Never commit a key value, and don't paste one into this file.

**Tier 4 — full OIDC end-to-end (optional, manual).** CLI → conductor(OAuth) → Keycloak. Deferred because of an issuer-alias problem: Keycloak mints tokens with a fixed `iss`, conductor validates `iss` against `DBOS_OAUTH_ISSUER`, and discovery advertises endpoints at that same host — so if conductor reaches Keycloak by network alias while the CLI uses a host-mapped port, the CLI cannot resolve the advertised URLs. Mitigation is a shared network with alias `keycloak`, `KC_HOSTNAME` set to match, and a host `/etc/hosts` entry. Tiers 2 and 3 together cover the same ground with far less setup.

Guard tiers 2–4 with `//go:build integration` and skip on missing prerequisites. `make test` runs tier 1; `make test-integration` runs the rest.

## Roadmap

Sliced so that **every step ends with a binary you can run and a test that proves it** — no step leaves the tree in a state where the only verification is "it compiles". Each is roughly one PR. Steps within a milestone are ordered; milestones D and E parallelize.

**A — walking skeleton.** Goal: one real command against a real conductor, as early as possible.

- [ ] **A1. Codegen** — vendor `internal/api/openapi-3.0.json`, `oapi-codegen` as a `go tool` dep, `Makefile` (`spec`/`generate`/`build`/`test`/`lint`), commit generated code, CI drift check. *Done when:* `make generate` is idempotent and the generated client compiles.
- [ ] **A2. Root wiring** — `Use: "dbos"`, global flags (`-o/--output`, `-a/--app`, `--org`, `--url`, `--profile`), real `dbos version`. No config file yet; `--url`/`--org` only. *Done when:* `dbos version` and `dbos --help` are correct.
- [ ] **A3. Transport + output, minimal** — `internal/client` building the generated client from `--url` with no auth; `internal/output` with `table` (array renderer) and `json`. *Done when:* unit tests cover both renderers.
- [ ] **A4. `dbos app list`** — the first end-to-end command. *Done when:* it returns real rows from a locally running conductor.
- [ ] **A5. Conductor test harness (tier 3)** — testcontainers Postgres + conductor on a shared network, conductor sourced via `CONDUCTOR_IMAGE` or built `FromDockerfile` (see Testing), `/healthz` wait strategy, `.env` loader via godotenv, skip-on-missing-secret, CI wiring with `CONDUCTOR_LOCAL_LICENSE_KEY`. *Done when:* `make test-integration` proves A4 against a container. **Pulled early on purpose** — every later command reuses it, so building it now makes the rest of the roadmap self-verifying.

**B — configuration.**

- [ ] **B1. Profiles** — `internal/config`, precedence flag > env > profile, `dbos config list|use|set|show`.
- [ ] **B2. Credential store** — `internal/creds`, 0600, per-profile, read-only `./.dbos/credentials` fallback.

**C — auth.** Each step is independently testable; C1 needs no conductor at all.

- [ ] **C1. OIDC device flow** — `internal/auth` discovery + device grant + polling, plus the tier-2 Keycloak container and realm fixture. *Done when:* a token is obtained from a real Keycloak with no conductor involved.
- [ ] **C2. Auth wiring** — bearer injection, `dbos_` passthrough, refresh-on-expiry, `dbos login`/`logout`.
- [ ] **C3. Error mapping** — `problem+json`, 401 → "run `dbos login`", 403 + `X-DBOS-Error: past_limit`, mode-missing-route. Deliberately after auth, since 401/403 are the cases hardest to fabricate.
- [ ] **C4. Identity** — `internal/identity` Provider with three impls, `internal/cloud` hand-written `/v1alpha1` client, `dbos profile`/`register`.

**D — v1 command surface.** Each step is a small PR over the A-milestone plumbing.

- [ ] **D1. Workflow reads** — `list` (search-backed, full filter set), `get`, `steps`, `events`; single-object detail renderer.
- [ ] **D2. Workflow mutations** — `cancel`/`resume`/`restart`/`fork`/`delete`, variadic + stdin `-`, single-vs-bulk dispatch, `-o ids`.
- [ ] **D3. Apps, queues, schedules (read)** — `app get|versions|executors`, `queue list|get`, `schedule list|get`.
- [ ] **D4. Schedule mutations** — `pause`/`resume`/`trigger`, scalar-output rule.
- [ ] **D5. Tokens** — `token list|create|delete`; `create` prints the bare secret once.

**E — v1.1.** Independent of each other; pick by demand.

- [ ] **E1. Aggregates** — `workflow stats`, `step-stats`, `--group-by`/`--bucket`/`--select`, `csv` output.
- [ ] **E2. Export/import** — round-trip via stdout/stdin.
- [ ] **E3. Metrics, alerts, backfill** — `app metrics`, `alert list|create|delete`, `schedule backfill`.
- [ ] **E4. OAuth-gated set** — `org *`, `role *`, `audit list`, with mode gating enforced.
- [ ] **E5. Full OIDC e2e (tier 4)** — only if the issuer-alias setup proves worth it.

## Known risks

1. **Cloud refresh tokens may not be issued.** `~/cloud/auth0/tenant.yaml:8-16` sets `allow_offline_access: false` on `dbos-cloud-api` while the TS CLI requests `offline_access` anyway. If Auth0 declines, users re-login every 24h (token lifetime 86400s). Determine empirically in phase 3; the credential store already tolerates a missing refresh token.
2. **The v2 API has not shipped yet, but will ship before this CLI does.** Until then the spec can still shift under us; the vendored spec plus the CI drift check makes that a mechanical update rather than a silent breakage. This is a pre-merge inconvenience, not a long-term constraint — there will only ever be one released v2 API to support.
3. **Self-hosted OIDC needs a client ID the CLI cannot discover.** Profiles carry it. A future unauthenticated `GET /v2/config` on conductor returning `{mode, oauth:{issuer, audience, clientId}}` would let `dbos login --url X` self-configure and stop the CLI guessing which endpoints exist. Deferred, not designed away.
4. **Cloud's `/conductor/v2` proxy is branch-local** (`devhawk/conductor-v2-api`) and ships alongside conductor v2, ahead of the CLI. Cloud-mode testing against production has to wait for that deploy; local and self-hosted modes don't.

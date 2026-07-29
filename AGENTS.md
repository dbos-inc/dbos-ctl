# AGENTS.md

This file provides guidance to coding agents when working with code in this repository.

**This is a living design doc.** The Roadmap below is the source of truth for what is built and what is not. When you land code, update the roadmap checkboxes and correct any section that has drifted — a stale description here is worse than no description.

## Working agreement

- **Branches are the user's to create — do not create branches.** When we're ready to start a work stream, the user creates the feature branch; all work happens there.
- **Commit freely on the feature branch.** While on a feature branch, make commits as the work progresses.
- **Check the branch before any git write.** Run `git branch --show-current` before committing (or any write op). If it's `main`, **stop and ask** — never commit directly to `main`, even for a small fix. Don't assume the branch from earlier in the session; the user may have merged and switched back to `main` in between.
- **One work stream at a time, user-reviewed.** Each roadmap step is roughly one work stream / PR. When a stream is done, **stop** — the user reviews and merges into `main` before the next stream begins. Do not start the next roadmap step until the current one is merged.

## Repository state

`dbos-cli` is a Go CLI that wraps the **DBOS Conductor API**. The entrypoint is `cmd/dbos/main.go` (a thin `package main` calling `cli.Execute()`) and the command tree lives in `internal/cli` — build new commands there, each in its own file registering on `rootCmd` from its `init()`. `internal/api` holds the generated client + OAuth-gate table. Milestone **A** (walking skeleton — codegen, root wiring, transport/output, `app list`, and the container integration harness) is complete; milestone **B** (config: `internal/config` + `dbos config` profiles, and the `internal/creds` credential store) has landed; **C1** (`internal/auth` OIDC device flow) has landed. **No per-file copyright/license headers** — `LICENSE` (MIT, DBOS, Inc.) covers the whole repo, matching every sibling DBOS repo (`~/conductor`, `~/cloud`, `~/go-transact`); new `.go` files start straight at `package` (an optional package-doc comment is fine).

Licensed MIT (`LICENSE`, copyright DBOS, Inc., matching `~/ts-transact/LICENSE`); this repo is intended to be public, so keep README and help text publishable.

**The invoked command is `dbos`**, not `dbos-cli`. The repo and module (`github.com/dbos-inc/dbos-cli`) keep the `dbos-cli` name, but **the binary is `dbos`**. Go names a binary after the last path element of its `main` package, so the entrypoint lives at `cmd/dbos/` — that makes `go build ./cmd/dbos`, `go install github.com/dbos-inc/dbos-cli/cmd/dbos@latest`, and goreleaser all produce `dbos` with no renaming. Cobra's `Use:` in the root command must say `dbos`, and all docs and examples use `dbos`.

> ⚠️ **Known name collision.** The DBOS Python SDK ships its own `dbos` console script. A user with both installed gets whichever comes first on `PATH`, with no warning and no error — the failure looks like "my flags stopped working". Mitigations to weigh when packaging: make `dbos version` unambiguous about which binary it is, and document the conflict in install instructions. Revisit if the two CLIs ever need to coexist by design.

## Commands

```bash
go build -o dbos ./cmd/dbos    # build (binary is gitignored)
go run ./cmd/dbos version      # run a subcommand without building
go test ./...                  # unit tests only — no Docker required
go test -run TestName ./internal/cli   # single test
go vet ./... && gofmt -l .

make generate                  # regenerate the API client from the vendored spec
make spec                      # re-vendor the spec from a local conductor checkout
make test-integration          # container-backed tests (see Testing)
```

`go.mod` declares `go 1.25` — a *minimum*, not a build pin. Under the default `GOTOOLCHAIN=auto` an older local toolchain silently downloads a matching one; under `GOTOOLCHAIN=local` (common in CI images and air-gapped builds) it's a hard error. Pin an exact patch version in CI/Docker config, not in `go.mod`.

**Go version policy: support the two most recent Go releases**, matching [Go's own support window](https://go.dev/doc/devel/release#policy) (each release is supported until two newer ones exist, ~1 year). Set the `go` directive to the *older* of the two — `1.25` today, since `1.26` is current. The directive only constrains building **from source** (`go install`, distro packagers, contributors); prebuilt release binaries are unaffected, so there is no reason to chase the latest — a too-high floor just breaks `go install` for anyone one release behind with `GOTOOLCHAIN=local`. Bump the floor only when a specific language or stdlib feature requires it, and never above the oldest still-supported release.

## The three deployment modes

This is the central complexity. Conductor runs three ways, and **the deployment changes which endpoints exist**, not merely how requests are authenticated. Note these three are **named scenarios, not a config enum** — the profile encodes behavior as explicit axes (`auth` + `url` + `oidc`; see Config and credentials), and `selfhosted`/`oauth`/`cloud` are just convenient labels for common axis-combinations:

| Mode | Base URL | Auth | Org | Identity endpoints |
|---|---|---|---|---|
| `selfhosted` (no auth) | `http://host:8090` | none | always `local` | **absent** |
| `oauth` (self-hosted + OIDC) | `http://host:8090` | user JWT or `dbos_` key | real | `/v2/users*` |
| `cloud` | `https://cloud.dbos.dev/conductor` | Auth0 JWT or `dbos_` key | real | `/v2/users*` (proxied) |

The **Base URL** column assumes conductor-direct; a console-fronted self-host or cloud adds a path prefix (see *Container topology sets the URL prefix* below). Mode (auth/org/endpoint surface) and topology (URL prefix) are orthogonal knobs.

Evidence, in the sibling `~/conductor` checkout:

- `controllers/apiv2/router.go:140-145` registers identity, role, and audit routes **only** when `cfg.OAuthEnabled`. In no-auth mode `/v2/users*`, `/v2/orgs/{org}` (get/update), `join`, `secrets`, `members`, `roles`, and `audit-logs` do not exist.
- **The spec declares this, so the CLI doesn't hardcode it.** Each OAuth-only operation carries a custom `x-dbos-requires-oauth: true` extension — **14 operations** in the current spec (`createRole`, `deleteRole`, `generateSecret`, `getCurrentUser`, `getOrg`, `grantRole`, `joinOrg`, `listAuditLogs`, `listMembers`, `listPermissions`, `listRoles`, `registerUser`, `removeMember`, `updateOrg`). The CLI derives its gate set from this extension, not from a copy of the route list — so when conductor adds or removes a gated route, `make generate` picks it up and the CI drift check makes the change visible.
- `main.go:160-201` (`EnsureLocalOrgAndUser`) hardcodes org and user `local` when OAuth is off.
- `middleware/apikey_authentication.go` keys off the `dbos_` prefix on the bearer token to distinguish API keys from JWTs; the API-key middleware is only registered when OAuth is on.
- **Enabling `oauth` is a conductor-side switch, the same under every container topology.** Set `DBOS_OAUTH_ENABLED=true` plus `DBOS_OAUTH_ISSUER` / `DBOS_OAUTH_AUDIENCE` (and a JWKS URL) on the **conductor** service (`config/config.go:35-37,71-73`; both docker images ship these as commented env in their compose). That single switch flips `OAuthEnabled`, registers `/v2/users*`, and turns on JWT/`dbos_` validation. Conductor **validates** tokens (issuer/audience/JWKS) but issues none and holds **no interactive client ID** — only an M2M admin allow-list (`AUTH0_MGMT_CLIENT_ID`, `config.go:79`) — so the CLI must carry the device-flow client ID itself (risk #3). The `~/console` image adds a **separate, browser-only** OAuth config (`DBOS_OAUTH_AUTHORIZATION_URL`/`TOKEN_URL`/`CLIENT_ID`/`USERINFO_URL`/… → `env-config.js` + the nginx `/oauth-proxy/*` locations, an authorization-code flow for the SPA); it does **not** gate the API and is irrelevant to the CLI. Enabling console OAuth without also enabling conductor OAuth leaves the API unauthenticated.

Consequence for the CLI: a command whose operation is `x-dbos-requires-oauth` fails **before the request** in `selfhosted` mode with a mode-aware message ("not available without OAuth"), never a raw 404 — the spec-derived gate set makes this a lookup, not a guess.

### Container topology sets the URL prefix

Orthogonal to mode is **how conductor is reached**, which fixes the path prefix the client prepends to every `/v2/...` route. Self-hosters run one of two images (`~/conductor` bare, or `~/console` which puts an nginx in front), giving three topologies:

| Topology | Deployed as | CLI `url` (base) | conductor receives |
|---|---|---|---|
| conductor-direct | `~/conductor` image, port `:8090` | `http://host:8090` | `/v2/...` (no prefix) |
| console-fronted | `~/console` image (nginx) ahead of conductor | `http://host/api/conductor` | strip `/api/conductor` → `/v2/...` |
| cloud | cloud reverse proxy | `https://cloud.dbos.dev/conductor` | strip `/conductor` → `/v2/...` |

So the base path is **just part of the profile's `url`**, configurable per profile — not a `cloud`-only `/conductor` special case. The client uses `url` verbatim as its `Server`; `""`, `/conductor`, and `/api/conductor` are three values of one knob (console nginx: `rewrite ^/api/conductor/(v2/.*)$ /$1`, `~/console/docker/nginx.conf`).

Two caveats on the console image, both **in progress** — this lives on the `lanice/conductor-api-v2` branch and the API is merged in the conductor/cloud repos but **not yet deployed to staging or production**:

- It currently exposes the API **only at the browser's `/api/conductor/v2/` path**, and **that rewrite is not settled** — the console team may change the prefix or add a clean `/conductor/v2/` external ingress mirroring cloud's (today the only other ingress is the unchanged `/conductor/v1alpha1/` Transact-SDK one). This is exactly why the base path is a **single profile field**: whatever the console lands on, adapting is a one-line config change, not a code change. Treat `/api/conductor` as a provisional default, not a committed contract.
- The console compose still publishes conductor on `:8090` directly, so **conductor-direct stays available alongside a console deployment** unless the operator firewalls it — a profile can point at either the console prefix or `:8090`.

## Cloud specifics

**Upshot: from the CLI's side, cloud is just conductor reached at a `/conductor` URL prefix, with Auth0 as the OIDC provider.** Everything below is why that one sentence holds despite what cloud does internally — the caveats are all server-side and shape-invisible, so none of them leak into the client.

Cloud's reverse proxy now exposes the **entire** Conductor API surface under a `/conductor/v2` prefix, so **one generated client works against all three modes**. Cloud is just the profile `url` carrying a `/conductor` prefix — one value of the base-path knob from *Container topology* above; the generated `/v2/...` paths land correctly after a constant-prefix strip. **There is no hand-written cloud client** — the earlier `/v1alpha1/user*` design is obsolete.

- **Org-scoped routes** (`/conductor/v2/orgs/:orgName/*path`, plus explicit `GET`/`PATCH /conductor/v2/orgs/:orgName` for the bare org resource) are a pure prefix strip → `/v2/orgs/...`, org name verbatim, **no name→ID rewrite** — the Conductor API is org-*name*-keyed, and conductor now owns the past-limit gate, so the proxy no longer blocks inline (`~/cloud/controllers/public/conductor/route.go:283-312`, `~/cloud/routes/public/router.go:199-202`).
- **User routes** live outside `/v2/orgs`, so cloud mounts `POST /conductor/v2/users` and `GET /conductor/v2/users/me` as their own routes (`router.go:189-190`) and **preserves conductor's camelCase shapes**: `getCurrentUser` proxies conductor's `UserProfile` body untouched (adding `isDbosAdmin: true` for a DBOS admin — a field conductor already declares `omitempty`, so the generated struct already carries it); `registerUser` authors a body deliberately matching conductor's schema (`~/cloud/controllers/public/conductor/users.go`). The stale cloud swagger (`~/cloud/docs/public/public_swagger.json`) and the old PascalCase `/v1alpha1/user/profile` are **not** used.
- **Both a `dbos_` API key and an Auth0 JWT authenticate the whole API surface**, user routes included (`JWTOrConductorAPIKeyMiddleware`, `router.go:183`). There is no Auth0-only subset anymore.
- **Two operations are cloud-intercepted** — `POST .../join` and `DELETE .../members/{username}` — because cloud runs side effects (the org-move saga, telemetry cleanup) around them (`~/cloud/controllers/public/conductor/intercept.go`). Both keep conductor's request/response shapes by explicit design: *"a client generated from the single spec cannot tell the two apart."* No CLI-side handling is needed.
- **Cloud serves conductor's spec** (with `servers` repointed to `/conductor`) publicly at `/conductor/v2/openapi.json` (3.1, the version we vendor) and `/conductor/v2/openapi-3.0.json` (`router.go:180-181`) — a live cross-check that the vendored spec + `/conductor` base matches what cloud advertises.
- **Errors** are RFC 7807 `application/problem+json` (`ErrorModel`: `title`/`status`/`detail`/`errors`/`instance`). A past-limit org gets `403` + header `X-DBOS-Error: past_limit`, relayed unchanged even through the intercepted paths (`intercept.go:157`, `writeConductorError`) — surface it as an actionable upgrade message.
- **Auth0 values** (`~/ts-transact/packages/dbos-cloud/users/authentication.ts:7-11`): domain `login.dbos.dev` (prod) / `dbos-inc.us.auth0.com` (staging), client ID `6p7Sjxf13cyLMkdwn14MxlH7JdhILled` / `G38fLmVErczEo9ioCFjVIHea6yd0qMZu`, audience `dbos-cloud-api`, device authorization grant.

## Code generation

The Conductor client is **generated, not hand-written**. Do not edit anything in `internal/api/`.

Conductor emits **OpenAPI 3.1.0** by default — `dbos-conductor openapi` (`~/conductor/main.go:254-255`; `-spec-version 3.0` also exists but is not used). Verified output: 49 paths / 57 operations / 55 schemas, clean camelCase `operationId`s, and zero `oneOf`/`anyOf`/`allOf`/`discriminator`. 3.1 renders nullable fields as `type: ["T", "null"]` rather than 3.0's `nullable: true`; `oapi-codegen` handles both. **Pin `oapi-codegen` to ≥ v2.8.0** — the release that added OpenAPI 3.1 support (earlier versions were 3.0-only, limited by an older `kin-openapi` parser). That same release raised oapi-codegen's own minimum to **Go 1.25**, which is exactly the module's `go` directive (see Commands), so the generator toolchain and the build floor line up rather than fight.

- **One generated client serves all three modes.** Cloud proxies conductor's exact request/response shapes under `/conductor/v2` (see Cloud specifics), and even serves this same spec at `/conductor/v2/openapi.json`, so there is no second hand-written client for cloud.
- Generator: `oapi-codegen`, added as a `go tool` dependency in `go.mod` (matching the pattern conductor uses for goimports/golangci-lint) so `make generate` needs no external toolchain.
- Config: `generate: {models: true, client: true}`, package `api`.
- The spec is **vendored** at `internal/api/openapi-3.1.json` and generated code is **committed**. CI runs `make generate && git diff --exit-code` so spec drift fails loudly — the guard against the Conductor API still moving pre-ship.
- **`make generate` also emits the OAuth-gate table.** `oapi-codegen` discards vendor `x-` extensions, so `x-dbos-requires-oauth` never reaches the generated types. A small companion generator (a `go:generate`-style script reading the same vendored spec) emits `internal/api/oauth_gated.go` — a `map[string]bool` keyed by `operationId` — committed and drift-checked alongside the client. This is why the CLI can gate by operation without hardcoding the route list; keep both outputs behind the one `make generate` so they can never disagree.
- **Target the released Conductor API only.** The CLI ships after Conductor does, so there is exactly one API version to support — no compatibility shims, no version negotiation, no pinning to a spec revision. During the interim, conductor's API work sits on `devhawk/v2-api` and cloud's proxy on `devhawk/conductor-v2-api`; `make spec` reads whatever the local checkout has, so keep it on the right branch until they merge, then regenerate from `main` and forget the branches ever existed. The CI drift check is the guard that this actually happened.
- Known cosmetic wart: huma stamps a read-only `$schema` property on ~40 schemas, so generated structs carry an unused `Schema *string`. Ignore it.
- The stray `openapi.json` at the repo root is a gitignored scratch artifact (conductor's default output filename); the build uses only the vendored `internal/api/openapi-3.1.json`.

## Architecture

Cobra layout, with the entrypoint separated from the command tree so the binary is named `dbos` (see Repository state): `cmd/dbos/main.go` is a thin `package main` that calls `cli.Execute()`. The command tree lives in `internal/cli` — each subcommand in its own file as a package-level `*cobra.Command` var, registered by that file's `init()` via `rootCmd.AddCommand(...)`, no central registration list. Flags are declared in the same `init()`; persistent flags belong on `rootCmd`. `Execute()` exits 1 on error and Cobra prints the error, so `RunE` functions should return errors rather than printing and exiting. (`cmd/` holds only the binary's `main`, per the usual Go convention; command logic is importable and testable in `internal/cli`.)

```
cmd/dbos/main.go     entrypoint — package main, calls cli.Execute()
internal/
  cli/               cobra command tree: root, version, login, logout, profile, config, workflow, queue, app, token
  api/               vendored spec + generated client — do not hand-edit
  config/            os.UserConfigDir()/dbos/config.yaml profiles (XDG-aware)
  creds/             credential store: Store interface + 0600 file backend
  auth/              OIDC discovery + device flow + refresh
  client/            profile -> configured api.ClientWithResponses
  output/            table (text/tabwriter) and JSON rendering
```

There is no `internal/cloud` and no `internal/identity` package: the cloud proxy exposes conductor's exact shapes (see Cloud specifics), so the generated client is the *only* API client, and identity is just generated `getCurrentUser`/`registerUser` calls with one selfhosted guard.

**Config and credentials.** A profile is `{name, auth: none|bearer, url, org, app?, oidc?: {issuer, audience, clientID}}` plus a `current` pointer. **There is no `mode` enum** — behavior is a function of explicit axes: `auth` (send a bearer token or not), `url` (the full API base **including any reverse-proxy prefix** — `/conductor`, `/api/conductor`, or none; see *Container topology* — used verbatim as the client `Server`), and the `oidc` block. `selfhosted`/`oauth`/`cloud` elsewhere in this doc are just named combinations of those axes. The config is written to `config.yaml` under the platform config dir resolved by the stdlib **`os.UserConfigDir()` + `/dbos/`** — `$XDG_CONFIG_HOME/dbos/` (default `~/.config/dbos/`) on Linux, `%AppData%\dbos\` on Windows. YAML because humans edit it and comments survive (`gopkg.in/yaml.v3`, not viper). **Why `os.UserConfigDir()` and not a hardcoded `~/.dbos`:** it follows the modern XDG convention that config-first CLIs standardized on (`bat`, `fish`, `starship` under `~/.config`; `gh` too), it's cross-platform for free, and it sidesteps a name collision with the DBOS SDK's *project-local* `.dbos/`. There is no home-directory incumbent to preserve — the TS `dbos-cloud` CLI's `.dbos/` is **cwd-relative** (`cloudutils.ts:34`), so a home location is greenfield and should just follow the standard. Precedence: flag > env (`DBOS_PROFILE`, `DBOS_URL`, `DBOS_ORG`, `DBOS_APP`, `DBOS_TOKEN`) > profile. Cloud profiles default the Auth0 values above so no hand-written OIDC block is needed. **With no active profile (ad-hoc `--url`), `auth` is inferred from token presence:** no token anywhere (`--token`/`DBOS_TOKEN`/stored creds) → `auth: none` and `org` defaults to `local`; any token present → `auth: bearer` with `org` from `--org`/`DBOS_ORG`. So a bare `dbos --url http://localhost:8090 app list` just works against a no-auth conductor with no flags.

Credentials live beside the config as `credentials.json` in the same `os.UserConfigDir()/dbos/` directory, keyed by profile, mode `0600` (JSON because it is machine-written only). **Decided: keep a read-only fallback** to the TS CLI's cwd-relative `./.dbos/credentials`, so an existing `dbos-cloud` login carries over without re-authenticating. Read that file, never write it — the CLI surface is deliberately incompatible with the TS CLI, but the credential *format* compatibility is worth the ~30 lines. That fallback stays cwd-relative because that is where the TS CLI wrote it; our own file is never cwd-relative, so it survives a `cd`. **On Windows `0600` is effectively a no-op** — the file inherits its per-user `%AppData%` directory ACL rather than a POSIX mode. v1 accepts that (matching `aws`/`gcloud`, which also just write files on Windows) and documents it; Windows users wanting stronger at-rest protection are pointed to the F7 keychain backend (Credential Manager).

**Two location sub-decisions, deferred (not blocking):**

1. **macOS path.** `os.UserConfigDir()` returns `~/Library/Application Support/dbos` on macOS, whereas CLI tools there conventionally use `~/.config/dbos` (`bat`, `starship`). Decide whether to accept the stdlib path or force `~/.config` on macOS. Leaning stdlib for simplicity unless the `~/.config` convention proves more expected.
2. **Config dir vs state dir for the credential file.** Strict XDG would put a secret under `$XDG_STATE_HOME` (`~/.local/state/dbos`) rather than the config dir. Co-locating it with `config.yaml` at `0600` matches what `gh` does (token in `~/.config/gh/hosts.yml`) and keeps everything in one directory — the current choice; revisit only if there's a reason to split.

**Storage sits behind a `Store` interface** so the backend can change without touching call sites:

```go
type Store interface {
    Load(profile string) (*Creds, error)
    Save(profile string, c *Creds) error
    Delete(profile string) error
}
```

v1 ships exactly one backend: the `0600` file above (plus the read-only TS-CLI fallback). **A file is the deliberate default, not a limitation.** The primary environments are headless — self-hosted conductor on servers, containers, CI, WSL — exactly where OS keychains are absent or need a Secret Service daemon that isn't running. Peer infra CLIs land the same way: `aws` (`~/.aws/credentials`), `kubectl` (`~/.kube/config`), and `docker` (`~/.docker/config.json`, base64, *not* encrypted) are all plaintext files; docker exposes an OS keychain only as an opt-in `credsStore` helper. And the durable secret this CLI mints — a scoped `dbos_` key — is meant to live in the platform's own secret store (CI secret, env var), so the file mostly holds 24h access tokens. An OS-keychain backend (macOS Keychain / Windows Credential Manager / Linux Secret Service, via [`99designs/keyring`](https://github.com/99designs/keyring) with its built-in encrypted-file fallback) is a **v1.1 opt-in** selected by a config key — docker's `credsStore` model — which is the whole reason the interface exists now.

**Auth.** One code path serves every `auth: bearer` profile (self-hosted OIDC or cloud): OIDC discovery at `{issuer}/.well-known/openid-configuration` → device authorization grant → poll the token endpoint honoring `interval`/`expires_in` → store. Refresh when a refresh token exists, otherwise re-prompt. `dbos_`-prefixed tokens skip the flow and are sent as-is. `internal/client` uses the profile's `url` **verbatim** as `Server` — any reverse-proxy prefix (`/conductor` cloud, `/api/conductor` console-fronted, none for conductor-direct) is already part of `url`, so there is no mode-based path munging — and attaches a `RequestEditorFn` for the bearer header, omitted entirely when `auth: none`.

**Identity.** The cloud-vs-conductor split is gone — the generated `getCurrentUser`/`registerUser` serve both `oauth` and `cloud` modes unchanged, because cloud proxies conductor's shapes. The only remaining branch is `selfhosted` (no-auth), where `/v2/users*` do not exist: there `dbos profile` reports a static `{Name: "local", OrgName: "local"}` and `register` returns a clear unsupported error. That is a mode check in the login/profile/register commands, not a `Provider` interface with per-backend impls.

## Versioning & release

The `version` command resolves its value from three sources, in order, so every build path reports something truthful:

1. **`-ldflags -X` at build time** — the only way to stamp a semver *tag*, since Go can't infer `v1.2.0` from the tree. `internal/cli` holds package-level `var version = "dev"`, `commit`, `date`; a tagged release overwrites them.
2. **`runtime/debug.ReadBuildInfo()`** — free VCS stamps `go build` bakes in (`vcs.revision`, `vcs.time`, `vcs.modified`) plus `Main.Version`, which is a real semver only when installed via `go install …/cmd/dbos@vX`. Used as the fallback when `version` is still the `dev` sentinel, so a plain `go build` or `go install` still prints a commit and a dirty flag. **Caveat:** recent Go also synthesizes a *pseudo-version* (`v0.0.0-<timestamp>-<hash>`) into `Main.Version` for a local main-module `go build`, so the resolver ignores pseudo-versions and keeps `dev` — `Main.Version` is only adopted when it's a real tag.
3. Sentinel `dev` if neither is present.

Net behavior: tagged release → `v1.2.0 (abc1234, 2026-07-28)`; `go install …@latest` → the module version; local `go build` → `dev (abc1234, dirty)`.

- **Wiring.** Set `rootCmd.Version` so Cobra gives `dbos --version` for free; keep `dbos version` as a richer subcommand printing version/commit/date/`go` version/OS-arch. `dbos version` doubles as the mitigation for the Python-SDK `dbos` PATH collision (Repository state) — it names *which* `dbos` this is.
- **Releases use [goreleaser](https://goreleaser.com).** A `.goreleaser.yaml` builds `./cmd/dbos` as binary `dbos` for linux/darwin/windows × amd64/arm64, injecting the `-X` ldflags from the git tag, producing archives + a `checksums.txt`, and (public repo, MIT) publishing a **Homebrew tap** and `go install` instructions. A tag-triggered GitHub Actions job (`on: push: tags: ['v*']`) runs `goreleaser release`. Snapshot builds (`goreleaser build --snapshot`) give unsigned local artifacts for testing without a tag.
- **Deferred until there's a v1 tag worth cutting** (tracked in the roadmap), but the layout decision (`cmd/dbos/`) and the `version`-command contract are fixed now so nothing has to move later. goreleaser itself needs no code — it is config plus a CI job.

## Command surface

The tables below map **every** one of the Conductor API's 57 operations to a command. The TypeScript `dbos-cloud` CLI (`~/ts-transact/packages/dbos-cloud/cli.ts:644-800`) is a reference for *which* workflow operations matter in practice, not a compatibility target: **the CLI surface is deliberately not compatible with it** — no aliases, no inherited flag names. Pick the better name. This CLI still does **not** replace `dbos-cloud` for app deploy, Postgres provisioning, dashboards, or env vars.

Status column: **v1** = first release, **v1.1** = fast follow, **defer** = build only on demand.

### Conventions

- **Bulk is variadic, not a separate verb.** `dbos workflow cancel ID1 ID2 ID3` calls `/cancel` for one ID and `/bulk-cancel` for several — the API split shouldn't leak into the CLI. A literal `-` reads IDs from stdin, so `dbos workflow list -o json | jq -r '.[].workflowId' | dbos workflow cancel -` works. `--children` maps to `cancelChildren`/`deleteChildren`.
- **Mutating commands take IDs positionally.** No `-w/--workflowid` flag.
- **Mode gating.** Commands marked *OAuth* map to operations flagged `x-dbos-requires-oauth` in the spec (14 of them; see The three deployment modes). They do not exist in `selfhosted` mode — the CLI checks the spec-derived gate set and fails with the mode-aware message before sending, not on a 404. **Teams plan.** Exactly one operation needs a Teams subscription: `listAuditLogs` (`requireTeamPlan`, `controllers/apiv2/auth.go:243`). The plan isn't encoded in the spec, so `audit list` stays a **runtime 403** ("requires a DBOS Teams subscription") — not a pre-request gate like the OAuth ones. On self-host it's effectively unreachable by default: in no-auth mode the route doesn't exist at all (it's also `x-dbos-requires-oauth`), and in `oauth` mode every org defaults to `free` (`migrations/000008_identity.up.sql:6`) with **no endpoint to upgrade** (`update-sub` is legacy-admin-only) — a self-hosted operator would set `organizations.subscription_plan = 'team'` in Postgres directly. The CLI just relays the 403 with that context; it never gates on plan itself. (The legacy internal surface also team-gated `app metrics` and role-create; **the current API does not** — don't re-add those gates.)

**Global (persistent) flags**, declared once on `rootCmd`: `-o/--output`, `-a/--app`, `--org`, `--url`, `--profile`. App name resolves `-a/--app` → `DBOS_APP` → profile default → error naming the flag; org resolves `--org` → `DBOS_ORG` → profile default → `local` (in `auth: none`). **A user belongs to exactly one org** (confirmed: `UserProfile` carries a single `orgName`/`orgId`, and `join` is a *move*), so org is a single resolved value — no org picker or switcher, and login can populate the profile's org from `getCurrentUser`.

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
- **Pagination default is undecided** (open sub-decision). The `list` operations are `limit`/`offset`-paged; whether the default is auto-fetch-all, a single page, or a single page with a truncation notice affects the `list -o ids | cancel -` pipeline (a capped default silently acts on only page 1). Deferred — pick when the list commands land (D1/E1).

### Exit codes

A small, stable, documented set (covered by a test), so CI can branch without parsing stderr:

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | general / runtime error |
| `2` | usage error (Cobra's default for bad flags/args) |
| `3` | auth required — 401, or no login where one is needed (pairs with the "run `dbos login`" message) |
| `4` | not found — 404 |

Deliberately narrow: past-limit/plan-gated and network-vs-server splits stay `1` for now (their detail is already in the message and `-o json`), because a wider table is more contract to keep stable than current demand justifies. Adding codes later is safe; renumbering these is breaking, so this set is fixed.

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
| `app metrics <name>` | `listMetrics` (`--start-time`/`--end-time`) | v1 |
| `app set-version <name> <ver>` | `setLatestAppVersion` | v1 |
| `app update <name>` | `updateApp` | v1 |
| `app register <name>` | `registerApp` | v1 |
| `app delete <name>` | `deleteApp` | v1 |
| `queue list` | `listQueues` | v1 |
| `queue get <name>` | `getQueue` | v1 |
| `schedule list` | `listSchedules` | v1 |
| `schedule get <name>` | `getSchedule` | v1 |
| `schedule pause\|resume <name>` | `pauseSchedule` / `resumeSchedule` | v1 |
| `schedule trigger <name>` | `triggerSchedule` → prints new `workflowId` | v1 |
| `schedule backfill <name>` | `backfillSchedule` (`--start-time`/`--end-time` required) | v1.1 |
| `alert list\|create\|delete` | `listAlertingRules` / `createAlertingRule` / `deleteAlertingRule` | v1.1 |

**Resolved naming.** `queue list`/`queue get` are unambiguously about queue *definitions* (`GET .../queues`). Enqueued workflows are `workflow list --queued` (`search` with `queuesOnly: true`), optionally narrowed by `--queue <name>` — they're workflows, so they belong under `workflow`. There is no `workflow queue list` subcommand; the TS CLI's nesting was the thing that made the two concepts collide.

`app update` is the tuning surface (`executorTimeoutSecs`, `gcRowsThreshold`, `gcTimeThresholdMs`, `globalTimeoutMs`, `privateMode`) — worth having for self-hosted operators. `app register` takes only `privateMode`; apps normally self-register through the SDK, so the CLI verb is for self-hosted operators provisioning apps by hand rather than the common path. `app delete` is destructive, so it confirms interactively unless `--force` (or a non-TTY stdin) is given.

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

## `dbos-cloud` TS CLI (reference — the scope boundary)

The TypeScript `dbos-cloud` CLI (`~/ts-transact/packages/dbos-cloud/cli.ts`) is the incumbent. This CLI **does not replace it.** The table below is the complete `dbos-cloud` surface so the boundary is explicit and reviewable: exactly the workflow-management commands cross into this CLI (re-platformed onto the Conductor API), and everything else stays in `dbos-cloud`. Do not port a cloud-specific command without a deliberate scope decision recorded here.

A structural note that matters for the port: `dbos-cloud`'s own workflow commands **do not use the Conductor API** — they POST to an `/appsadmin/{org}/applications/{app}/workflows` proxy, a different and older surface. So the five commands below are *re-platformed*, not wrapped; their flag names and semantics are a reference for intent only (this CLI renames flags freely — see Command surface).

| `dbos-cloud` group | Commands | Backend | In this CLI? |
|---|---|---|---|
| **workflow** | `list`, `cancel`, `resume`, `restart` | `POST /appsadmin/{org}/applications/{app}/workflows[...]` | **Yes** → the Conductor API's `searchWorkflows` / `cancel` / `resume` / `restart`, and much more (get, steps, fork, delete, events, stats, export/import) |
| **workflow queue** | `queue list` | `POST /appsadmin/{org}/applications/{app}/queues` | **Yes** → `workflow list --queued` (`search` with `queuesOnly:true`) |
| **auth / user** | `login`, `logout`, `profile`, `register`, `revoke` | Auth0 device flow + `GET /v1alpha1/user/profile` | **Partial** — `login`/`logout`/`profile`/`register` reimplemented against the Conductor API's identity + Auth0 (device flow is shared); no `revoke` yet |
| **application** | `register`, `update`, `deploy`, `change-database-instance`, `delete`, `list`, `status`, `versions`, `logs`, `resource-usage`, `cmd` | `/v1alpha1/{org}/applications[...]`, `/vmsadmin/...` | **No** — deploy, executor scaling, VM exec, logs, usage are cloud infra. (The Conductor API has its *own* read-only `app list/get/versions/executors/metrics` — those are ours; the deploy lifecycle is not.) |
| **application env / secrets** | `env create`, `env import`, `env list`, `env delete` | `/v1alpha1/{org}/applications/secrets[...]` | **No** — cloud env vars |
| **database** | `provision`, `status`, `list`, `reset-password`, `destroy`, `restore`, `link`, `unlink`, `url`/`connect` | `/v1alpha1/{org}/databases[...]` | **No** — Postgres provisioning, BYOD linking, PITR restore |
| **dashboard** | `launch`, `url`, `delete` | `/v1alpha1/{org}/dashboard` | **No** — monitoring dashboards |
| **organization** | `invite`, `list`, `rename`, `join`, `remove` | `/v1alpha1/{org}[...]` | **Overlaps** — the Conductor API has its own `org`/`role` surface (see Tokens/org/roles), gated to OAuth mode. The cloud `org` commands here are the `/v1alpha1` incumbents; this CLI's `org *` targets the Conductor API, not these. |

**The boundary in one sentence:** this CLI owns *workflow and conductor-object management* (workflows, queues, schedules, apps as conductor sees them, tokens, org/roles/audit in OAuth mode) across all three deployment modes; `dbos-cloud` keeps *cloud infrastructure* (app deploy, Postgres, dashboards, secrets, resource usage) — which has no Conductor API equivalent and is explicitly out of scope.

## Testing

Container-backed tests use **testcontainers-go** (core + `modules/postgres`, both v0.43.0). Tests are tiered because the upper tiers need secrets and network:

**Tier 1 — unit, no Docker.** `go test ./...`. Config precedence, credential round-trip and file mode, TS-credentials fallback parsing, device-flow polling against an `httptest` OIDC server, error mapping (problem+json / 401 / past-limit / mode-missing-route). This tier must stay green with no Docker daemon.

**Tier 2 — Keycloak container, no secrets.** Exercises the device flow against a real OIDC provider, with **no Auth0 and no conductor** involved. **There is no official testcontainers Keycloak module** — use `testcontainers.GenericContainer` with `quay.io/keycloak/keycloak:26.0`, command `start-dev --import-realm`, and a realm fixture committed at `internal/auth/testdata/dbos-realm.json` defining a public client with `oauth2.device.authorization.grant.enabled: "true"` plus a test user. Owning the realm fixture here means CLI auth tests do **not** depend on conductor's realm, whose only client (`~/conductor/docker/keycloak/dbos-realm.json`, `dbos-console`) does not enable the device grant.

**What the tier-2 test covers, and why it stops where it does.** It asserts everything in *our* code that touches a real provider: OIDC discovery, the device-code request, and a poll that returns a real `authorization_pending`. It does **not** complete approval — the device grant requires a human to confirm the `user_code` in a browser, and Keycloak exposes **no API** for that (the session params are embedded in login/consent HTML, so the only alternatives are a real browser or brittle form-scraping). Approval tests *Keycloak's UI*, not our code, so we skip it: the token-success / `slow_down` / `access_denied` paths are covered by the `httptest` mock in tier 1. The tier-2 test *does* still mint a real signed JWT from Keycloak via the password grant (as conductor does) — proving the realm/client/user issue a valid token — though that token doesn't flow through our device-flow code. This mirrors the wider DBOS stack — conductor's Keycloak tests avoid browser driving too (they use the password grant); the only browser automation lives in the console repo's Playwright suite.

**Tier 3 — conductor + Postgres containers.** The CLI never talks to Postgres; it exists only to back conductor. Its `docker/entrypoint.sh` runs migrations automatically. Wait on `/healthz`. Covers the real command surface end to end in `selfhosted` mode.

Source the conductor container **two ways, selected by env**, so the eventual switch is config rather than a rewrite:

- `CONDUCTOR_IMAGE` set → pull that image. Preferred once available.
- otherwise → `FromDockerfile{Context: $CONDUCTOR_DIR, Dockerfile: "docker/Dockerfile"}`. Required today (no published image has the API yet). **No location is assumed** — `CONDUCTOR_DIR` must be set explicitly. The build passes `TARGETARCH` as a build arg: conductor's Dockerfile needs it to fetch the right `golang-migrate` binary, and testcontainers' build doesn't set it the way BuildKit/`docker compose` do, so the harness supplies `runtime.GOARCH`.

> A public image exists — **`dbosdev/conductor`** on Docker Hub (multi-arch amd64/arm64, tags `latest`/`0`/`0.15`/`0.15.0` back to `0.6.0`) — but **no published tag contains the Conductor API**: every tag is built from `main`, where `controllers/apiv2` and the `openapi` subcommand do not exist. The publish workflow (`.github/workflows/docker-publish.yml`) is `workflow_dispatch`-only and has never produced a `dev-*` tag. Once it merges and a release is cut, set `CONDUCTOR_IMAGE=dbosdev/conductor:<tag>` and drop the checkout dependency from CI entirely. Before then, manually dispatching that workflow on the API branch would publish `dev-<shortsha>` and unblock image-based testing early.

> **Gate:** conductor **panics without `DBOS_CONDUCTOR_LICENSE_KEY`** (`~/conductor/config/config.go:206-208`). Use the **local** key, not the cloud one: `validateLicenseKey` short-circuits to "pro" when the key's SHA-256 matches a hardcoded entry in `localKeyHashes`, so validation is entirely offline. Any other key triggers a startup call to `https://cloud.dbos.dev/v1alpha1/conductor-api-keys/check` on every container boot. Skip the tier when the **key** is unset — never fail the suite for a missing secret (fork PRs don't get secrets). The conductor **source** is different: with neither `CONDUCTOR_IMAGE` nor `CONDUCTOR_DIR` set the harness **fails** rather than guess a location, so the CI integration job is gated on `CONDUCTOR_IMAGE` (it only runs once an image with the API exists).

**Secrets.** Same pattern as the conductor repo: a gitignored `.env` locally, a GitHub Actions secret in CI.

- Local: `cp .env.example .env` and fill in `DBOS_CONDUCTOR_LICENSE_KEY` — the value is the `CONDUCTOR_LOCAL_LICENSE_KEY` line in `~/conductor/.env`. The integration helper loads `.env` from the repo root via `github.com/joho/godotenv` (a test-only dependency), which does not override variables already set in the environment — so both `make test-integration` and a bare `go test -tags integration ./...` work. Keep `.env` in godotenv format (`KEY=value`, no `export` prefix); conductor's own `.env` uses the `export` form because it's meant to be `source`d.
- CI: `DBOS_CONDUCTOR_LICENSE_KEY: ${{ secrets.CONDUCTOR_LOCAL_LICENSE_KEY }}`, reusing conductor's secret name (`~/conductor/.github/workflows/test.yml:56`).
- `.env` is gitignored here and in conductor. Never commit a key value, and don't paste one into this file.

**Tier 4 — full OIDC end-to-end (optional, manual).** CLI → conductor(OAuth) → Keycloak. **Keycloak is a valid conductor issuer, not just a CLI provider:** conductor's user-auth middleware (`middleware/user_authentication.go:41-62`) does generic RS256 + JWKS + `iss`/`aud` validation — the `Auth0*` config names are historical, the mechanism is standard OIDC — so a Keycloak-minted token exercises the OAuth-gated `/v2/users*`/org/role/audit endpoints once `DBOS_OAUTH_ISSUER`/`DBOS_OAUTH_AUDIENCE`/JWKS point at the realm. So the only blocker is not token compatibility but networking — an issuer-alias problem: Keycloak mints tokens with a fixed `iss`, conductor validates `iss` against `DBOS_OAUTH_ISSUER`, and discovery advertises endpoints at that same host — so if conductor reaches Keycloak by network alias while the CLI uses a host-mapped port, the CLI cannot resolve the advertised URLs. Mitigation is a shared network with alias `keycloak`, `KC_HOSTNAME` set to match, and a host `/etc/hosts` entry. Tiers 2 and 3 together cover the same ground with far less setup.

Guard tiers 2–4 with `//go:build integration` and skip on missing prerequisites. `make test` runs tier 1; `make test-integration` runs the rest.

## Roadmap

Sliced so that **every step ends with a binary you can run and a test that proves it** — no step leaves the tree in a state where the only verification is "it compiles". Each is roughly one PR. Steps within a milestone are ordered; milestone F's steps parallelize.

**Priority: app and token management is the first *feature* milestone (D), ahead of workflows and everything else — but it rides on config (B) and auth (C), pulled in just ahead of it.** Every `app *` and `token *` operation is registered unconditionally in conductor (none carry `x-dbos-requires-oauth`), so the *mechanics* are testable in `selfhosted` no-auth mode against the A5 harness with just `--url`. But shipping them as real features needs config (a persisted target, so you're not retyping `--url`) and auth (to reach cloud or an authenticated self-host at all, and to make a minted `dbos_` token *mean* something — the CI story is fundamentally a login flow). So the order is **skeleton (A) → config (B) → auth (C) → app & token (D) → workflows (E) → v1.1 (F)**. The walking skeleton still demos a real `app list` early (A4), so nothing waits on config/auth to show signs of life.

**A — walking skeleton.** Goal: one real command against a real conductor, as early as possible.

- [x] **A1. Codegen** — vendor `internal/api/openapi-3.1.json`, `oapi-codegen` (≥ v2.8.0) as a `go tool` dep, `Makefile` (`spec`/`generate`/`build`/`test`/`lint`), commit generated code, CI drift check. `make generate` also emits `internal/api/oauth_gated.go` from the `x-dbos-requires-oauth` extension. *Done when:* `make generate` is idempotent, the generated client compiles, and the gate table lists the 14 OAuth-only operations.
- [x] **A2. Root wiring** — entrypoint at `cmd/dbos/main.go`, command tree in `internal/cli`, `Use: "dbos"`, global flags (`-o/--output`, `-a/--app`, `--org`, `--url`, `--profile`), and a real `dbos version` with the ldflags/`ReadBuildInfo` resolution (see Versioning & release) plus `rootCmd.Version` for `--version`. No config file yet; `--url`/`--org` only. *Done when:* `dbos version` prints a commit from a plain `go build` and `dbos --help` is correct.
- [x] **A3. Transport + output, minimal** — `internal/client` building the generated client from `--url` with no auth; `internal/output` with `table` (array renderer) and `json`. *Done when:* unit tests cover both renderers.
- [x] **A4. `dbos app list`** — the first end-to-end command. *Done when:* it returns real rows from a locally running conductor. (Command + full path proven by a mock-server e2e test; the live-conductor proof is A5's containerized run.)
- [x] **A5. Conductor test harness (tier 3)** — testcontainers Postgres + conductor on a shared network, conductor sourced via `CONDUCTOR_IMAGE` or built `FromDockerfile` (see Testing), `/healthz` wait strategy, `.env` loader via godotenv, skip-on-missing-secret, CI wiring with `CONDUCTOR_LOCAL_LICENSE_KEY`. *Done when:* `make test-integration` proves A4 against a container. **Pulled early on purpose** — every later command reuses it, so building it now makes the rest of the roadmap self-verifying.

**B — configuration.** Named targets so nothing after this retypes `--url`, and a place to persist a login.

- [x] **B1. Profiles** — `internal/config`, precedence flag > env > profile, `dbos config list|use|set|show`. Retrofits A4's `app list` off bare `--url` onto named profiles and gives every later milestone a configured target.
- [x] **B2. Credential store** — `internal/creds`: the `Store` interface with its one v1 backend (per-profile `0600` file under `os.UserConfigDir()/dbos/`, read-only `./.dbos/credentials` fallback). The interface is the seam the F7 keychain backend drops into.

**C — auth.** Unlocks authenticated (bearer) reach — cloud and self-hosted OIDC — for every milestone that follows. Each step is independently testable; C1 needs no conductor at all.

- [x] **C1. OIDC device flow** — `internal/auth` discovery + device grant + polling, plus the tier-2 Keycloak container and realm fixture. *Done when:* a token is obtained from a real Keycloak with no conductor involved. (Tier-1 mock covers the full flow; tier-2 proves discovery + device-code + real `authorization_pending` against Keycloak and mints a real signed JWT via the password grant — device approval is browser-only with no API, so it's not automated. See Testing.)
- [ ] **C2. Auth wiring** — bearer injection, `dbos_` passthrough, refresh-on-expiry, `dbos login`/`logout`, persisting the token via B2.
- [ ] **C3. Error mapping** — `problem+json` → message, 401 → "run `dbos login`", 403 + `X-DBOS-Error: past_limit` → the upgrade message. (The pre-request OAuth-gate check waits for F4, where the OAuth-only commands it guards first appear.)
- [ ] **C4. Identity (whoami)** — `dbos profile` via the generated `getCurrentUser`, with a single `selfhosted`-mode guard (static `local`). The natural end-to-end confirmation that login worked; `register` ships with the OAuth-gated org/invite flow in F4.

**D — app & token management (the first feature milestone).** The endpoints are unconditional, so they're exercised against the no-auth A5 harness — but with B and C in place they now also work against cloud and authenticated self-host on day one, and minted tokens are meaningful.

- [ ] **D1. App reads** — `app get`, `app versions`, `app executors`, `app metrics` (building on A4 `app list`), plus the single-object **key/value detail renderer** the 29-field `App`/`Workflow` shapes need. *Done when:* each returns real data from the harness and the detail renderer has unit coverage. (`app metrics` renders `table`/`json` here; its report-shaped `csv` format lands with F1.)
- [ ] **D2. App writes** — `app set-version`, `app update` (the self-hosted tuning surface: `executorTimeoutSecs`, `gcRowsThreshold`, `gcTimeThresholdMs`, `globalTimeoutMs`, `privateMode`), plus `app register` and `app delete` (delete confirms interactively unless `--force`/non-TTY). *Done when:* an update round-trips through `app get`, and a `register`→`delete` pair round-trips against the harness.
- [ ] **D3. Tokens** — `token list|create|delete`; `create` prints the bare `dbos_` secret once, scoped by `--app`/`--permission`, and is `-o ids` friendly. *Done when:* a token minted against the harness can list and delete itself. This is the login-once-then-mint-a-scoped-key CI flow — cross-mode from the start now that auth (C) is already in place.

**E — workflows, queues, schedules.** Small PRs over the A/B/C/D plumbing.

- [ ] **E1. Workflow reads** — `list` (search-backed, full filter set), `get`, `steps`, `events` (reuses D1's detail renderer).
- [ ] **E2. Workflow mutations** — `cancel`/`resume`/`restart`/`fork`/`delete`, variadic + stdin `-`, single-vs-bulk dispatch, `-o ids`.
- [ ] **E3. Queues & schedules (read)** — `queue list|get`, `schedule list|get`.
- [ ] **E4. Schedule mutations** — `pause`/`resume`/`trigger`, scalar-output rule.

**F — v1.1.** Independent of each other; pick by demand.

- [ ] **F1. Aggregates** — `workflow stats`, `step-stats`, `--group-by`/`--bucket`/`--select`, `csv` output.
- [ ] **F2. Export/import** — round-trip via stdout/stdin.
- [ ] **F3. Alerts & backfill** — `alert list|create|delete`, `schedule backfill`. (`app metrics` moved up to D1; its `csv` format is F1.)
- [ ] **F4. OAuth-gated set + mode gating** — `org *`, `role *`, `audit list`, `register`; plus the pre-request gate that makes them fail cleanly elsewhere: consult A1's `oauth_gated.go` and refuse an OAuth-only operation in `selfhosted` mode with the mode-aware message rather than letting it 404. (`audit list` additionally stays a runtime 403 on non-Teams orgs — see Command surface.)
- [ ] **F5. Full OIDC e2e (tier 4)** — only if the issuer-alias setup proves worth it.
- [ ] **F6. Release automation** — `.goreleaser.yaml` (multi-OS/arch build of `./cmd/dbos`, ldflags from the tag, archives + checksums, Homebrew tap) and a tag-triggered CI job (see Versioning & release). Gated on the first real tag: land it when the milestone-E v1 surface is ready to ship, ahead of the rest of F.
- [ ] **F7. OS-keychain credential backend** — an opt-in `Store` implementation over `99designs/keyring` (macOS Keychain / Windows Credential Manager / Linux Secret Service), selected by a config key, with the `0600` file backend remaining the default (see Config and credentials). Build on demand.

## Known risks

1. **Cloud refresh tokens may not be issued.** `~/cloud/auth0/tenant.yaml:8-16` sets `allow_offline_access: false` on `dbos-cloud-api` while the TS CLI requests `offline_access` anyway. If Auth0 declines, users re-login every 24h (token lifetime 86400s). Determine empirically in the auth milestone (C); the credential store already tolerates a missing refresh token.
2. **The Conductor API has not shipped yet, but will ship before this CLI does.** Until then the spec can still shift under us; the vendored spec plus the CI drift check makes that a mechanical update rather than a silent breakage. This is a pre-merge inconvenience, not a long-term constraint — there will only ever be one released Conductor API to support.
3. **Self-hosted OIDC needs a client ID the CLI cannot discover.** *Confirmed:* conductor validates tokens (issuer/audience/JWKS) but stores no interactive client ID — only an M2M admin allow-list (`config.go:79`). Profiles carry the client ID. A future unauthenticated `GET /v2/config` on conductor returning `{mode, oauth:{issuer, audience, clientId}}` would let `dbos login --url X` self-configure and stop the CLI guessing which endpoints exist (and could carry the base-path prefix too). Deferred, not designed away.
4. **Cloud's `/conductor/v2` proxy ships and cuts over before the CLI is public.** It is fully implemented in the `~/cloud` checkout as a *staged-migration* mount ("Deleted once console fully cuts over", `~/cloud/routes/public/router.go:111-115`), living beside the older `/conductor/v1alpha1` proxy while the console migrates. Like the Conductor API itself (risk #2), the cutover completes ahead of this CLI going public, so it is a pre-public sequencing fact, not a long-term constraint — there will be one cloud proxy surface to target. Until the cutover, cloud-mode testing against production waits for the deploy; local and self-hosted modes don't. One mechanical caveat survives: the CI drift check guards the *spec*, not the *base path*, so confirm the `/conductor` prefix once against the deployed cloud before release.

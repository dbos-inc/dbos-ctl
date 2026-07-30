# dbos

A command-line client for the [DBOS](https://www.dbos.dev) Conductor API. `dbos`
talks to a self-hosted Conductor, a self-hosted Conductor with OIDC auth, or
DBOS Cloud — the target is selected by a named **profile**.

> Status: early. The command surface below is what ships today (login, identity,
> and app listing); workflow management lands in a later milestone.

## Install

Build from source (Go 1.24+):

```sh
make build      # produces ./dbos
# or
go install github.com/dbos-inc/dbos-cli/cmd/dbos@latest
```

## Quick start

```sh
# DBOS Cloud
dbos config set cloud --cloud
dbos login                       # opens the device-authorization flow
dbos whoami                      # confirm who you're logged in as
dbos app list

# A self-hosted Conductor with no auth
dbos config set local --auth none --url http://localhost:8090
dbos --profile local app list
```

## Profiles

Configuration lives in `config.yaml` under your OS config dir
(`~/.config/dbos/config.yaml` on Linux, `~/Library/Application Support/dbos/` on
macOS). A profile is a named bundle of settings; `config set` creates or updates
one, touching only the fields you pass:

```sh
dbos config list                 # all profiles, marking the current one
dbos config show [profile]       # one profile's settings
dbos config use <profile>        # set the default profile
dbos config set <profile> ...    # create or update
```

There are three common shapes:

| Shape | How to create | Auth | Identity |
|---|---|---|---|
| **Self-hosted, no auth** | `config set x --auth none --url http://host:8090` | none | always `local` |
| **Self-hosted + OIDC** | `config set x --auth bearer --url http://host:8090 --issuer <url> --client-id <id> [--audience <aud>]` | user JWT or `dbos_` key | real |
| **DBOS Cloud** | `config set x --cloud` | Auth0 JWT | real |

A profile must target either DBOS Cloud (`--cloud`) or a self-hosted Conductor
(`--url`); the two are mutually exclusive. `--cloud` points at `cloud.dbos.dev`
and derives everything else (the `/conductor` URL, bearer auth, and the Auth0
tenant) automatically.

## Authentication

```sh
dbos login     # OIDC device flow against the profile's issuer; stores a token
dbos logout    # forget the stored token for the current profile
```

`login` runs the [device-authorization flow](https://www.rfc-editor.org/rfc/rfc8628):
it prints a URL and a code, you approve in a browser, and the token is stored in
`credentials.json` (mode `0600`) next to `config.yaml`, keyed by profile. Tokens
are refreshed automatically on expiry when the issuer returns a refresh token.

Two ways to bypass the flow:

- **`DBOS_TOKEN`** — a bearer token used as-is for one invocation.
- **`dbos_…` API keys** — set as `DBOS_TOKEN` (or stored); sent verbatim. These
  authenticate machine-to-machine calls (e.g. `app list`) but carry no user
  identity, so `dbos whoami` needs a user login, not a key.

## Commands

| Command | Description |
|---|---|
| `dbos login` / `dbos logout` | Acquire / discard credentials for the current profile |
| `dbos whoami` | Show the logged-in identity (`local` on a no-auth target) |
| `dbos app list` | List applications in the org |
| `dbos config list \| show \| use \| set` | Manage profiles |
| `dbos version` (or `--version`) | Print version information |

## Configuration precedence

Each setting is resolved **flag → environment → profile**, so a flag always
wins and the profile is the fallback:

| Setting | Flag | Env |
|---|---|---|
| Profile | `--profile` | `DBOS_PROFILE` |
| Conductor URL | `--url` | `DBOS_URL` |
| Organization | `--org` | `DBOS_ORG` |
| Application | `-a`, `--app` | `DBOS_APP` |
| Bearer token | — | `DBOS_TOKEN` |
| Output format | `-o`, `--output` | — |

## Output

Human-readable tables by default; `-o json` emits the raw API shape for
scripting (never truncated or reprojected):

```sh
dbos app list                    # aligned table
dbos app list -o json            # raw JSON array
dbos whoami -o json              # raw UserProfile
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General error |
| `2` | Usage error (bad flags/arguments) |
| `3` | Authentication required (HTTP 401) — run `dbos login` |
| `4` | Not found (HTTP 404) |

## Development

```sh
make generate    # regenerate the API client from the vendored OpenAPI spec
make build       # build ./dbos
make test        # unit tests
make lint        # golangci-lint
```

The generated client (`internal/api`) is committed; CI fails on spec drift
(`make generate` must be a no-op). Integration tests are tagged `integration`
and stand up real Conductor + Postgres in throwaway containers — see
`make test-integration` and `.env.example` for the required license key and
image/checkout settings.

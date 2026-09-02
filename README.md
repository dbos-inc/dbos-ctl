# dbosctl

A command-line client for the [DBOS](https://www.dbos.dev) Conductor API.
`dbosctl` talks to DBOS-managed Conductor, a self-hosted Conductor, or a
self-hosted Conductor with OpenID Connect (OIDC) auth — the target is selected
by a named **profile**.

> Status: early. The command surface below is what ships today (login, identity,
> and app listing); workflow management lands in a later milestone.

## Install

**Install script.** Detects your platform, verifies the download against the
release checksums, and installs to the first writable of `/usr/local/bin`,
`~/.local/bin`, or the current directory:

```sh
curl -sSfL https://raw.githubusercontent.com/dbos-inc/dbos-ctl/main/install.sh | sh
```

Set `VERSION=v0.1.0` to pin a release, or `BIN_DIR=/somewhere` to choose where it
lands. Releases also ship archives for linux, macOS, and Windows on amd64 and
arm64 if you would rather [download one directly](https://github.com/dbos-inc/dbos-ctl/releases);
the binaries are statically linked, so they run anywhere, Alpine included.

**With Go** (1.24+), which builds from source:

```sh
go install github.com/dbos-inc/dbos-ctl/cmd/dbosctl@latest
```

**With Nix**:

```sh
nix run github:dbos-inc/dbos-ctl -- --help    # run it without installing
nix profile install github:dbos-inc/dbos-ctl
```

For NixOS, add the flake as an input and use `inputs.dbos-ctl.packages.${system}.default`.
Intel Macs (`x86_64-darwin`) are not supported — nixpkgs dropped the platform in 26.11; use
the release binaries or `go install` there.

**From a checkout:**

```sh
make build      # produces ./dbosctl
```

`dbosctl version` reports which of these you have: a release prints its tag, a
`go install` prints the module version, and a Nix or local build prints `dev`
with the commit it came from.

The binary is `dbosctl`, not `dbos`: the DBOS language SDKs ship their own
`dbos` entrypoints (the Python SDK installs a `dbos` console script), and two
different tools answering to one name on `PATH` is a silent, confusing failure.
The `ctl` suffix keeps this CLI unambiguous alongside any of them.

## Quick start

```sh
# DBOS-managed Conductor
dbosctl config set managed --managed
dbosctl login                       # opens the device-authorization flow
dbosctl whoami                      # confirm who you're logged in as
dbosctl app list

# A self-hosted Conductor with no auth
dbosctl config set local --auth none --url http://localhost:8090
dbosctl app list --profile local

# Registering an app
dbosctl app register <app-name>     # must match app name in DBOS config

# Creating an API key
dbosctl api-key create <key-name>   # typically provided to app via DBOS_CONDUCTOR_KEY env var
```

## Profiles

Configuration lives in `config.yaml` under your OS config dir
(`~/.config/dbos/config.yaml` on Linux, `~/Library/Application Support/dbos/` on
macOS). A profile is a named bundle of settings; `config set` creates or updates
one, touching only the fields you pass:

```sh
dbosctl config list                 # all profiles, marking the current one
dbosctl config show [profile]       # one profile's settings
dbosctl config use <profile>        # set the default profile
dbosctl config set <profile> ...    # create or update
```

There are three common shapes:

| Shape | How to create | Auth | Identity |
|---|---|---|---|
| **DBOS-managed** | `config set x --managed` | Auth0 JSON Web Token (JWT) | real |
| **Self-hosted + OIDC** | `config set x --url http://host:8090 --issuer <url> --client-id <id> [--audience <aud>]` | user JWT or `dbos_` key | real |
| **Self-hosted, no auth** | `config set x --url http://host:8090` | none | always `local` |

A profile must target either DBOS-managed Conductor (`--managed`) or a
self-hosted Conductor (`--url`); the two are mutually exclusive. `--managed`
points at `cloud.dbos.dev` and derives everything else (the `/conductor` URL,
bearer auth, and the Auth0 tenant) automatically. Passing
`--issuer`/`--client-id` implies bearer auth, so `--auth` is only needed for the
uncommon case of a self-hosted Conductor you
reach with a `dbos_` API key but no OIDC login: `--auth bearer`. Because an API
key carries no user identity, give that profile an `--org` too.

## Authentication

```sh
dbosctl login     # OIDC device flow against the profile's issuer; stores a token
dbosctl logout    # forget the stored token for the current profile
```

`login` runs the [device-authorization flow](https://www.rfc-editor.org/rfc/rfc8628):
it prints a URL and a code, you approve in a browser, and the token is stored in
`credentials.json` (mode `0600`) next to `config.yaml`, keyed by profile. Tokens
are refreshed automatically on expiry when the issuer returns a refresh token.

Two ways to bypass the flow:

- **`DBOS_TOKEN`** — a bearer token used as-is for one invocation.
- **`dbos_…` API keys** — set as `DBOS_TOKEN` (or stored); sent verbatim. These
  authenticate machine-to-machine calls (e.g. `app list`) but carry no user
  identity, so `dbosctl whoami` needs a user login, not a key.

## Commands

| Command | Description |
|---|---|
| `dbosctl login` / `dbosctl logout` | Acquire / discard credentials for the current profile |
| `dbosctl whoami` | Show the logged-in identity (`local` on a no-auth target) |
| `dbosctl app list` | List applications in the org |
| `dbosctl app get <name>` | Show one application's details |
| `dbosctl app versions <name>` | List an application's versions |
| `dbosctl app executors <name>` | List an application's connected executors |
| `dbosctl app metrics <name>` | List an application's metrics (`--since`, default 24h) |
| `dbosctl app register <name>` | Register an application (optionally `--private-mode`) |
| `dbosctl app update <name>` | Update tuning settings (e.g. `--executor-timeout-secs`, `--private-mode`) |
| `dbosctl app set-version <name> <version>` | Set the application's latest version |
| `dbosctl app delete <name>` | Delete an application (prompts to confirm; `--force` required when non-interactive) |
| `dbosctl workflow list` | List workflows, filterable (`--status`, `--name`, `--since 1h`, …); returns all matching by default, `--limit`/`--offset` to bound |
| `dbosctl workflow get <id>` | Show a workflow's details (app-scoped, needs `--app`) |
| `dbosctl workflow steps <id>` | List a workflow's steps |
| `dbosctl workflow events <id>` | List a workflow's events |
| `dbosctl workflow cancel\|resume\|delete <id>...` | Mutate one or more workflows (variadic; `-` reads IDs from stdin; `--children` on cancel/delete) |
| `dbosctl workflow fork <id>` | Fork a workflow into a new execution (prints the new ID; `--start-step`, `--new-id`) |
| `dbosctl queue list \| get <name>` | Inspect queue definitions (app-scoped, needs `--app`) |
| `dbosctl schedule list \| get <name>` | Inspect scheduled workflows |
| `dbosctl schedule pause \| resume <name>` | Pause / resume a schedule |
| `dbosctl schedule trigger <name>` | Fire a schedule now (prints the started workflow ID) |
| `dbosctl schedule backfill <name> --since --until` | Replay a schedule over a window (prints the started workflow IDs) |
| `dbosctl api-key list` | List API keys (aliases: `token`, `apikey`) |
| `dbosctl api-key create <name>` | Create an API key — prints the secret once; scope with `--app`/`--permission` |
| `dbosctl api-key rename <name> <new-name>` | Rename an API key; the secret is unchanged |
| `dbosctl api-key delete <name>` | Delete an API key |
| `dbosctl permission list` | List grantable permissions |
| `dbosctl config list \| show \| use \| set` | Manage profiles |
| `dbosctl sysdb migrate` | Create or upgrade a DBOS system database |
| `dbosctl sysdb reset` | Empty the DBOS system database |
| `dbosctl sysdb rename-application` | Re-own rows after an application is renamed (alias: `rename-app`) |
| `dbosctl version` (or `--version`) | Print version information |

## sysdb

`sysdb` groups the commands that open a database instead of calling Conductor.
They connect to PostgreSQL (or CockroachDB) directly, take a database URL rather
than a profile, and honour none of the [common flags](#common-flags). `-D` and
`--schema` are defined once on the group, so every subcommand shares them:

```sh
dbosctl sysdb migrate -D postgres://user:pass@host:5432/dbos_sys
DBOS_SYSTEM_DATABASE_URL=... dbosctl sysdb migrate
```

The subcommand names are the ones Python, TypeScript, and Go use for the same
operations. The grouping is this CLI's own grammar — it groups by noun
everywhere else — not a renaming.

### sysdb migrate

Creates the system database and schema if they are missing and applies whatever
migrations they lack.

It is safe to re-run: migrations already recorded are skipped, and an up-to-date
database is left alone. The system schema is shared by every DBOS SDK, and the
migrations are vendored into this binary, so provisioning a database does not
mean picking an SDK and installing its toolchain.

| Flag | Purpose |
|---|---|
| `-D`, `--db-url` | System database URL (else `$DBOS_SYSTEM_DATABASE_URL`) |
| `--schema` | Schema holding the system tables (default `dbos`) |
| `-r`, `--app-role` | Grant this role access to the system tables, so the application need not own the database |
| `--no-listen-notify` | Leave out the triggers that fire `pg_notify` |
| `--cockroach` | Render the printed SQL for CockroachDB (print mode only) |
| `--print-migrations all\|N` | Print the SQL from that migration onward instead of running it |
| `--print-user-role` | Print the `--app-role` grants instead of running them |

By default the system schema carries a trigger that fires `pg_notify` when a
message is sent, so a waiting application is woken rather than left to poll.
That does not work everywhere. CockroachDB has no LISTEN/NOTIFY at all, which
`migrate` detects and handles without being asked. A connection pooler in
transaction mode also breaks it — the notification arrives on a session the
application does not keep — and nothing can detect that, so pass
`--no-listen-notify`:

```sh
dbosctl sysdb migrate -D postgres://... --no-listen-notify        # e.g. behind PgBouncer
```

The database is complete either way and reports the same migration version; only
the triggers differ, and the applications fall back to polling. In print mode
the flag is the only signal there is — nothing to detect against — so the
generated script says in its header which of the two it is.

For the same reason, a printed script for CockroachDB has to be asked for:

```sh
dbosctl sysdb migrate --print-migrations all --cockroach > schema.sql
```

CockroachDB differs in more than the triggers — no `ALTER FUNCTION … SET
search_path`, a different statement for migration 28, no `DROP TRIGGER` before
v25, no `CONCURRENTLY` — and a script for the wrong engine fails partway
through, leaving a half-migrated database. Live migration needs none of this: it
asks the server, so `--cockroach` there is an error rather than an override.

The print modes never connect, and write nothing but SQL and comments to stdout,
for a database whose DDL goes through review:

```sh
dbosctl sysdb migrate --print-migrations all > schema.sql
dbosctl sysdb migrate --print-user-role -r myapp_role > grants.sql
```

Because the SQL is CREATE/DROP INDEX CONCURRENTLY in places, run those scripts
outside a transaction block — plain `psql`, not `psql --single-transaction`.

Postgres (and CockroachDB) only. A SQLite system database is migrated by the
application process that opens it.

### sysdb reset

Deletes the DBOS rows, leaving the schema migrated and immediately usable:

```sh
dbosctl sysdb reset -D postgres://user:pass@host:5432/dbos_sys
```

This is narrower than `dbos reset` in the SDK CLIs, which drop the database.
Two reasons. `--schema` exists so the DBOS tables can share a database with
application tables, and a system database is [shareable between
applications](https://docs.dbos.dev/explanations/sharing-a-system-database) —
so dropping it reaches past what was asked about. And emptying needs only the
privileges `--app-role` grants, which is the point of provisioning out of band
in the first place.

The migration history is kept, so nothing has to migrate the database again
afterwards. That is what makes this usable between test runs, and on
CockroachDB it is the difference between cheap deletes and a schema change.

| Flag | Purpose |
|---|---|
| `-a`, `--app <name>` | Empty only this application's rows, for a shared system database |
| `--drop-database` | Drop the whole database instead |
| `--force` | Skip the confirmation prompt (required when non-interactive) |
| `-o`, `--output` | `table` (default) or `json` |

`--app` deletes the rows that name it and lets the foreign keys take the rest:
every workflow-keyed table cascades from `workflow_status`, so a workflow's
steps, messages, events, and streams go with it. Other applications in the same
schema are untouched, and so are rows no application owns.

Unlike the `--app` the Conductor commands take, this one resolves from the
command line only — no `$DBOS_APP`, no profile. What decides how much of a
database to destroy should have to be typed.

`--drop-database` is the blunt version, and cannot be combined with `--app` or
`--schema` — both describe work inside the database it destroys.

Only the tables DBOS creates are emptied. They are named in the binary rather
than read from the catalog, so pointing `--schema` at a schema that also holds
application tables empties the DBOS ones and leaves the rest alone.

That list is also why both `reset` and `rename-application` refuse a schema
migrated past what the binary knows: a migration this build has never seen may
have added a table it would silently skip, leaving it full — or, for a rename,
leaving its rows under the old name — while reporting success. The system schema
is shared by every SDK and they release on their own schedules, so meeting a
newer database is normal rather than alarming. Upgrade dbosctl.

Per-table row counts go to stdout, with progress on stderr, the same split
`rename-application` uses — a table by default, `-o json` for a scripted reset.
`--drop-database` prints none — the tables go with the database — and neither
does a failure, since the deletes are one transaction and a rollback removed
nothing.

### sysdb rename-application

An application owns what it creates, keyed by its configured name, so renaming
it strands those rows under the old name. This moves them:

```sh
dbosctl sysdb rename-application --from old-name --to new-name
dbosctl sysdb rename-app --to new-name --adopt-unclaimed-rows   # same command
```

**Stop the application being renamed first.** Nothing here locks it out, and a
running one keeps dequeuing under its old name.

| Flag | Purpose |
|---|---|
| `-f`, `--from` | The application's previous name; omit to only adopt unclaimed rows |
| `-t`, `--to` | The application that ends up owning the rows (required) |
| `--adopt-unclaimed-rows` | Also take rows no application owns (`application_name` is null) |
| `--batch-size` | Workflows and steps re-owned per transaction (default 10000) |
| `--force` | Skip the confirmation prompt and the `--to` name warnings (required when non-interactive) |
| `-o`, `--output` | `table` (default) or `json` |

Rows no application owns predate system-database sharing, so claiming them is a
decision rather than a default: naming neither source is an error rather than a
rename that reports moving nothing.

`--to` is looked at before anything moves. A name that is only whitespace is
refused outright — no application can be configured with it, so the rename would
move a whole history somewhere nothing will look for it again. Two others are
reported and asked about rather than refused:

- a name outside `^[a-z0-9-_]{3,256}$`. DBOS Transact has no limits on application
  name, but DBOS Conductor does. A name that doesn't match this regular expression
  cannot be registered or used with DBOS Conductor. DBOS Cloud keeps a shorter
  limit of its own, checked when an application is registered there.
- a name that already owns rows in the schema. Merging two applications is a real
  thing to want, and it is what `--to` alone with `--adopt-unclaimed-rows` does
  on purpose; it is also what naming the wrong existing application looks like.

Each is also exactly what the corresponding mistake looks like — a stray capital
or space, and a `--to` naming the wrong existing application — and nothing here
can tell the two apart. So both are shown above the confirmation prompt and the
answer decides them. `--force` skips that prompt and the two questions with it,
including the query that asks the database whether the name is taken.

Queues, schedules, versions, and in-flight workflows move in one transaction — a
half-owned application would dequeue work whose version row it can no longer
see. Terminal workflows and their steps are unbounded, so they move in batches
of `--batch-size` keys, and an interrupted run resumes rather than starting
over. The moved-row counts go to stdout, with progress on stderr — a table by
default, `-o json` for a scripted rename.

Those counts are rows *durably* moved. A failure inside the opening transaction
reports nothing, because the rollback moved nothing; a failure in the batched
tail reports what committed before it, which is where a re-run picks up.

Like `reset`, this refuses a schema migrated past what the binary knows, and
refuses before moving anything.

A schema too old for the feature is refused too, and so is one part way into
it. Migrations 100-104 add `application_name` a table at a time and each commits
on its own, so a `migrate` interrupted inside that range leaves some of these
tables carrying the column and some not. The rename names the tables that are
short and tells you to finish migrating, rather than re-owning the ones it can
and leaving you to work out from a zero why the rest stayed put.

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
| System database (`sysdb`) | `-D`, `--db-url` | `DBOS_SYSTEM_DATABASE_URL` |
| Output format | `-o`, `--output` | — |

Flags are scoped to the command that uses them, so pass them **after** the
command name (`dbosctl app list --org acme`), and each command's `--help` lists
only the flags it honors.

## Output

Human-readable tables by default; `-o json` emits the raw API shape for
scripting (never truncated or reprojected):

```sh
dbosctl app list                    # aligned table
dbosctl app list -o json            # raw JSON array
dbosctl whoami -o json              # raw UserProfile
```

Commands with a natural ID also accept `-o ids` (one ID per line), for piping —
a literal `-` reads IDs from stdin:

```sh
dbosctl workflow list -a myapp --status PENDING -o ids | dbosctl workflow cancel -a myapp -
```

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | General error |
| `2` | Usage error (bad flags/arguments) |
| `3` | Authentication required (HTTP 401) — run `dbosctl login` |
| `4` | Not found (HTTP 404) |
| `130` | Interrupted (Ctrl-C) |

## Development

```sh
make generate    # regenerate the API client from the vendored OpenAPI spec
make build       # build ./dbosctl
make test        # unit tests
make lint        # golangci-lint
make snapshot    # build all-platform artifacts without publishing

make test-integration              # conductor-backed tests (needs Docker)
make test-sysdb ENGINE=postgres    # sysdb tests against one engine
make test-sysdb ENGINE=cockroach
```

The generated client (`internal/api`) is committed; CI fails on spec drift
(`make generate` must be a no-op).

The system-database migrations in `internal/migrations` are the master copy: new
migrations are written there and the SDKs follow, so nothing regenerates them
from a transact checkout. `internal/migrations/doc.go` covers what adding one
involves, and why migrations numbered 100 and up cannot be added here alone.

Integration tests are tagged `integration` and stand up real Conductor +
Postgres in throwaway containers — see `make test-integration` and
`.env.example` for the required license key and image/checkout settings.

The sysdb tests are their own tier, run once per supported system database.
They cover every `sysdb` command, since all three run real SQL the two engines
do not always agree on. They skip unless `DBOS_TEST_ENGINE` names one, which is
what `make test-sysdb` sets; CI runs a job per engine, so a failure says which
one broke. That tier needs no license key, so it still gates fork PRs, where the
conductor tier can only skip.


### Update flake.nix

If go.sum is updated, the `vendorHash` value in flake.nix has to be updated too.

The updated `vendorHash` can be generated via the nixos/nix docker container 
using the following command:

```sh
docker run --rm \
  -v "$(pwd)":/workspace \
  -w /workspace \
  nixos/nix \
  sh -c "git config --global --add safe.directory /workspace && nix --extra-experimental-features 'nix-command flakes' build"
```

### Publishing a Release

```sh
git checkout main && git pull   # release from origin/main
make lint && make test          # CI reruns these, but fail locally first
make snapshot                   # optional: build all-platform artifacts, no publish
git tag -a v0.9.0 -m "v0.9.0"
git push origin v0.9.0
```
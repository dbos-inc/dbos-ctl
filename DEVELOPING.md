# Developing dbosctl

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

## Prebaked test database images

The SDK test suites run against images whose DBOS system schema is already
migrated, published to this repo's GitHub Packages registry:

```
ghcr.io/dbos-inc/dbos-test-postgres:{14,15,16,17,18}-m108
ghcr.io/dbos-inc/dbos-test-postgres:{14,15,16,17,18}-nolisten-m108
ghcr.io/dbos-inc/dbos-test-cockroach:{25.2,25.4,26.2}-m108
```

Every version DBOS supports, which for PostgreSQL is every version upstream
still supports. CockroachDB starts at v25.2: v24 is still in product support
there but is not a supported DBOS system database, and the migration set does
not apply to it. The list lives in `supported-databases.json`, which is
also where the `migrations` tier in `ci.yml` builds its matrix from: the images
are built for exactly the set the migrations are tested against, and adding a
version is one edit rather than two that can drift.

Each PostgreSQL version is built twice. The default carries the triggers that
fire `pg_notify`, and the `-nolisten` images are what `--no-listen-notify`
produces: the same schema without them, which is how a deployment behind a
connection pooler in transaction mode has to run. That configuration is
supported, so the suites need to be able to test against it. CockroachDB has no
LISTEN/NOTIFY on any release, so it has nothing to vary and gets one image per
version.

Both variants report the same migration version -- a migration that renders
empty still occupies its number -- so an SDK sees a fully migrated database
either way and skips migrating regardless of which it is handed.

They exist for CockroachDB, which runs every DDL statement as an online schema
change: migrating one database costs around a minute there against under a
second on PostgreSQL. The PostgreSQL images are built the same way so a suite
can treat the two engines identically, not because they save meaningful time.

`.github/workflows/test-images.yml` builds and publishes them on every push to
`main` that touches the migrations, the Dockerfiles or the supported version
list -- anything that changes what ends up inside an image. Each image carries four databases named
`dbos_test_N`, already at the latest migration, and each architecture is built
on a native runner -- baking the schema means running the database and applying
the whole corpus, which is not something to do under emulation.

The schema is generated during the build rather than committed, so an image
cannot ship a migration set older than the source it came from.

There are three kinds of tag:

| tag | moves? | for |
|---|---|---|
| `16-m108` | never | a suite that wants a run to mean the same thing next month |
| `16` | on every migration | a suite that wants PostgreSQL 16 and always the current schema |
| `latest`, `latest-nolisten` | on every migration, and when the newest supported version changes | a suite that just wants a current database |

`latest` names the newest supported version of that engine, flagged in
`supported-databases.json` rather than computed from sort order, so moving it is
a deliberate edit rather than a side effect of adding a row.

A `-mNNN` tag is never rebuilt: the workflow drops any target already published
at the current migration version, so dispatching it twice is a no-op. That is
not only about build time. Rebuilding cannot reproduce the image it replaces --
`initdb` writes a fresh system identifier into the cluster, CockroachDB a fresh
cluster ID -- so an unchanged source tree still yields different bytes, and
without the check a second run would leave `16-m108` pointing at a digest
nobody asked it to move to.

The `force` dispatch input exists for the case that otherwise blocks: a base
image picking up security fixes with no migration change. It republishes the
`-mNNN` tags, which is the whole point of it and also the reason it is not the
default.

An image does not have to be current to be worth using. Every SDK gates on
`current < latest`, so a database ahead of an SDK is left alone and one behind
gets only the tail applied: against an image one migration stale, catching up
measured 1.8s where the full corpus was 62.5s.

What an image cannot do by itself is make a suite faster. A suite that creates
its own databases still pays the migration, and one that creates a database the
image already has will fail outright -- adopting these means pointing a suite at
the baked names, not just swapping its base image.

To build one locally, generate the schema and hand it to the Dockerfile:

```sh
go run ./cmd/dbosctl sysdb migrate --print-migrations all --cockroach > docker/schema.cockroach.sql
docker build -f docker/Dockerfile.cockroach docker \
  --build-arg BASE_IMAGE=cockroachdb/cockroach:latest-v26.2 -t dbos-test-cockroach:local

go run ./cmd/dbosctl sysdb migrate --print-migrations all --no-listen-notify > docker/schema.postgres-nolisten.sql
docker build -f docker/Dockerfile.postgres docker \
  --build-arg BASE_IMAGE=postgres:16 --build-arg SCHEMA_FILE=schema.postgres-nolisten.sql \
  -t dbos-test-postgres:local-nolisten
```

The sysdb tests are their own tier, run once per supported system database.
They cover every `sysdb` command, since all three run real SQL the two engines
do not always agree on. They skip unless `DBOS_TEST_ENGINE` names one, which is
what `make test-sysdb` sets; CI runs a job per engine, so a failure says which
one broke. That tier needs no license key, so it still gates fork PRs, where the
conductor tier can only skip.


## Update flake.nix

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

## Publishing a Release

```sh
git checkout main && git pull   # release from origin/main
make lint && make test          # CI reruns these, but fail locally first
make snapshot                   # optional: build all-platform artifacts, no publish
git tag -a v0.9.0 -m "v0.9.0"
git push origin v0.9.0
```
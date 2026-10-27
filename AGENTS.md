# AGENTS.md

Weave is a RAG pipeline engine for Go. It ingests documents, chunks them, embeds the chunks, stores the vectors, and retrieves and assembles context for a prompt, with a Forge extension and a Forge dashboard contract on top.

There are two modules. The root is `github.com/xraph/weave`. The nexus embedder, `github.com/xraph/weave/embedder/nexus`, has its own `go.mod` so the root never depends on nexus, and it builds against the working tree through `replace github.com/xraph/weave => ../..`. That replace is the only one allowed. The release workflow tags the root as `vX.Y.Z` and the sub-module as `embedder/nexus/vX.Y.Z`.

## Commands

Go comes from `go.mod`. Build with `GOWORK=off` so you see what CI sees.

```bash
GOWORK=off go build ./...
make test          # go test -v ./...
make test-race     # go test -race -v ./...
make vet           # go vet ./...
make lint          # golangci-lint run ./...
make lint-fix      # golangci-lint run --fix ./...
make fmt           # gofmt -s -w . then goimports -local github.com/xraph/weave
make tidy          # go mod tidy and go mod verify
```

The root targets skip `embedder/nexus`, because Go leaves out any directory with its own `go.mod`. Each has a sub-module twin that loops over `SUBMODULES` in the Makefile with `GOWORK=off`:

```bash
make test-submodules   # go test -v ./... in each sub-module
make vet-submodules
make lint-submodules
make tidy-submodules   # go mod tidy and go mod verify
```

`make fmt` already reaches the sub-module, since gofmt and goimports walk the whole tree.

Don't use `make build`, `make run`, `make install` or `make dev`. They point at `./cmd/weave`, which doesn't exist, so they fail. `go build ./...` is the build.

The docs site lives in `docs/` (Fumadocs, Next.js, pnpm). `make docs` serves it and `make docs-build` builds it.

### What CI runs

`.github/workflows/ci.yml` runs on every push and pull request to `main`. Most of it comes from the shared `xraph/workflows/.github/workflows/go-ci.yml@v1`, on Go 1.26 and Ubuntu only. That workflow prefers a Makefile target when one exists, so the commands below are the ones that actually execute here.

| Job | What it runs |
|---|---|
| Test | `make test` |
| Coverage | `go test -race -covermode=atomic -coverprofile=coverage.out ./...` (there's no `test-coverage` target) |
| Lint | `go build ./...` to warm the cache, then golangci-lint at the version pinned in the shared workflow |
| Verify | `gofmt -l .`, `make vet`, then `go mod tidy` and `git diff --exit-code -- go.mod go.sum` |
| Security | standalone `gosec` (only G115 excluded) and `govulncheck`, both failing the build on findings |
| Format | `goimports -l -local github.com/xraph/weave .` must print nothing |
| Docs | in `docs/`: `pnpm install --frozen-lockfile`, `pnpm types:check`, `pnpm lint`, `pnpm build` |
| Sub-modules | `make tidy-submodules` then `git diff --exit-code` on `embedder/nexus/go.mod` and `go.sum`, `make vet-submodules`, `make test-submodules`, and goimports on `embedder/nexus` |

Two things catch people out. The tidiness check fails whenever `go mod tidy` would change `go.mod` or `go.sum`, so you commit them tidy, and the standalone gosec binary never reads `.golangci.yml`, which means the gosec excludes listed there (G104, G117, G304, G402, G704) do nothing for you in the Security job.

`.github/workflows/docs-deploy.yml.txt` is not an active workflow. The `.txt` suffix keeps GitHub from running it.

### Tests and databases

The memory and SQLite backends always run (SQLite opens a file under `t.TempDir()`). Postgres and MongoDB run only when you give them a database:

| Variable | Used by |
|---|---|
| `WEAVE_TEST_POSTGRES_DSN` | `store/postgres/conformance_test.go` |
| `WEAVE_TEST_MONGO_DSN` | `store/mongo/conformance_test.go` |
| `WEAVE_TEST_PGVECTOR_DSN` | `vectorstore/pgvector` tests, on a server with the pgvector extension (`pgvector/pgvector:pg17`) |

Without them those tests call `t.Skip` and the package still reports `ok`, so unless you run with `-v` a skipped backend looks exactly like a passing one. CI sets neither variable. The Postgres and Mongo stores get no coverage there, so if you touch either one you have to run it yourself.

There are no testcontainers here. You start throwaway containers on a non-default port, because `internal/pgtest` and `internal/mongotest` fail the test outright on port 5432 or 27017 (and on any `mongodb+srv` URI) to keep you off a live database:

```bash
docker run -d --rm --name weave-pg -p 55432:5432 -e POSTGRES_PASSWORD=weave postgres
docker run -d --rm --name weave-pgvector -p 55433:5432 -e POSTGRES_PASSWORD=weave pgvector/pgvector:pg17
docker run -d --rm --name weave-mongo -p 57017:27017 mongo --replSet rs0
docker exec weave-mongo mongosh --quiet --eval 'rs.initiate({_id: "rs0", members: [{_id: 0, host: "localhost:27017"}]})'

WEAVE_TEST_POSTGRES_DSN='postgres://postgres:weave@localhost:55432/postgres?sslmode=disable' \
WEAVE_TEST_MONGO_DSN='mongodb://localhost:57017/?directConnection=true' \
WEAVE_TEST_PGVECTOR_DSN='postgres://postgres:weave@localhost:55433/postgres?sslmode=disable' \
GOWORK=off go test -v -count=1 ./store/... ./vectorstore/...
```

Run Mongo as a replica set. A single node is enough. `ReplaceSourceDocument` needs a transaction, so on a standalone server the source cases in the conformance suite fail. Give `rs.initiate` a second or two after the container starts, and keep `directConnection=true` in the DSN: the member is named `localhost:27017` inside the container, which your machine can't reach on that port.

Each Postgres test gets a random `weave_test_*` schema with `search_path` pinned to it, and each Mongo test gets a random database. Both are dropped on cleanup, so parallel packages don't collide.

The pgvector tests use `pgtest.SchemaWithVector`, which creates the `vector` extension once in `public` and pins `search_path` to the test schema, then `public`. The extension stays behind on purpose: dropping a schema that held it would drop it for every other test on that server. `TestAvailableOnPlainPostgres` runs on `WEAVE_TEST_POSTGRES_DSN` and needs that server to have no pgvector package, which the plain `postgres` image doesn't. `TestSearchFindsSmallTenantInLargeTable` fails on pgvector older than 0.8.0.

## Layout

| Path | What lives there |
|---|---|
| `*.go` (root) | package `weave`: sentinel errors, `Config` and `DefaultConfig`, the `ID` alias, `Entity`, tenant and app context helpers in `scope.go`, `ComponentInfo` and `Describer` in `component.go` |
| `id/` | TypeID identifiers and prefixes |
| `collection/`, `document/`, `chunk/` | domain types, each with its own `Store` interface |
| `store/` | the composite `store.Store` and its backends: `memory`, `postgres`, `sqlite`, `mongo` |
| `store/storetest/` | the conformance suite every backend runs |
| `vectorstore/` | the `VectorStore` interface and the `memory`, `pgvector` and `fabriq` implementations (the last is package `fabriqvec`) |
| `loader/`, `chunker/`, `embedder/`, `retriever/`, `assembler/` | pipeline components: text extraction, chunking strategies, embedders (OpenAI and local), retrievers (similarity, hybrid, MMR, rerank), context assembly with token budgets and citations |
| `embedder/nexus/` | its own module: an embedder that calls a nexus gateway, so the root never imports nexus |
| `scripts/pin-submodules.sh` | pins the sub-module's `require github.com/xraph/weave` to a release tag; only the release workflow runs it |
| `pipeline/`, `middleware/` | step pipeline and the cache, tenant and tracing middleware around it |
| `engine/` | the coordinator everything else plugs into |
| `ext/`, `plugins/` | lifecycle hook interfaces and the registry (see below) |
| `audit_hook/`, `observability/` | hook implementations for audit trails and go-utils metrics |
| `api/` | Forge HTTP handlers |
| `extension/` | the Forge extension |
| `extension/contract/` | the Forge dashboard contract for the `weave` contributor, with `manifest.yaml` embedded |
| `internal/` | `pgtest`, `mongotest`, `migrateretry` (the grove run on Postgres that retries replicas racing on grove's tracking tables, shared by `store/postgres` and `vectorstore/pgvector`), `sqllike` (LIKE escaping) and `telemetry` (the tracer, span attribute keys, error naming, component kinds, metric families and the hook error throttle that the engine, the embedders and both hook registries share) |
| `docs/` | the documentation site |
| `MIGRATION.md` | what the old templ dashboard did and where each piece went |

## Conventions

### Errors

Sentinels live in `errors.go` and all carry a `weave: ` prefix. Wrap with `%w` and the same prefix: `fmt.Errorf("weave: create engine: %w", err)`. Backends translate driver errors into those sentinels so that callers only ever branch with `errors.Is`: Postgres turns `grove.ErrNoRows` into the matching `Err...NotFound` and SQLSTATE 23505 into `ErrCollectionAlreadyExists` or `ErrDuplicateDocument`, SQLite matches `UNIQUE constraint failed`, and Mongo uses `mongo.IsDuplicateKeyError`.

At the edges, `api/helpers.go` maps not-found sentinels to `forge.NotFound`. `extension/contract/errors.go` maps them to contract codes and turns anything it doesn't recognise into `CodeInternal` with a generic message, because a store error can carry a DSN or a hostname. Keep that behaviour when you add an error.

### IDs

Every entity ID is an `id.ID` wrapping a TypeID, in the form `prefix_suffix`. The prefixes are `doc`, `col`, `chk`, `pipe` and `ingjob`. `DocumentID`, `CollectionID` and the rest are type aliases of `id.ID`, so the compiler won't stop you passing a chunk ID where a collection ID belongs. Parse untrusted input with `id.ParseWithPrefix`. `id.New` panics on an invalid prefix, which is a programming error.

### Stores

`store.Store` embeds `document.Store`, `collection.Store` and `chunk.Store`, plus `Migrate`, `Ping` and `Close`. Each backend declares `var _ store.Store = (*Store)(nil)`, builds on a `*grove.DB` (memory aside), and has a `conformance_test.go` that hands `storetest.Run` an opener returning a fresh, migrated, empty store, so when you add or change store behaviour the place for the test is a new case in `store/storetest/`, where all four backends pick it up. A test in one backend's package covers that backend only.

Migrations are grove `migrate.Group`s registered in each backend's `migrations.go`, all named `weave`, and each backend's `Migrate` runs its group through `migrate.NewOrchestrator`, so every version gets one `grove_migrations` row. Postgres and Mongo installs from v1.8 and earlier ran every `Up` on each start with nothing recorded. The orchestrator adopts them only because every `Up` is idempotent, so keep any migration you add that way (`IF NOT EXISTS` in SQL, collection and index creation in Mongo). The `.sql` files under `store/postgres/migrations/` aren't loaded by anything: the schema that runs is the one in `store/postgres/migrations.go`.

Tenant and app IDs travel on the context. Set them with `weave.WithTenant` and `weave.WithApp`, and the engine reads it back with `weave.TenantFromContext`.

### Options and the Forge extension

`engine.New(opts ...engine.Option)` takes `func(*Engine) error` options (`WithStore`, `WithVectorStore`, `WithEmbedder`, `WithChunker`, `WithLogger`, `WithMetrics`, `WithExtension` and so on). The extension uses `ExtOption`, which is `func(*Extension)` and returns no error.

`extension.New` embeds `*forge.BaseExtension`. On `Register` it loads config from the `extensions.weave` key, then the older `weave` key. It takes a store from the named grove database when `GroveDatabase` is set, or else from any `*grove.DB` it finds in the vessel container. Then it builds the engine with the app's logger and metrics (ahead of any `WithEngineOption`, so a caller's own wins), mounts the HTTP routes under `/weave` unless `DisableRoutes` is set, and provides `*engine.Engine` to the container. `Start` runs `Migrate` unless `DisableMigrate` is set.

Contract handlers in `extension/contract/` call engine methods only, never the store. That way a fix in the engine reaches the dashboard and the HTTP API together.

### Lifecycle hooks

`ext` and `plugins` define the same hook interfaces (`CollectionCreated`, `IngestStarted` and the rest), one interface per event. The engine's registry is `plugins.Registry`, and it finds hooks by type assertion, so `audit_hook` and `observability`, written against `ext`, still fire because the method sets match. If you add a hook, add it to both packages.

### Logging

Use `github.com/xraph/go-utils/log`. The engine defaults to `log.NewNoopLogger()` until you pass `engine.WithLogger`, and the extension passes the app's logger, so every engine and registry log line is live in a Forge app. Don't log `err.Error()` there: a store error can quote a DSN or a tenant key and a hook's error can quote a document. Log `telemetry.ErrorType(err)` as `error_type`, and throttle anything that could fire once per document. Inside the extension, log through `e.Logger()` with `forge.F` fields.

### Telemetry

Spans start in `engine/` (cortex calls the engine directly, so the HTTP handlers are the wrong place) with `telemetry.Tracer()`, scope `github.com/xraph/weave`. Name them "verb object" in lower case and put counts, enum strings and opaque IDs on them, never query text, content, metadata, tenant IDs or an error's text: mark a failure with `telemetry.Fail(span, err)`. Metrics go through the go-utils sink from `engine.WithMetrics`, never `otel.Meter()`, which a Forge app doesn't export. Component labels come from `telemetry.Kind` against the fixed kind sets, so a new built-in component kind needs adding there. A call to a third party starts on `baggage.ContextWithoutBaggage(ctx)`. `docs/content/docs/subsystems/observability.mdx` lists every span and metric; update it with the code.

### Lint rules that bite

`.golangci.yml` is a version 2 config, so you need golangci-lint v2.

- `errcheck` runs with `check-blank` and `check-type-assertions`, so `v, _ := x.(string)` fails. The code marks the deliberate ones `//nolint:errcheck // zero value is fine`.
- `govet` enables every analyzer except `fieldalignment`, shadowing included.
- `revive`'s `exported` rule wants a doc comment on every exported identifier.
- `nolintlint` reports a `//nolint` that no longer suppresses anything. Every existing directive names its linter and gives a reason. Follow that.
- `goimports` uses `github.com/xraph/weave` as the local prefix: standard library, then third party, then weave, as three groups.
- Test files are exempt from `gosec`, `errcheck` and `gocritic`.

Commits use conventional commits with a scope: `feat(engine): ...`, `fix(contract): ...`, `build(deps): ...`.

## Dependencies

Weave depends on these xraph modules (see `go.mod` for versions):

- `github.com/xraph/fabriq/core`, for `vectorstore/fabriq`
- `github.com/xraph/grove` and `grove/drivers/{mongodriver,pgdriver,sqlitedriver}`
- `github.com/xraph/forge`
- `github.com/xraph/go-utils` and `github.com/xraph/vessel`

`embedder/nexus` adds `github.com/xraph/nexus`, which brings its own forge and grove requirements. Keep those at or below what the root pins, or tidy in the sub-module lifts them past the root.

Cortex consumes weave, from both its root and `extension` modules. Nothing else in forgery does.

To bump an xraph dependency, `go get` each module at its release and tidy:

```bash
GOWORK=off go get github.com/xraph/grove@vX.Y.Z \
  github.com/xraph/grove/drivers/mongodriver@vX.Y.Z \
  github.com/xraph/grove/drivers/pgdriver@vX.Y.Z \
  github.com/xraph/grove/drivers/sqlitedriver@vX.Y.Z
GOWORK=off go mod tidy
GOWORK=off go build ./... && GOWORK=off go test -race ./...
```

Grove's release tags its drivers at the same version as the root, so move all four together. After a root bump, run `make tidy-submodules` too, since `embedder/nexus` reads the root's requirements through its replace. To bump nexus, `cd embedder/nexus && GOWORK=off go get github.com/xraph/nexus@vX.Y.Z && GOWORK=off go mod tidy`.

Never commit a `go.work` or a `replace` pointing at a sibling checkout. `.gitignore` doesn't exclude `go.work`, so check `git status` before you commit. The in-repo `replace github.com/xraph/weave => ../..` in `embedder/nexus/go.mod` stays: release pins the require, never the replace.

## Releasing

Releases are cut by dispatching the release workflow. Don't push a tag by hand.

1. Make sure the upstream xraph releases weave needs (fabriq/core, grove, forge) are out and pinned in `go.mod`.
2. Wait for CI on `main` to go green: `gh run list --workflow ci.yml --branch main --limit 1`.
3. Dispatch: `gh workflow run release.yml --ref main -f tag=vX.Y.Z`.
4. Then re-pin cortex to the new tag.

`.github/workflows/release.yml` checks out `main` with full history, creates `vX.Y.Z` at that commit as `github-actions[bot]` and pushes it. Then it runs `go mod download`, `go build ./...` and `go test -race -count=1 ./...` on the root module, and the same plus a tidy check on `embedder/nexus`.

Next it tags the sub-module. On `main` the sub-module's `require github.com/xraph/weave` names the previous release, which is fine for local work (the replace wins) and wrong for anyone who runs `go get`, because the replace doesn't apply to a dependency. So the workflow runs `scripts/pin-submodules.sh vX.Y.Z`, commits `chore: pin sub-modules to vX.Y.Z`, tags that commit `embedder/nexus/vX.Y.Z`, pushes the tag and resets the commit away. Only the tag reaches it; `main` never moves. Then it runs `go get github.com/xraph/weave/embedder/nexus@vX.Y.Z` against `proxy.golang.org` in an empty module to prove a consumer can resolve it.

Last, it writes release notes from `git log` since the previous tag and publishes a GitHub release with `softprops/action-gh-release`, listing both `go get` lines.

Mind the order. The tag is pushed before the build and tests run, so a red release run still leaves a published tag behind, and since the module proxy may already have fetched it you fix forward with the next patch version and leave the bad tag alone. The workflow also fires on a pushed `v*` tag. That's the path you shouldn't use.

## Branch rules

`main` has one ruleset: it blocks deletion and force-pushes. There are no required checks or reviews, so direct pushes to `main` go through, and CI runs after the push.

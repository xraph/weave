# Upgrading to v1.9.0

v1.9.0 puts a tenant on every call, so most of what follows is about that.
You'll need to change code if anything calls Weave without `weave.WithTenant`
on the context, if you wrote your own `store.Store`, or if you made the
pgvector table by hand. Rows that an older version wrote without a tenant stay
in your database where nothing can read them, and the SQL to move them is
below.

## Every call needs a tenant

Put the tenant on the context and Weave works inside it:

```go
ctx = weave.WithTenant(ctx, "acme")
```

A call without one fails with `weave.ErrTenantRequired`, and nothing gets
read or written. That covers every engine call that touches data and every
`store.Store` method except `Migrate`, `Ping`, `Close` and the two recovery
reads below. `Retrieve` refuses before it embeds the query, so a missing
tenant costs you no embedding call. Over HTTP the routes answer 403, and the
dashboard contract answers `PERMISSION_DENIED`. The built-in vector stores
refuse a `Search` with no `TenantKey` and an `Upsert` whose entry has no
`metadata["tenant_id"]`, with the same error.

A job that isn't serving a request has to build its own context with the
tenant it means. Nothing searches or lists across tenants any more, so if you
relied on a missing tenant meaning "everyone", you'll have to call once per
tenant and merge the results yourself.
[Multi-tenancy](docs/content/docs/concepts/multi-tenancy.mdx) has the call by
call table.

## Rows written without a tenant

v1.8 let you write without a tenant, and its dashboard always did. Those rows
have `tenant_id = ''`, and since `""` isn't a tenant, no call reaches them now.
Nothing deletes them either; they stay put until you give them a real tenant.

On Postgres, run this in one transaction, with `$1` bound to the tenant you're
moving them to (or a quoted literal in its place in `psql`):

```sql
BEGIN;
UPDATE weave_collections SET tenant_id = $1 WHERE tenant_id = '';
UPDATE weave_documents SET tenant_id = $1 WHERE tenant_id = '';
UPDATE weave_chunks SET tenant_id = $1 WHERE tenant_id = '';
UPDATE weave_vectors
   SET metadata = jsonb_set(metadata, '{tenant_id}', to_jsonb($1::text))
 WHERE coalesce(metadata->>'tenant_id', '') = '';
COMMIT;
```

The `coalesce` is there because v1.8's pipeline left `tenant_id` out of a
vector's metadata altogether when the row had no tenant, and a plain
`metadata->>'tenant_id' = ''` would miss those. If your vectors live in a
table you named with `WithTableName`, use that name.

SQLite takes the same first three statements, with `?` for the tenant
(there's no vector table in SQLite). On MongoDB:

```js
const tenant = "acme"
db.weave_collections.updateMany({ tenant_id: "" }, { $set: { tenant_id: tenant } })
db.weave_documents.updateMany({ tenant_id: "" }, { $set: { tenant_id: tenant } })
db.weave_chunks.updateMany({ tenant_id: "" }, { $set: { tenant_id: tenant } })
```

Collection names are unique per tenant. If the tenant you're moving to
already has a collection with the same name as one of the old ones, the
first statement fails on Postgres and SQLite, and on Mongo `updateMany` stops
at that row with the earlier ones already moved. Find the clashes first and
rename one side:

```sql
SELECT name FROM weave_collections
 WHERE tenant_id = '' AND name IN (SELECT name FROM weave_collections WHERE tenant_id = $1);
```

If the old rows belong to different tenants, run the statements once per
collection, with `AND id = '...'` on `weave_collections`,
`AND collection_id = '...'` on documents and chunks, and
`AND metadata->>'collection_id' = '...'` on `weave_vectors`.

Fabriq kept v1.8's vectors under whatever Fabriq tenant was on the context
when they were written, and SQL won't reach them. Once the rows are moved,
`ReindexCollection` under the new tenant embeds the collection again and
writes vectors that carry it.

Recovery also leaves these rows alone. It counts them in `SkippedNoTenant`
and logs one warning per sweep, and it picks them up on the next sweep once
they have a tenant.

## The dashboard runs under one tenant

Give the extension `WithDashboardTenant` so it can find the signed-in user's
tenant, or put the tenant on the request context in your own middleware. A
request with neither is `PERMISSION_DENIED`, and so is one where your
function returns an error. The `tenant` field on lists and retrieval can only
repeat the tenant you resolved. "The dashboard answers for one tenant at a
time" in the dashboard record below has the details.

## RegisterRoutes returns an error

`api.API.RegisterRoutes` and `Extension.RegisterRoutes` now return an `error`
naming each route forge refused, and `Extension.Register` fails with it. A
bare call still compiles, but it drops the error, so check it wherever you
call them yourself. `api.API.Handler()` has nowhere to return one and panics
instead.

## A borrowed Grove database stays open

When the extension builds its store from a `*grove.DB` in the container,
`Stop` used to close that database, under every other extension still using
it. It leaves it open now, because the container owns it. A store you pass
with `WithStore` is still yours to the end of `Stop`, and Weave still closes
it.

## Upsert by source

This one is new, and nothing changes unless you use it. Set
`IngestInput.SourceKey` when you ingest the same thing over and over (a docs
page you read on every deploy, say) and Weave keeps one current version per
key and collection. The same content again embeds nothing and comes back with
`Unchanged` set. New content is embedded beside the old version and swapped
in, and `Replaced` lists the IDs it replaced. A keyed ingest that finds the
same content still in flight on another replica returns that document in
state `processing` with a nil error. `DeleteBySource` removes a key. On
MongoDB a keyed ingest needs a replica set; plain ingest doesn't.
[Ingestion](docs/content/docs/subsystems/ingestion.mdx#upsert-by-source) walks
through the whole order.

## Recovery

A process killed mid-ingest used to leave its document `pending` or
`processing` forever, with some of its chunks and vectors written. Now the
extension sweeps for those every `recovery_interval` (5m by default). A
document whose last heartbeat is older than `stalled_after` (15m) is marked
`failed` with `weave.ErrIngestInterrupted`, and only then are its partial
chunks and vectors deleted, so a search never returns half a document and an
ingest that turns out to be alive keeps what it wrote.
`OnIngestFailed` fires once per document even with several replicas
sweeping. A negative `recovery_interval` turns the loop off, and a negative
`stalled_after` makes `Register` fail. Without the extension, recovery is off
until you pass `engine.WithRecovery`, or you call `RecoverStalled` from your
own scheduler.

Pick `stalled_after` well above your slowest embed call. Heartbeats only go
out between steps, so a single call that runs longer gets its document
failed underneath it.

## If you wrote your own store

`store.Store` grew six methods, and a custom store doesn't compile until it has
all of them. The built-in stores (memory, SQLite, Postgres, Mongo) already do.

- `ReplaceSourceDocument(ctx, doc, chunks)` swaps in a new version of a
  source-keyed document in one atomic step.
- `TouchDocument(ctx, docID, staleBefore)` is an ingest's heartbeat, and with a
  cutoff it's a claim only one caller wins.
- `FailDocument(ctx, docID, errText, staleBefore)` moves a pending or processing
  document to failed, and never touches a ready one.
- `CompleteDocument(ctx, docID, chunkCount)` moves a processing document to
  ready with its chunk count. It's how an ingest without a source key
  finishes, and it refuses any other state, so a document recovery has
  already failed can't come back as ready.
- `ListStalledDocuments(ctx, before, limit)` is recovery's read across tenants.
  It works with no tenant on the context and leaves out rows with an empty
  tenant.
- `CountStalledUntenanted(ctx, before)` counts the rows that listing leaves out.

The last two are the only methods besides `Migrate`, `Ping` and `Close` that run
without a tenant. The three moves have to be single guarded writes (a
compare-and-set on the stored state), not a read and then an update, or two
replicas can both win. [Writing a custom store](docs/content/docs/guides/custom-store.mdx)
spells out each rule, and `storetest.Run` checks them. If your store ever held
v1.8 rows, run `storetest.RunLegacy` too: you give it a function that writes a
document as it is, empty tenant and all, and it checks those rows never reach
recovery.

There's also a new document state, `superseded`, for an old version a replace
has swapped out and not yet purged. Every other method has to refuse a
missing tenant with `weave.ErrTenantRequired` before it touches the database.

## Schema and migrations

Postgres and Mongo migrations are tracked from now on. v1.8 ran every
migration's `Up` on each start and recorded nothing. Now `Migrate` runs the
`weave` group through Grove's orchestrator, and each version gets one row in
`grove_migrations`. You don't have to do anything when you upgrade: every
migration creates its tables and indexes only if they're missing, so the first
tracked run finds yours, leaves the rows alone and records the versions.
SQLite was tracked already. Replicas booting together on a fresh Postgres
database race on Grove's own tracking tables, and `Migrate` retries that race
up to five times.

Two new migrations run on `Migrate`, on SQLite, Postgres and Mongo:

- `20261024000100` adds `source_key` to `weave_documents`, a unique index on
  `(collection_id, source_key)` over ready keyed rows, and a lookup index on
  `(tenant_id, collection_id, source_key)`.
- `20261024000200` adds `idx_weave_documents_state_updated` on
  `(state, updated_at)`, which recovery lists stalled documents by.

Both are idempotent, so an install that already has the column or the indexes
is adopted as it stands. On a large `weave_documents` table on Postgres, the
`CREATE INDEX` takes a lock that blocks writes while it builds. Run it by hand
with `CONCURRENTLY` first if that matters to you, and `Migrate` will find it
there.

## pgvector's table

Older docs had you create the `vector` extension and `weave_vectors` by hand.
Now `pgvector.Migrate(ctx, db, dims)` does it, as its own tracked group,
`weave_pgvector`. If you run your own orchestrator, add
`pgvector.Migrations(dims)` to it instead. The extension's `Start` doesn't
migrate this table, and the `weave` group never creates an extension, so an
app without pgvector never needs it installed. `pgvector.Available` tells you
whether the server has it before you try.

A table you made by hand is adopted when its size matches `dims`. When it
doesn't, the migration fails with `weave.ErrDimensionMismatch` naming both
numbers and records nothing. Drop the old docs' unnamed HNSW index
(`weave_vectors_embedding_idx`) before the first run, or you end up with two
of them.

## Deprecated

These still compile, and only work within the tenant on the context:

- `engine.WithTenantID` and `engine.WithTenantFilter` on `Retrieve`. With no
  tenant on the context, the value becomes the tenant. With one, it has to
  match, or you get `weave.ErrInvalidArgument`. `WithTenantFilter("")` no
  longer means "rows without a tenant".
- `CompareParams.Tenant` on `RetrieveCompare`, with the same rules.
- `Tenant *string` on the collection, document and chunk list and count
  filters. Leave it nil. A non-nil value has to equal the context tenant, and
  anything else, `""` included, is `weave.ErrInvalidArgument`.

Put the tenant on the context and drop them when you next touch that code.

## Smaller things

- SQLite writers wait up to five seconds for the write lock instead of failing
  at once with `SQLITE_BUSY`, so concurrent ingests on one file queue up.
- Weave needs forge v1.13.1, grove v1.10.0 (with its three drivers) and, for
  the Fabriq vector store, fabriq/core v1.10.0.
- The nexus embedder is its own module,
  `github.com/xraph/weave/embedder/nexus`, tagged with the same version as
  Weave. You only pull nexus in when you import it.

# Dashboard migration: templ to React shell

Weave's dashboard used to render server-side with templ and ForgeUI, from
`dashboard/`. It's moving to the Forge dashboard's React shell, as
`@forge-go/dashboard-plugin-weave`, reading a `weave` contract contributor in
`extension/contract`. The templ package is gone.

Read that carefully, though, because only half of the destination is built.
The contract exists: `extension/contract` registers the `weave` contributor and
answers all 17 intents, with Go tests behind every one. The React plugin
(`packages/plugin-weave` in the forge-dashboard repo) doesn't exist yet. So no
new page has been run, let alone walked in a browser. Every entry below marked
**moved** names the React page or contract intent it is meant to become, taken
from the design spec
(`docs/superpowers/specs/2026-10-07-weave-dashboard-migration-design.md` in the
forge-dashboard repo), not from something we've watched work. Once the pages
exist we'll walk every one of them in a browser, confirm each **moved** entry
against what it really shows, and fix this file wherever a page turns out
different.

This file is the record of the move. We wrote it by reading every templ source
before deleting it: 27 `.templ` files (12 pages, 11 components, 3 widgets and 1
settings panel), their 27 generated `*_templ.go` files, and eight Go files
(`contributor.go`, `data.go`, `manifest.go`, `plugin_iface.go`,
`components/icons.go`, `pages/types.go`, `shared/pagination.go` and
`widgets/types.go`), plus `forge.contributor.yaml`. That's 63 tracked files.
Once the directory is gone there is nothing left to check against, so anything
missing from here is a feature that went missing by accident. Every page,
column, stat, action, filter, form field, badge, empty state, widget and nav
item is listed below, and each one says whether it **moved**, **changed** or
was **dropped**, and why.

The cut happened in two steps. Commit `dfe48b8` (xraph/weave#39, merged as
`e6e7949`) stopped the extension from registering the templ dashboard. Commit
`9221db3`, right after this file, deleted the `dashboard/` package on its own.

## What you need to do

If you use Weave through the extension and its routes, you don't need to change
anything in Weave's own API. Every route, request body and config key is where
it was. If you wrote your own `store.Store`, you do: it has five new methods to
implement, see "If you wrote your own store" under "Upgrading to v1.9.0" at the
top of this file. One thing you might notice: the retrieve route now returns
hydrated hits (see "Fixed in Weave on the way"), so each hit carries
its real chunk ID, document ID and collection ID. Existing clients keep
working.

`Extension.DashboardContributor()` is gone. #39 removed it, along with the
extension's templ registration. If your own code imports
`github.com/xraph/weave/dashboard` (or any package under it), that import has to
go, because the package no longer exists. Nothing else in Weave imports it: we
grepped the module for it before deleting. (Mind the grep if you're on macOS.
BSD grep prints paths without a leading `./`, so a filter written as
`grep -v '^./dashboard/'` doesn't filter anything there. Filter on
`^dashboard/` or use `--exclude-dir`.)

Once the plugin exists, add it to your shell:

```tsx
import weavePlugin from "@forge-go/dashboard-plugin-weave"

const plugins = [corePlugin, weavePlugin]
```

The pages mount at `/@weave`, under a nav group called RAG. They use Tailwind
classes of their own, so the shell's stylesheet has to scan the package. If your
shell declares its sources with `@source`, add
`@source "<path to>/packages/plugin-weave/src";` next to the others.

Weave needs forge v1.12.0, and grove v1.7.0 with it. v1.12.0 is the first forge
release whose dashboard packages import neither templ nor forgeui, and `go.mod`
pins both. `github.com/a-h/templ` and `github.com/xraph/forgeui` are gone from
the module.

The contributor, the extension and the plugin are all named `weave`. The
contributor's capabilities are `weave.read` and `weave.write`.

The dashboard answers for one tenant at a time. Give the extension
`WithDashboardTenant` so it can find the signed-in user's tenant, or put the
tenant on the request context in a middleware of your own. A request with
neither is refused with `PERMISSION_DENIED`. Lists and retrieval still take a
`tenant` field, but it can only repeat the tenant you resolved. Ingest runs under
that tenant too, so the document you ingest is yours, and so are its chunks and
vectors.

Your YAML now reaches the engine. The extension used to keep its config to
itself, so the engine ran on its built-in defaults whatever you wrote. From
this release `default_chunk_size`, `default_chunk_overlap` and `default_top_k`
take effect, and `default_embedding_model` and `default_chunk_strategy` are what
a new collection records. If you set any of them, expect different chunking on
new collections and a different `top_k` on retrieval after you upgrade.
`shutdown_timeout` and `ingest_concurrency` reach the engine too, and the
Pipeline page shows them, but nothing in Weave reads either one yet. The keys
live under `extensions.weave` (or `weave`):

```yaml
extensions:
  weave:
    default_chunk_size: 512
    default_chunk_overlap: 50
    default_embedding_model: text-embedding-3-small
    default_chunk_strategy: recursive
    default_top_k: 10
    shutdown_timeout: 30s
```

The extension refuses to start when the effective `default_chunk_overlap` is
at or above the effective `default_chunk_size`, defaults included. So
`default_chunk_size: 32` on its own fails, because the default overlap is 50. The
error names both values. Before, that config made the fixed chunker loop forever
and the recursive chunker panic on the first ingest.

Mind the dashboard's request limit if you ingest big files. Weave caps ingested
content at 1 MiB, but the dashboard transport caps the whole request at 1 MiB
first, and JSON escaping can double the size of a text file on the way (every
quote, backslash and newline becomes two bytes, a control character six). A file
just under Weave's cap is refused by the transport with a BAD_REQUEST reading
"request body exceeds 1048576 bytes". To ingest files near 1 MiB, raise the
dashboard's limit to about 3 MiB:

```yaml
extensions:
  dashboard:
    contract_max_body_bytes: 3145728
```

In Go that's `dashboard.WithContractMaxBodyBytes(3 << 20)` on the dashboard
extension. It raises the cap for every contributor's requests, not only
Weave's.

If you call Weave from Go, a few things grew, and a few things behave
differently now:

- `Engine.UpdateCollection` renames a collection and edits its description and
  metadata. The chunk settings, model, dimensions and strategy stay fixed.
- `Engine.Components()` reports the wired loader, chunker, embedder, vector
  store and retriever, and `Engine.DescribeExtensions()` lists each extension
  with the hooks it implements.
- `Engine.RetrieveCompare` runs the configured retriever next to a raw vector
  search. `Engine.Assemble` and `Engine.AssembleRefs` run the default assembler
  with a token budget you choose.
- `Engine.ListChunks` pages chunks by document or by collection, on all four
  stores.
- Collection and document list filters have `SortDesc` (newest first, ties
  broken by ID). The default order stays ascending, so offset paging doesn't
  shift under you. Document counts take `Search` and `UpdatedBefore`.
- Collection, document and chunk filters, and retrieval, take `Tenant *string`.
  It's deprecated already: the tenant comes from the context, and a non-nil
  value has to repeat it. `""` is not a tenant, so a pointer to it is
  `weave.ErrInvalidArgument`. See "Deprecated" under "Upgrading to v1.9.0".
- A duplicate document (same collection, same content hash) and a duplicate
  collection name now return `weave.ErrDuplicateDocument` and
  `weave.ErrCollectionAlreadyExists` on all four stores. Before, only the memory
  store typed the document error, and the other three passed the raw constraint
  error through.
- SQL search treats `%`, `_` and `\` as literal characters. A search for `50%`
  used to be a wildcard on SQLite and Postgres.
- The memory store and pgvector break score ties by ID, so two searches over the
  same data agree.

## Fixed in Weave on the way

Showing the engine's data honestly meant fixing the engine first. We did it
test first, and all four stores (memory, SQLite, Postgres, Mongo) pass the
same conformance suite with no skips. These are the
things that were wrong, and that the templ pages had been hiding.

- Retrieval hits came back anonymous. The similarity and MMR retrievers and the
  engine's own fallback built each result chunk from `Content` and `Metadata`
  only, so chunk ID, document ID, collection ID, index, offsets and token count
  were all zero. The vector store does return the chunk ID, and the engine
  dropped it. The engine now keeps it and reads each hit back from the chunk
  store. A vector whose chunk row is gone stays at its rank and is marked
  orphaned, never dropped. That costs one `GetChunk` per hit, which is fine at
  `TopK` scale and worth a batch getter if `TopK` grows.
- Postgres, SQLite and Mongo dropped `created_at` and `updated_at` when reading
  collections and documents back, so both read as the zero time. Memory was
  fine.
- Postgres document filters didn't work at all. Every filtered list and count
  failed with `could not determine data type of parameter $1 (SQLSTATE 42P18)`,
  because `ListDocuments`, `CountDocuments` and `CountChunks` hard-coded `$1`,
  `$2` and `$3`, and grove only rewrites `?`. The templ pages threw the error
  away, which is why every state count on Postgres read 0.
- Mongo's search compiled your text as a regular expression, so a `(` was an
  error.
- SQLite can't take `OFFSET` without `LIMIT`, and the grove driver drops a
  negative limit without saying so. The store now sends `math.MaxInt32` as the
  limit when you give an offset and no limit.
- Postgres and Mongo refused to store a collection, document or chunk with no
  metadata map, because the mappers wrote a nil map into a `NOT NULL` column.
  They store an empty map now.
- Every list sorted `created_at` ascending, so every "recent" list was the
  oldest rows. `SortDesc` exists now, and the dashboard asks for it.

- The dashboard answered for every tenant. Anyone who could sign in to it could
  list, read, edit and delete any tenant's collections, and ingest into them,
  because its requests carried no tenant and an absent `tenant` meant all of
  them. Every intent now runs under the tenant the host resolves for the
  signed-in principal, through `extension.WithDashboardTenant` (or the tenant on
  the request context when you don't set it). No tenant, or a resolver error, is
  `PERMISSION_DENIED`, and the resolver's error text stays on the server. A
  `tenant` in the request can only repeat the resolved one. Another tenant's ID
  is not found, and ingest into another tenant's collection writes nothing. The
  Overview's `scope` reads `"tenant"` now.
- Route registration errors used to vanish. The API threw away the error from
  every route it registered, so a route forge refused just didn't exist, and you
  found out from a 404. `api.API.RegisterRoutes` and `Extension.RegisterRoutes`
  now return an `error` naming each refused route, and `Extension.Register`
  fails with it. Check it wherever you call them yourself. A call written as a
  bare statement still compiles, but it drops the error, which is the old
  behaviour. `api.API.Handler()` has no error to return, so it panics when a
  route fails to register.

One finding carries into the open list below: `pipeline/steps.go` omits
`tenant_id` from the vector metadata for untenanted rows.

## The templ dashboard's own defects

These went with the package. We list them so nobody mistakes one for a feature
and goes looking for it in the React pages, and so you can stop wondering why the
old delete button never deleted anything.

- Create, edit and delete called routes that don't exist. The forms and dialogs
  posted to `/weave/collections` and `/weave/documents/:id`, but Weave's routes
  live under `/weave/v1/`. Edit sent a `PUT` that nobody serves (there is no
  update route at all), and the forms were form-encoded against handlers that
  read JSON. None of the three ever worked.
- The delete dialog's success handler throws. It calls `.close()` on the dialog
  element, which is a `div` with data attributes and not a `<dialog>`, so the
  call fails before the page refreshes. It never got the chance to matter,
  because the request 404'd first.
- "Recent" lists showed the oldest rows. The Overview's Recent Ingestions, the
  collection's Recent Documents and the recent-ingestions widget all called
  `ListDocuments` with a limit and the store's default ascending order.
- Postgres state counts were always 0 (see above), and so were the document
  and chunk counts on every collection row, on every backend. Nothing ever
  updated `Collection.DocumentCount` or `ChunkCount`, and the table printed
  them anyway.
- Retrieval results never linked to a chunk or a document. The View Chunk
  button and the document link both checked for a non-empty ID, and every
  retrieval path had thrown the ID away, so neither ever rendered. The
  collection badge, the Index and the Tokens line (always 0) had the same
  problem.
- Errors were swallowed everywhere. Every page ignored the error from its
  counts, its collection list, its chunk list, its recent-documents read and its
  retrieval call. A missing embedder showed "No results". A failed read showed
  an empty table or a zero.
- The Pipeline page and the Pipeline Health widget reported every stage Active
  whatever was wired. Loader and chunker were hard-coded true, and the other
  three were "the store isn't nil", which the page had already checked.
- The Loaders page was a hard-coded list. It named file extensions as if they
  were MIME types, listed `url`, `http`, `https`, `directory` and `dir` as types
  nothing accepts, and missed the ones the loaders do (`text/x-markdown`,
  `application/xhtml+xml`, `text/uri-list`, and the empty type the text loader
  takes).
- Content length was labelled "Characters" and "chars". It's bytes of the raw
  input. The chunk page called its byte offsets a "Character range". The
  offsets are byte offsets into the text after loading and trimming.
- The Documents count badge, and the page count under it, ignored the search.
  `CountFilter` for documents had no search field, so a searched list reported
  the unsearched total.
- Links and filters joined values into URLs with no escaping, so a search or a
  prefix holding `&`, `#` or `+` broke the filter and the paging.
- Every truncation sliced bytes (`s[:max-3]`), so a multibyte character on the
  boundary came out as a broken rune.
- Metadata maps printed in Go's map order, which changes from one render to the
  next.
- A bad collection filter was silently ignored. On the Documents page and on
  Retrieval, a collection ID that failed to parse became "no filter", so you
  searched everything and weren't told. A negative `limit` or `offset` quietly
  became the default.
- The collection form showed the engine's default overlap whenever a collection
  stored 0, so an overlap of 0 was never visible.
- The topbar had a search box (`ShowSearch`) and the manifest declared
  `searchable`. Nothing ever handled a search.
- The Pipeline and Settings pages printed `Engine.Config()`, and the extension
  never passed its config to the engine (no `WithConfig`). So they showed the
  engine's built-in defaults, never the YAML you wrote.
- The unknown-state branch of the state badge printed whatever string it was
  given, and the Documents page passed the `state` query value to the store
  without checking it.
- `JSONViewer` was never used by any page.

## Deliberately dropped

- The three widgets (RAG Stats, Recent Ingestions, Pipeline Health). The
  Overview covers what each one showed, and plugins have no widget slot.
- The settings panel `weave-config`. It was a read-only copy of the Pipeline
  page's configuration, plus the extension names the Pipeline page now lists.
- The five templ plugin hooks in `plugin_iface.go`: `Plugin`,
  `CollectionDetailContributor`, `DocumentDetailContributor`,
  `RetrievalResultContributor` and `PageContributor`. Nothing in Weave or its
  plugins implements any of them, and each one returns a `templ.Component`.
- The `searchable` capability and the topbar search. They were declared and
  never implemented.
- The hard-coded Loaders list. The Pipeline page reports content types probed
  from each loader's `Supports` method instead.
- The strategy select on Retrieval (Similarity, MMR, Hybrid). `WithStrategy` sets
  `RetrieveParams.Strategy` and nothing reads it. The engine runs whichever
  single retriever it was given, or a plain vector search if it was given none.
  The page names the configured retriever from `system.components`.
- The per-stage "Active" badges. They said Active no matter what.
- The score bar and the score colours on a retrieval hit. A cosine value means
  different things on different embedding models, and a colour or a 0 to 1 bar
  claims a quality judgement nobody can make.
- The Embedding Model and Strategy columns on the Collections table, and the
  Embedding Dimensions stat. Weave records them and never uses them: the engine
  has one global embedder and one global chunker, and only chunk size and chunk
  overlap take effect.
- The chunk "Parent" stat. `Chunk.ParentID` is stored and read back, and nothing
  in the engine, the chunkers or the pipeline ever sets it.

Everything else that was dropped says why where it appears below.

## Blocked

Every templ surface either has a React replacement or is dropped with a reason,
but a **moved** or **changed** entry isn't live until the work behind it lands.
Two things are left:

- Every **moved** and **changed** entry waits on the React plugin
  (`packages/plugin-weave` in the forge-dashboard repo). It doesn't exist yet.
- After that, every **moved** entry still has to be confirmed by walking the
  page in a browser.

The rest is done. `extension/contract` answers every intent, the extension
hands the engine its config, and Weave is on forge v1.12.0 and grove v1.7.0.

## Page by page

Status is one of **moved** (same thing, same place, as far as the design says),
**changed** (the behaviour is different on purpose, with the reason) or
**dropped** (gone, with the reason).

A few things hold across every React page. Every list takes `limit` and
`offset` and answers `items`, `total`, `limit` and `offset`, with a default of
25 and a maximum of 100, where the templ pages paged 20 at a time. A negative
`limit` or `offset` is refused as a bad request. Every list takes an optional
`tenant`. Wire field names are snake_case. No response carries a vector or an
embedding, and a Go test marshals every handler's response to prove it. Errors
are typed (not found, bad request, conflict, unavailable naming what is
missing, and a generic internal error that is logged), and none is swallowed.
Every collection, document and chunk ID is `font-mono text-xs`.

The templ pages built every URL by joining strings. The React pages use the
shell's router, so none of that survives.

### Route map

templ routes are relative to the contributor, and every page and action went
through htmx swaps of `#content`.

| templ route | React route | status |
|---|---|---|
| `/` | `/` | moved |
| `/collections?search=&limit=&offset=` | `/collections` | moved |
| `/collections/create` | `/collections/new` | moved |
| `/collections/detail?id=` | `/collections/:id` | moved |
| `/collections/edit?id=` | `/collections/:id/edit` | changed: edits name, description and metadata only, see Collection form |
| `/documents?search=&state=&collection=` | `/documents` | moved |
| `/documents/detail?id=` | `/documents/:id` | moved |
| `/chunks?doc=` and `/chunks/browser?doc=` | `/chunks`, opening on a collection picker | changed: the templ page needed a document ID and dead-ended without one |
| `/chunks/detail?id=` | `/chunks/:id` | moved |
| `/retrieval?q=&collection=&strategy=&top_k=&min_score=` | `/retrieval` | changed: a query never goes into the URL, it can hold customer text and URLs land in tracing |
| `/pipeline` | `/pipeline` | moved |
| `/loaders` | `/pipeline` | changed: folded in, see Loaders |
| `/extensions` | `/pipeline` | changed: folded in, see Extensions |
| a plugin page (`PluginPage`, `PageContributor`) | none | dropped: see Plugin extension points |
| the "Page not found" empty state for an unknown route | the shell's own not-found | changed |
| the "Engine not initialized" empty state | an UNAVAILABLE error naming the engine | changed |
| the "No store configured" empty state | an UNAVAILABLE error naming the store | changed |
| an unparseable or missing `id` returning a page-not-found error | a bad-request error for an ID that won't parse, not-found for one that doesn't exist | changed |

### Overview

`pages/overview.templ`, and `components/stat_card.templ` for its cards.

| templ | React | status |
|---|---|---|
| Title "Overview" | `/`, "Overview" | moved |
| Subtitle "Weave RAG pipeline dashboard" | one line saying this dashboard shows one tenant's data, the signed-in user's (`scope` reads `"tenant"`) | changed: the thing worth saying on this page changed |
| Stat card Collections, "Data groups" | `StatGrid`, `system.overview` | moved |
| Stat card Documents, "Total ingested" | documents by state | changed: the design shows the four states, so the total is their sum and not a card of its own |
| Stat card Ready, "Searchable documents" | `StatGrid`, documents by state | moved |
| Stat card Processing, "In progress" | `StatGrid`, documents by state | moved |
| Stat card Failed, "Needs attention" | `StatGrid`, documents by state | moved |
| Stat card Pending, "Awaiting processing" | `StatGrid`, documents by state | moved |
| Stat card Chunks, "Searchable segments" | `StatGrid`, chunk count | moved |
| Card subtitles ("Data groups", "In progress" and the rest) | none | dropped: they restated the label |
| Card icons (folders, file-text, check-circle, activity, alert-circle, clock, puzzle) | the kit's `StatGrid` draws the cards | changed |
| Counts read with errors thrown away, so a failure showed 0 | the query fails and says so | changed |
| Card "Recent Ingestions", "Last 10 ingested documents" | a newest-documents table, the 10 newest (`system.overview`) | changed: it showed the 10 oldest, and "newest first" is real now |
| Column Title, a link to document detail, "Untitled" in italics when empty | Title, a link to `/documents/:id` | moved |
| Column Collection, a link showing the collection name, or the ID when the name wasn't found | Collection, a link | changed: the name comes from the contract, where templ rebuilt a map from a full collection listing on every render |
| Column State, a `StateBadge` | Document state badge | changed: new variants, see Badges |
| Column Chunks | Chunks | moved |
| Column Actions, a View button | none | dropped: the title is already a link to the same place |
| Empty state "No documents yet", "Ingest documents to see recent activity" | the empty newest-documents table | moved |
| Plugin widget sections rendered below the table (`PluginSections`) | none | dropped: see Plugin extension points |

### Collections

`pages/collections.templ`.

| templ | React | status |
|---|---|---|
| Title "Collections", with a count badge from the pagination total | `/collections`, the `total` from `collections.list` | moved |
| Description "Manage document collections" | none | dropped: it restated the title |
| Button "New Collection" | a button to `/collections/new` | moved |
| Search "Search collections...", `search` param, 300 ms debounce, URL updated | name search on `collections.list` | moved |
| Search value joined into the URL with no escaping | the shell's router | changed |
| Empty state "No collections found", "Create your first collection to get started" | the empty `ResourceTable` | moved |
| Column Name, `font-medium`, a link to detail | Name in `font-medium`, a link to `/collections/:id` | moved |
| Description under the name, cut to 60 bytes with "..." | the description under the name | changed: cut by width, not by byte |
| Column Embedding Model, an outline badge | none | dropped: recorded and never used, see Deliberately dropped |
| Column Strategy, a secondary badge | none | dropped: same |
| Column Docs, always 0 | live document count | changed: counted from the store, not read from a column nothing updates |
| Column Chunks, always 0 | live chunk count | changed: same |
| Column Actions: View, Edit, Delete | none on the list | changed: the name is the link, and Edit and Delete sit on the detail page |
| Row Delete opening a `ConfirmDialog`, "Delete Collection", "All documents and chunks will be permanently deleted." | `ConfirmDialog` on the detail page, `collections.delete` | changed: it works now, and it sets `pending` and shows an error inside the dialog |
| Pagination: "Page N of M (T total)", Previous and Next, 20 a page, shown only past one page | offset paging, `limit` and `offset` | changed |

### Collection detail

`pages/collection_detail.templ`.

| templ | React | status |
|---|---|---|
| Header: the name, and the description as subtitle | the name and description (`collections.get`) | moved |
| Button Back | the Collections nav item | changed: the shell's navigation does it |
| Button Edit | Edit, to `/collections/:id/edit` | moved |
| Button Delete, opening a `ConfirmDialog` with the same text as the list | Delete, `ConfirmDialog`, `collections.delete` | moved |
| Stat Documents, "In this collection", hidden when the stats read failed | stats by state | moved |
| Stat Chunks, "Searchable segments", hidden the same way | stats, chunk count | moved |
| Stat Embedding Dims, with the model as its subtitle | none | dropped: recorded and never used |
| Stat Chunk Size, with "Overlap: N" | chunk settings in the `DescriptionList` | moved |
| A failed stats read hiding two cards with no message | the query fails and says so | changed |
| Card Configuration: ID | `DescriptionList`, ID | moved |
| Configuration: Embedding Model, Embedding Dimensions, Chunk Strategy | none on the detail page | dropped: recorded and never used. The create form's "Recorded, not used" box shows what this deployment actually runs |
| Configuration: Chunk Size, Chunk Overlap | chunk settings in the `DescriptionList` | moved |
| Card Metadata, a key and value list in map order, "No metadata" when empty | metadata sorted by key | changed: a stable order |
| Card "Recent Documents", "Latest documents in this collection", the first 5 | the newest documents (`documents.list`) | changed: it showed the 5 oldest |
| Columns Title (with "Untitled"), State, Chunks, Actions (a View button) | the same table, minus Actions | changed: the title is the link |
| Button "View All", to `/documents?collection=` | a link to Documents filtered by this collection | moved |
| Empty state "No documents yet", "Ingest documents into this collection" | the empty table, next to the Ingest action | moved |
| Plugin sections (`CollectionDetailContributor`) | none | dropped: see Plugin extension points |

### Collection form

`pages/collection_form.templ`, shared by create and edit.

| templ | React | status |
|---|---|---|
| Title "Create Collection" or "Edit Collection" | `/collections/new`, `/collections/:id/edit` | moved |
| Buttons Cancel, in the header and again in the footer | Cancel | moved |
| Field Name, required, placeholder "My Collection" | Name | moved |
| Field Description, placeholder "Optional description" | Description | moved |
| Field Embedding Model, text, defaulting to the engine's `DefaultEmbeddingModel` | the "Recorded, not used" box, showing what this deployment runs (`system.components`) | changed: Weave records the model and never uses it, so it isn't a field you set |
| Field Chunk Strategy, a select of recursive, semantic, sliding_window, fixed and code | the same box | changed: same reason |
| Field Chunk Size, a number, defaulting to `DefaultChunkSize` | Chunk size on create, read-only on edit | moved |
| Field Chunk Overlap, a number, defaulting to `DefaultChunkOverlap` | Chunk overlap on create, read-only on edit | moved |
| Edit form letting you change all six fields | edit changes name, description and metadata only | changed: the chunk settings are fixed at creation. Weave can't re-chunk existing documents, so a change would only apply to new ones. The page says so |
| Submit "Create Collection" or "Update Collection" | Create, Save | moved |
| Create posting form-encoded to `/weave/collections`, edit sending a `PUT` | `collections.create` and `collections.update` | changed: the templ routes never existed |
| No overlap validation | overlap at or above size is refused as a bad request | changed |
| A duplicate name reaching the store | a conflict error on create and on rename | changed |
| No field for embedding dimensions | none | dropped: recorded and never used |

### Documents

`pages/documents.templ`.

| templ | React | status |
|---|---|---|
| Title "Documents", with a count badge | `/documents`, the `total` from `documents.list` | moved |
| The badge counting the unsearched total while you searched | `total` agrees with the list, because document counts take `Search` | changed |
| Description "Browse and manage ingested documents" | none | dropped: it restated the title |
| Search "Search documents...", on title, 300 ms debounce | title search | moved |
| State select: All States, Pending, Processing, Ready, Failed | state filter | moved |
| Collection select, with All Collections, hidden when no collection exists | collection filter | moved |
| State tab badges: All, Pending, Processing, Ready, Failed, the active one filled | none | changed: a second control for the filter the select already holds. The design lists one state filter |
| Filter and tab links joining search, state and collection into a URL with no escaping | the shell's router | changed |
| Empty state "No documents found", "Ingest documents to see them here" | the empty `ResourceTable` | moved |
| Column Title, a link, "Untitled" in italics when empty | Title, a link to `/documents/:id` | moved |
| Column Source, cut to 40 bytes, "-" when empty | none in the table | dropped: Source is on the detail page |
| Column Collection, a link showing the name or the ID | Collection, a link | moved |
| Column State, a `StateBadge` | Document state badge | changed: see Badges |
| Column Chunks | Chunks | moved |
| Column Actions: a View button | none | dropped: the title is the link |
| Row Delete, opening a `ConfirmDialog`, "All chunks will be permanently removed." | `documents.delete` in a `ConfirmDialog` | moved: where the button sits (row or detail page) gets settled when the pages are walked |
| Pagination: Previous and Next, 20 a page | offset paging | changed |

### Document detail

`pages/document_detail.templ`.

| templ | React | status |
|---|---|---|
| Title: the document title, or "Untitled Document" | the title | moved |
| Button Back | the Documents nav item | changed |
| Button "View Chunks", to the chunk list | the chunk reader on this page | changed: the chunks are on the page now |
| Button Delete, with a `ConfirmDialog` ("All associated chunks will be permanently removed.") | `documents.delete`, `ConfirmDialog` | moved |
| Stat State, the raw lowercase string | the document state badge | changed: a badge, with the stalled marker where it applies |
| Stat Chunks, "Segments" | chunk count with the details | changed |
| Stat "Content Length", labelled "Characters" | Content length in the details, in bytes | changed: it was always bytes |
| Stat "Source Type", "unknown" when empty | Source type in the details | moved |
| Card Error, a `pre` shown only for a failed document that has an error | an alert with the stored error | moved |
| Details: Document ID | ID, mono | moved |
| Details: Collection, the name as plain text | Collection, a link | changed |
| Details: Source, "-" when empty | Source | moved |
| Details: Source Type | Source type | moved |
| Details: Content Hash, the first 16 characters and "..." | the full content hash with a copy button | changed |
| Details: Content Length, "N chars" | Content length, in bytes | changed: see above |
| Card Metadata, in map order, "No metadata" when empty | metadata sorted by key | changed |
| Card "Chunks Preview", "First chunks from this document", the first 5 | the span map and the chunk reader, showing every chunk | changed: a preview of 5 became the whole document |
| Preview columns #, Content (cut to 100 bytes), Tokens, Actions (View) | the chunk reader, every chunk's full text in order, with overlap highlighted, in a lazily loaded virtualised list | changed |
| Button "View All N", shown past 5 chunks | none | dropped: nothing is hidden behind it now |
| Empty state "No chunks", "This document has not been chunked yet" | the span map's empty case | moved |
| Plugin sections (`DocumentDetailContributor`) | none | dropped: see Plugin extension points |

### Chunks

`pages/chunks.templ`.

| templ | React | status |
|---|---|---|
| The page needing `?doc=`, and without it an empty state "Select a document" | `/chunks` opens on a collection picker, then pages that collection's chunks (`chunks.list`) | changed: it dead-ended unless you came from a document |
| Title "Chunks" or "Chunks: <document title>" | Chunks | changed |
| Count badge, `len(chunks)` | the paged `total` | changed |
| Subtitle "N chunks in this document", or "Browse document chunks" | none | dropped |
| Button "Back to Document" | none on the page | dropped: the chunk reader lives on the document page now |
| Column "#" | chunk index | moved |
| Column Content, cut to 120 bytes | content, clamped | moved |
| Column Tokens | token estimate | moved |
| Column Offsets, "start-end" | offsets, as byte offsets | moved |
| Column Actions, a View button | none | dropped: the row is the link |
| Empty state "No chunks found", "This document has no chunks" | the empty `ResourceTable` | moved |
| No paging, every chunk of the document in one response | offset paging | changed |
| Listing by document only | `chunks.list` by document or by collection | changed |
| The `/chunks/browser` alias | none | dropped: a second path to the same page |

### Chunk detail

`pages/chunk_detail.templ`.

| templ | React | status |
|---|---|---|
| Title "Chunk #N" | `/chunks/:id`, `chunks.get` | moved |
| Button "Back to Chunks" | the Chunks nav item | changed |
| Button "View Document" | a link to `/documents/:id` | moved |
| Stat Index, "Position in document" | index, in the title and details | moved |
| Stat Tokens, "Token count" | token estimate | moved: it's `len/4` in every chunker, and the page says estimate |
| Stat Offsets, "Character range" | offsets, as byte offsets | changed: they were never characters |
| Stat Parent, the parent ID or "none" | none | dropped: nothing ever sets it |
| Details: Chunk ID, Document ID, Collection ID | IDs, mono | moved |
| Details: Start Offset, End Offset, Token Count | offsets and token estimate | moved |
| Card Content, "Full chunk content", a scrolling `pre` | full text | moved |
| Card Metadata, only when non-empty, in map order | metadata sorted by key | changed |

### Retrieval

`pages/retrieval.templ`.

| templ | React | status |
|---|---|---|
| Title "Retrieval Playground", "Test semantic search across your collections" | `/retrieval`, the "Ask Weave" card | changed |
| Field Query, a textarea, placeholder "Enter your search query..." | query | moved |
| Field Collection, All Collections and one entry per collection | collection filter | moved |
| Field Strategy: Similarity, MMR, Hybrid | none | dropped: the engine ignores it. The page names the configured retriever from `system.components` |
| Field "Top K", default 10 | Top K | moved |
| Field "Min Score", step 0.01, 0 to 1, default 0 | Min score | moved |
| Button Search | "Run query" | moved |
| A `GET` form that put the query into the URL | `retrieval.run`, a command | changed: every run calls the embedder, usually a paid API call, and a command fires only when you press Run. The last result stays on screen while the next loads |
| Heading "Results", with a count badge | a summary line: the retriever kind, hits and elapsed time, and how many the vector search scanned | changed |
| Empty state "No results", "Try a different query or adjust the parameters" | three different empties: nothing run yet, the vector search found nothing, or the retriever dropped everything the vector search found | changed |
| A card per hit | a `ResourceTable` row per hit, with an inspector for the selected one (a sheet on narrow screens) | changed |
| Hit rank, "#N" | Rank | moved |
| Hit score, a `ScoreBadge` | the final score under a header named for its kind (Cosine, RRF score, Rerank score), with `tabular-nums` and three decimals | changed: no colours, see Deliberately dropped |
| Hit collection badge, never rendered | the source document and chunk index in the row, and links in the inspector | changed |
| Hit button "View Chunk", never rendered | a link to `/chunks/:id` in the inspector | moved |
| Hit score bar | none | dropped: see Deliberately dropped |
| Hit content, cut to 500 bytes in a scrolling `pre` | the chunk text clamped to two lines in the row, in full in the inspector | changed |
| Hit "Tokens" and "Index", always 0 | tokens and chunk index in the inspector | changed: real now |
| Hit document link, never rendered | a link to `/documents/:id` | moved |
| An invalid collection ID silently becoming "all collections" | a bad-request error | changed |
| Errors swallowed | a `CommandAlert` carrying the server's message | changed |
| Plugin result sections (`RetrievalResultContributor`, never called) | none | dropped: see Plugin extension points |

### Pipeline

`pages/pipeline.templ`.

| templ | React | status |
|---|---|---|
| Title "Pipeline", "RAG pipeline component status and configuration" | `/pipeline`, `system.components` | moved |
| Heading "Pipeline Components" | the component report | moved |
| Stage card Loader, "Extracts text from documents" | Loader: its kind, parameters and supported content types | changed: from the engine, not hard-coded |
| Stage card Chunker, "Splits text into segments" | Chunker: kind and parameters | changed |
| Stage card Embedder, "Generates vector embeddings" | Embedder: kind, parameters and dimensions | changed |
| Stage card "Vector Store", "Stores and indexes vectors" | Vector store: kind, parameters and its tenant-filter note | changed |
| Stage card Retriever, "Finds relevant chunks" | Retriever: kind, parameters (MMR lambda, hybrid k, whether a reranker is present) and what its score means | changed |
| Badge Active or "Not Configured" | `configured`, as the engine reports it | changed: it said Active whatever was wired |
| The check or cross icon in each stage | none | dropped: it followed the Active flag |
| Arrows between the five stages | none | dropped: decoration, and the design doesn't carry them |
| Card "Engine Configuration": Default Chunk Size and Overlap (in "tokens"), Embedding Model, Chunk Strategy, Top-K, Shutdown Timeout, Ingest Concurrency | the engine config as the engine holds it | moved: the extension passes its config to the engine now, so it's finally your YAML |

### Loaders

`pages/loaders.templ`. Folded into Pipeline.

| templ | React | status |
|---|---|---|
| Title "Loaders", with a count badge (7), "Supported document loaders and formats" | Pipeline's loader section | changed: one page for the pipeline |
| Card Text, "Plain text documents": `text/plain`, `.txt` | content types from `Supports`: `text/plain`, and the empty type | changed: `.txt` is not a type |
| Card Markdown: `text/markdown`, `.md` | `text/markdown`, `text/x-markdown` | changed |
| Card HTML: `text/html`, `.html`, `.htm` | `text/html`, `application/xhtml+xml` | changed |
| Card CSV: `text/csv`, `.csv` | `text/csv` | changed |
| Card JSON: `application/json`, `.json` | `application/json` | changed |
| Card URL, "Web page content from URLs": `url`, `http`, `https` | `text/uri-list` | changed: nothing accepted the three the card listed |
| Card Directory, "Batch load from filesystem directory": `directory`, `dir` | none | changed: `DirectoryLoader.Supports` returns false for everything, because it hands each file to another loader by extension |
| The card descriptions ("Plain text documents" and the rest) | none | dropped: static copy |

### Extensions

`pages/extensions.templ`. Folded into Pipeline.

| templ | React | status |
|---|---|---|
| Title "Extensions", with a count badge, "Registered lifecycle extensions" | Pipeline's extensions section | changed |
| Column "#" | none | dropped: a row number |
| Column "Extension Name" | the extension's name | moved |
| Column Status, a "Registered" badge on every row | the hooks each extension implements (`DescribeExtensions`) | changed: every listed extension is registered, so the badge carried nothing |
| Empty state "No extensions registered", "Extensions will appear here when registered with the engine" | an empty extensions list | moved |

### Settings

`settings/config_panel.templ`, the `RenderSettings` method in `contributor.go`,
and the settings panel `weave-config` declared in `manifest.go` and in
`forge.contributor.yaml`. The panel is gone and its content is on Pipeline.

| templ | React | status |
|---|---|---|
| Panel `weave-config`, group Weave, icon layers, titled "Engine Settings" in `manifest.go` and "Weave Settings" in the YAML, with two different descriptions | none | dropped: the panel was a read-only copy of Pipeline's configuration |
| Card "Engine Configuration", the same seven fields as Pipeline | Pipeline's engine config | moved |
| Card "Registered Extensions", "Lifecycle hooks registered with the engine", names as badges | Pipeline's extensions section | changed: with the hooks each implements |
| Empty text "No extensions registered" | an empty extensions list | moved |
| Plugin settings panels (`DashboardSettingsPanel`) shown below | none | dropped: see Plugin extension points |

### Widgets

`widgets/stats.templ`, `widgets/recent_ingestions.templ`,
`widgets/pipeline_health.templ`, and their descriptors in `manifest.go` and
`forge.contributor.yaml`.

| templ | React | status |
|---|---|---|
| Widget `weave-stats`, "RAG Stats", "Collection, document, and chunk counts", size md, every 60 seconds, group Weave | none | dropped: the Overview covers it, and plugins have no widget slot |
| Its six cards: Collections, Documents, Ready, Failed, Pending, Chunks (it left out Processing) | the Overview's `StatGrid` | dropped: with the widget. The Overview shows all four states |
| Widget `weave-recent-ingestions`, "Recent Ingestions", "Recently ingested documents", size lg, every 15 seconds | none | dropped: with the widget. The Overview's newest-documents table replaces it |
| Its table: Title (cut to 30), Collection (cut to 20), State, Chunks, with no links, the 10 oldest documents | none | dropped: with the widget |
| Its empty state "No recent ingestions" | none | dropped |
| Widget `weave-pipeline-health`, "Pipeline Health", size md, every 60 seconds, described as "RAG pipeline component status" in `manifest.go` and "Pipeline component status and document state breakdown" in the YAML | the Overview's components strip, linking to Pipeline | changed |
| Its five component rows, Active or "N/A" | the strip and the Pipeline page | changed: real `configured` state |
| Its four document-state counts: Ready, Processing, Pending, Failed | the Overview's `StatGrid` | moved |
| Plugin widgets from `DashboardWidgets`, merged into the manifest | none | dropped: see Plugin extension points |

### Navigation and manifest

`manifest.go`, and `forge.contributor.yaml` (which duplicates it, with different
priorities).

| templ | React | status |
|---|---|---|
| Name `weave`, display name "Weave", icon layers, version 0.1.0 | `extension: "weave"`, `namespace: "weave"`, `label: "Weave"` | changed |
| Layout "extension", sidebar shown | none | dropped: the shell decides layout and sidebar |
| YAML `type: templ` and `build: mode: local` | none | dropped: templ is gone |
| Nav Overview, icon layout-dashboard, group Weave, priority 0 | Overview, group RAG, first | moved |
| Nav Retrieval, icon search, group Weave, priority 1 | Retrieval, group RAG, second | moved |
| Nav Pipeline, icon workflow, group Weave, priority 2 | Pipeline, group RAG, last | moved |
| Nav Collections, icon folders, group Content, priority 3 (0 in the YAML) | Collections, group RAG, third | moved |
| Nav Documents, icon file-text, group Content, priority 4 (1 in the YAML) | Documents, group RAG, fourth | moved |
| Nav Chunks, icon puzzle, group Content, priority 5 (2 in the YAML) | Chunks, group RAG, fifth | moved |
| Nav Loaders, icon upload, group Reference, priority 6 (0 in the YAML) | none | changed: it's a section of Pipeline |
| Nav Extensions, icon plug, group Reference, priority 7 (1 in the YAML) | none | changed: it's a section of Pipeline |
| Groups Weave, Content and Reference | one group, RAG | changed |
| Nav icons | an icon per item | changed: the plugin chooses its own |
| Topbar title "Weave", logo icon layers, accent `#10b981` | none | dropped: the shell themes every plugin the same way |
| Topbar search (`ShowSearch`), and the `searchable` capability | none | dropped: declared, never implemented |
| Topbar action "API Docs", icon file-text, to `/docs`, ghost variant | none | dropped: a docs link isn't a dashboard feature, and the design carries no topbar |
| Plugin nav items merged from `PageContributor` and `PluginPage` | none | dropped: see Plugin extension points |
| Widget and settings descriptors | see Widgets and Settings | dropped |

### Plugin extension points

`plugin_iface.go`. Nothing in Weave or its plugins implements any of these, and
every one of them returns a `templ.Component`.

| templ | React | status |
|---|---|---|
| `Plugin`: `DashboardWidgets`, `DashboardSettingsPanel`, `DashboardPages`, with `PluginWidget` and `PluginPage` | none | dropped: nothing implements it, and plugins have no widget slot |
| `CollectionDetailContributor` | none | dropped |
| `DocumentDetailContributor` | none | dropped |
| `RetrievalResultContributor`, which the contributor never called | none | dropped |
| `PageContributor`: `DashboardNavItems`, `DashboardRenderPage` | none | dropped |
| The contributor consulting plugin pages before its own routes, and merging plugin nav and widgets into the manifest | none | dropped |
| `PluginSections`, the helper that rendered a list of contributed sections | none | dropped |

### Badges

Document state, proportion first.

| templ `StateBadge` | React | status |
|---|---|---|
| ready, secondary | outline: the majority in any healthy collection | changed |
| pending, outline | secondary: transient | changed |
| processing, default | default: worth a second look | moved |
| failed, destructive | destructive: what you came to find | moved |
| any other state, an outline badge with the raw string | none | dropped: the four states are all there are |

A `processing` document whose `updated_at` is older than `stalled_after` (15
minutes by default) also carries a destructive marker with its real age. An
ingest sends a heartbeat after each step, so a document that far behind almost
certainly died with its process. The marker still states the age and doesn't
call the document dead: recovery fails it on its next sweep.

### Shared components and helpers

`components/*.templ`, `components/icons.go`, `contributor.go`, `data.go`,
`pages/types.go`, `shared/pagination.go` and `widgets/types.go`. Each page
component is covered on the page that used it.

| templ | React | status |
|---|---|---|
| `EmptyState`: an icon, a title, an optional description | the kit's `EmptyState`, and `ResourceTable`'s empty message | changed: no icon |
| `StatCard`: label, icon, value, subtitle | the kit's `StatGrid` | changed |
| `PageHeader`: title, count badge, description, an actions slot | the kit's page header | changed |
| `Pagination`: "Page N of M (T total)", Previous and Next | offset paging | changed |
| `ConfigDisplay`: a two-column grid of labels and mono values | the kit's `DescriptionList` | changed |
| `ScoreBadge`: default from 0.8, secondary from 0.5, else outline, two decimals | none | dropped: see Retrieval |
| `StateBadge` | the document state badge | changed: see Badges |
| `JSONViewer` | none | dropped: no page used it |
| `ConfirmDialog` and `DialogHelpers` (`tuiOpenDialog`, `tuiCloseDialog`, an errors `div`, htmx `hx-post` or `hx-delete`) | the kit's `ConfirmDialog`, which sets `pending`, shows errors inside it and can't close while the command runs | changed |
| `PathRewriter`, the `htmx:configRequest` listener that rewrote request paths | none | dropped: the shell's router |
| `PluginSections` | none | dropped: see Plugin extension points |
| `resolveIcon`, mapping 17 icon names with an info icon for anything else | none | dropped: the nav takes its icons directly |
| The six truncate helpers (`truncate`, `docTruncate`, `chunkTruncate`, `retrievalTruncate`, `widgetTruncate`, `docDetailTruncateContent`), all cutting bytes | truncation by width, with the full value on hover | changed |
| The five `resolveColName` variants and `buildCollectionNameMap`, a full collection listing per render | the collection name from the contract | changed |
| `parseIntParam` and `parseFloat64Param`, which turned a bad or negative value into the default | a negative or unparseable value is a bad request | changed |
| `NewPaginationMeta`, defaulting to 20 a page | `limit` and `offset`, default 25, maximum 100 | changed |
| `fetchEntityCounts`, which ignored every error | `system.overview` | changed |
| `fetchCollectionsPaginated` and `fetchDocumentsPaginated` | `collections.list` and `documents.list` | changed |
| `fetchRecentDocuments`, ascending | the 10 newest documents in `system.overview` | changed |
| `fetchPipelineStatus`, hard-coded true | `system.components` | changed |
| `Contributor.RenderPage`, `RenderWidget` and `RenderSettings`, with htmx swaps of `#content` and `hx-push-url` | contract queries and commands, and the shell's router | changed |
| `Contributor.Manifest` and `NewManifest` | the plugin definition and the contract manifest | changed |

## New in the React pages

Some things the templ dashboard never did at all. They aren't templ items, so
they have no fate above. They're here so you know what else the plugin carries,
and so the browser walk covers every one of them.

- Ingest, at `/collections/:id/ingest` (`documents.ingest`). You paste text, or
  pick a `.txt`, `.md`, `.html`, `.csv` or `.json` file the browser reads as
  text, and the server caps it at 1 MiB (and see the transport limit under
  "What you need to do"). Title, source and metadata. The result shows inline:
  ready with N chunks, failed with the stored error, or already ingested for a
  duplicate, which can be an earlier copy that failed or stalled.
- Reindex (`collections.reindex`). Its confirmation says what it does: it
  deletes every vector in the collection first, re-embeds `ready` documents
  only, runs synchronously, and leaves the collection partly indexed if it fails
  partway.
- Collection edit (`collections.update`) and a metadata field on both collection
  forms.
- A tenant filter on every list and on retrieval. The Collections table gains ID,
  tenant, chunk size and overlap and created-date columns, and the Documents
  table gains ID, size and updated. The collection detail page shows tenant, app
  and timestamps.
- The Overview's stalled count (documents in `processing` with no heartbeat for
  `stalled_after`, 15 minutes by default), and its components strip linking to
  Pipeline.
- Retrieval's comparison: the configured ranking next to the raw vector ranking,
  with each hit's raw cosine score, raw vector rank and how far it moved. The
  "Context sent to the model" tab shows the assembled text exactly as built, with
  an editable budget and Re-assemble (`retrieval.assemble`). The "Left out" tab
  shows strong vector matches the retriever didn't return. A budget line dims the
  hits that didn't fit, and an orphaned hit keeps its rank with a destructive "no
  chunk row" badge.
- The document span map and chunk reader, and previous and next links between
  chunks.
- On Pipeline, what each retriever's score means (cosine, cosine with MMR order,
  RRF sum, rerank score).

## Still open

These are known gaps. None of them is a regression from the templ pages.

- The pgvector paths are untested. No pgvector test runs, because the test image
  has no vector extension, so "verified" on pgvector rests on reading its code.
  No migration creates `weave_vectors` or the extension either.
- Fabriq's tie order and its tenant filter are unverified. Tenant filtering on
  retrieval is tested on the memory vector store only. The Pipeline page marks
  it unverified on Fabriq.
- The `pipeline` package leaves `tenant_id` out of the vector metadata for
  untenanted rows. The engine doesn't read that key for those rows, so nothing
  breaks, but anything that filters vector metadata on `tenant_id = ""` finds
  nothing.
- The strategy parameter is still ignored. `WithStrategy` sets it and nothing
  reads it.
- Reindex re-embeds and never re-chunks. It deletes every vector in the
  collection first, re-embeds the existing chunks of `ready` documents only
  with the one global embedder, runs synchronously, and a failure partway leaves
  the collection partly indexed with no failure event.
- Source text isn't stored. A document keeps `content_hash` and
  `content_length` only, so chunk offsets can't be checked against the original.
  They're byte offsets into the text after the loader ran and after
  `strings.TrimSpace`. The semantic and code chunkers approximate them, the
  recursive chunker falls back to 0 when it can't find the text, and the fixed
  and sliding-window chunkers can split a UTF-8 rune.
- An overlap of 0 can't be set. The engine reads 0 as "use the default".
- `token_count` is `len/4` in every chunker, and the assembler's budget counts
  the same way. Both are estimates.
- Embedding model, dimensions and chunk strategy are recorded and never used.
  The engine has one global embedder and one global chunker.
- Ingest is synchronous inside one request. `Start` is a no-op, and there is no
  sweeper, lease or heartbeat, so a process that dies mid-ingest leaves the
  document in `processing` forever. A closed tab or a proxy timeout doesn't any
  more: the dashboard's ingest and reindex run on past a cancelled request. A vector upsert failure marks the document
  failed but leaves its chunk rows. Weave's HTTP handler throws away the
  document ID that `Ingest` returns on failure.
- The scores mean different things per retriever. MMR reorders but returns the
  original relevance score, and it ignores `MinScore`. Hybrid returns a
  reciprocal rank fusion sum (k = 60), which isn't comparable to cosine. The
  reranker overwrites the score and keeps no vector score, and no concrete
  reranker ships. MMR with `TopK` of 0 returns nothing.
- Retrieval and Weave's HTTP API never call the assembler. Only the dashboard's
  retrieval page does. Your app may assemble its own way, so "Context sent to
  the model" is what Weave's default assembler would build, not necessarily what
  you sent.
- There are no component registries or getters for the loader, chunker,
  embedder, vector store or retriever. `Components()` works by type switch, and
  an unknown type is reported by its Go type name.
- Weave's own tests are thin. We added tests for the engine, the four
  stores, the retrievers' hit identity, comparison and the assembler, but loader
  behaviour, the chunkers' internals, the MMR, hybrid and reranker maths, and
  Fabriq stay untested.
- A freshly started Mongo container accepts TCP before `mongod` is ready, so a
  test run against a container a minute or two old can time out on server
  selection. Run it again a few seconds later.
- `collections.list` runs two count queries per row, one for documents and one
  for chunks, so a full page of 100 collections costs 200 extra queries. A
  grouped count would fix it. We left it for later.

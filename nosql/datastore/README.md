# datastore — Firestore in Datastore mode for TinyGo

`nosql/datastore` speaks the Datastore v1 JSON API, which is what Firestore
exposes when a database is created in Datastore mode.

```go
client, err := datastore.New("my-project")
defer client.Close()

key := datastore.NameKey("Task", "first")
_, err = client.Put(ctx, datastore.NewEntity(key).
    Set("title", datastore.String("write the driver")).
    Set("done", datastore.Bool(false)).
    Set("priority", datastore.Int(3)))

entity, err := client.Get(ctx, key)
title, _ := entity.Properties["title"].AsString()
```

## Why this exists

Google ships two Go clients for this API and neither builds under TinyGo, for
unrelated reasons.

`cloud.google.com/go/datastore` is gRPC-only with no REST fallback, and gRPC
fails at `google.golang.org/grpc/internal/credentials`: `cfg.Clone undefined
(type *tls.Config has no field or method Clone)`. That is TinyGo's `crypto/tls`
stub surfacing one layer up. 465 packages in the closure.

`google.golang.org/api/datastore/v1`, the generated REST client, **does** exist
and also fails — at TinyGo's empty `net/http.Transport`, inside
`cloud.google.com/go/compute/metadata`. 428 packages, 64 of them still gRPC. The
deprecated `datastore.New(*http.Client)` constructor does not help: the
generated package imports `google.golang.org/api/internal` unconditionally, so
the closure and the error are identical.

## Implementation selection

The HTTP stack follows the repository convention: `net/http` on host Go, this
repository's `https` package on TinyGo builds. `-tags force_tinygo_logic` runs
the native path under host Go so it is testable without TinyGo.

`google.Backend` names the HTTP stack and `google.SignerBackend()` names the RSA
implementation.

## What maps from DynamoDB, and what does not

"Equivalent to the DynamoDB client" is a statement about effort and shape, not
about API parity.

**Same shape**: POST plus JSON, one operation per request; a one-member union
value type, hand-encoded; int64 as text; an opaque cursor the caller feeds back;
one host and many small requests; a typed error with sentinels; `Close` releasing
pooled TLS handles.

**Different**:

| | DynamoDB | Datastore |
| --- | --- | --- |
| Auth | SigV4 per request | bearer token, minted once an hour |
| Identity | table + partition/sort key | kind + key path; ancestors are part of identity |
| Tenancy | none | a namespace on every key |
| Reads | `GetItem` single, `BatchGetItem` batch | `lookup` is batch by construction |
| Writes | separate operations | one commit carries a mutation list |
| Partial update | `UpdateItem` expression | none; update replaces the whole entity |
| Conditions | a condition expression | verb preconditions, `baseVersion`, or a transaction |
| Transactions | out of scope | **in** scope; the only conditional path |
| Integrity | `x-amz-crc32` | nothing |
| Tables | create/describe/list | none; kinds are implicit |
| Throttling | provisioned capacity | contention, reported as `ABORTED` |

A caller porting from `nosql/dynamodb` rewrites the write path and keeps the
read path.

## Values

`Value` is a proto3 `oneof`, so exactly one member is set. Zero is
`ErrEmptyValue` and two is `ErrAmbiguousValue`; the wire carries one member, so
there is no correct choice to make and picking one silently would encode
something the caller did not mean.

```go
datastore.String("s")            datastore.Blob([]byte{1, 2})
datastore.Int(42)                datastore.Time(t)
datastore.IntString("...")       datastore.KeyValue(k)
datastore.Float(1.5)             datastore.GeoPoint(35.6, 139.7)
datastore.Bool(true)             datastore.Nested(entity)
datastore.Null()                 datastore.Array(a, b)

datastore.Unindexed(v)           // composes with any of the above
```

Three distinctions the type keeps that a `map[string]any` could not:

- **Integer is text.** proto3 JSON encodes int64 as a string, which is what
  keeps a 64-bit id from passing through `float64`. `AsInt` converts; `AsNumber`
  returns the stored text.
- **Integer and double are different types.** `AsFloat` refuses an
  `integerValue` and `AsInt` refuses a `doubleValue`. Datastore stores them
  apart, and quietly widening one to the other is how a filter stops matching.
- **Null is not absent.** A property set to null and a property that is missing
  are different things to a query filter, and both are representable.

Timestamps go out as RFC 3339. Datastore stores microseconds, so a finer value
loses resolution on the server; the constructor does not hide that.

An embedded entity has no key. One that carries a key is rejected at encode time
rather than silently stripped.

## Struct mapping

`MarshalEntity` and `UnmarshalEntity` map a struct to an entity. They are the
only place this package uses reflection, and they live in their own file, so a
program that never calls them does not link them.

```go
type Task struct {
    Key      datastore.Key `datastore:"__key__"`
    Title    string        `datastore:"title"`
    Body     string        `datastore:"body,noindex"`
    Draft    string        `datastore:"draft,omitempty"`
    Internal string        `datastore:"-"`
}

e, err := datastore.MarshalEntity(t)
_, err = client.Put(ctx, e)
```

A field tagged `__key__` carries the entity's own key and must be a `Key` or
`*Key`; Datastore reserves that name for the key in queries, so no real property
can collide. Without such a field the entity comes back with no key and the
caller attaches one.

Supported: string, the integer and float kinds, bool, `[]byte`, `time.Time`,
`Key`, `Value`, slices, structs, and pointers to any of those. A nil pointer
becomes **null**, which is a value and not an absence — use `,omitempty` when
absent is what you meant. Decoding mirrors that: an explicit null zeroes the
field, an absent property leaves it alone.

**Maps are refused.** Datastore has no map type, so a map would become an
embedded entity whose property names come from runtime data rather than from
the struct — the one thing this mapping exists to avoid.

A `uint64` above `MaxInt64` is refused rather than wrapped: Datastore integers
are signed 64-bit and have no representation for it.

The `datastore` tag is **authoritative for this path only.** A code generator
over this driver reads its own tag, and a struct carrying both gets two field
mappings that look interchangeable and disagree on every renamed property. If
you generate a codec, treat a field carrying this tag but not yours as an error
rather than as agreement.

## Keys

```go
datastore.NameKey("Task", "first")            // string name
datastore.IDKey("Task", 5001)                 // numeric id
datastore.IncompleteKey("Task")               // the server allocates
parent.Child(datastore.PathElement{Kind: "Task", Name: "sub"})
key.WithNamespace("tenant-a")
```

A key carries what identifies the entity and nothing else. The project and
database are added by the client at encode time, so a `Key` stays portable
inside a program.

An incomplete key is legal only in an `Insert` or `AllocateIDs`. Only the last
path element may be incomplete: an ancestor without an identifier does not name
anything.

## Queries

```go
q := datastore.NewQuery("Task").
    Ancestor(parent).
    Filter("done", datastore.Equal, datastore.Bool(false)).
    Filter("priority", datastore.GreaterThanEqual, datastore.Int(3)).
    Order("created").
    Limit(50)

for {
    batch, err := client.Run(ctx, q)
    // ... use batch.Entities
    if !batch.HasMore() {
        break
    }
    q = q.Start(batch.EndCursor)
}
```

Filters combine with `AND`; there is no `OR` on this wire. Every builder method
returns a new `Query`, so a partially built query can be shared without one
caller's additions reaching another.

There is no iterator hiding round trips. `EndCursor` feeds `Start` explicitly,
the same shape as `storage/s3` and `nosql/dynamodb`.

`Count` uses `runAggregationQuery`. It exists because counting by paging through
keys costs a read per entity, so leaving it out would push callers toward the
expensive thing.

## Conditional writes

There is no condition expression language here. What the wire offers is:

- `Insert` fails with `ErrAlreadyExists` if the key is taken — put-if-absent
- `Update` fails with `ErrNoSuchEntity` if it is absent — put-if-present
- `WithBaseVersion(v)` / `WithUpdateTime(t)` — optimistic concurrency, from a
  previous read
- everything else needs a transaction

```go
err := client.RunInTransaction(ctx, func(tx *datastore.Tx) error {
    current, err := tx.Get(ctx, key)
    if err != nil {
        return err
    }
    n, _ := current.Properties["count"].AsInt()
    if n >= limit {
        return errTooMany            // nothing is written
    }
    current.Properties["count"] = datastore.Int(n + 1)
    tx.Put(*current)
    return nil
})
```

The predicate runs in Go, between a read and a commit that share a snapshot.
That is why the transaction is required and not optional: a client-side check
against a value read *outside* one is a race with a confident-looking API.

Mutations are queued and sent with the commit, so a closure that returns an
error writes nothing.

**The closure can run more than once.** Datastore reports contention as
`ABORTED`, and the right response is to re-run the whole closure — the reads it
decided on are stale. So it must have no side effects outside the transaction.
That cannot be enforced, only stated.

## Limits and chunking

Exported, because a caller batching work has to chunk against them and a number
copied out of Google's documentation into every consumer drifts silently when
the service changes it.

| Constant | Value |
| --- | --- |
| `MaxLookupKeys` | 1000 keys per `lookup` — `GetMulti` checks this before sending |
| `MaxRequestBytes` | 10 MiB |
| `MaxTransactionBytes` | 10 MiB |
| `MaxEntityBytes` | 1 MiB − 4 |
| `MaxKeyBytes` | 6 KiB |
| `MaxIndexedStringBytes` | 1500; a longer string is stored but not indexed |
| `MaxNestingDepth` | 20 |

**There is no maximum-mutations-per-commit constant, and that is not an
oversight.** Google documents no count limit on a commit — the bound is bytes,
`MaxRequestBytes` and, inside a transaction, `MaxTransactionBytes`. The only
documented count of 500 is property transformations per entity, which this
package excludes. So chunk a batch write by size, not by count.

## Composite indexes

Single-property indexes are automatic. A composite index is required when a
query combines an equality filter with an inequality on a different property,
orders on a property it also filters on inequality, or otherwise needs more than
one property considered together. Without one the query fails at runtime with
`FAILED_PRECONDITION`, on code that compiled cleanly.

`Index` describes one, so a tool that can see the need at build time has
somewhere to put it:

```go
idx := datastore.Index{
    Kind: "Task",
    Properties: []datastore.IndexProperty{
        {Name: "done"},
        {Name: "priority", Direction: datastore.Descending},
    },
}
yaml, err := datastore.MarshalIndexYAML([]datastore.Index{idx})
// feed to: gcloud datastore indexes create index.yaml
```

The output is sorted, so a tool that regenerates it produces a stable diff.

This is a description, not a request. **Applying** an index is an admin-API
operation and stays out of scope; the shape of an index is a property of the
service rather than of any one tool, which is why the type lives here instead of
being reinvented by every generator.

There is deliberately no `RequiredIndex(*Query)`. The rule for when a composite
index is needed is subtle, and a derivation that is quietly wrong is worse than
none — it would name an index that does not fix the query.

## TTL

**TTL is not expressible on this wire.** It is a policy over an ordinary
timestamp property, configured out of band:

```sh
gcloud firestore fields ttls update expiresAt --collection-group=Task --enable-ttl
```

So an expiring entity needs nothing special from this package: write a
`datastore.Time(...)` property and point a policy at it. The property must be a
timestamp; leaving it absent or null disables expiry for that entity, which is
the per-entity opt-out. One property per kind may be a TTL property, and a
database may hold at most 500 TTL policies. Deletion happens within about 24
hours of expiry, so it is a retention mechanism and not a correctness one — a
read may still return an expired entity, and application code has to check.

Datastore mode additionally cannot use TTL with a concurrency mode of Optimistic
With Entity Groups.

## Errors

Match on the sentinel, never on the HTTP code:

```go
if errors.Is(err, datastore.ErrAlreadyExists) { ... }
```

`ABORTED` and `ALREADY_EXISTS` are **both HTTP 409** and mean opposite things,
one retryable and one terminal. Classification keys on the canonical status
string in the error body; a reply with no body falls back to the code, and 409
falls back to `ALREADY_EXISTS` because guessing wrong in the retryable direction
would retry a duplicate insert forever.

## Retries and contention

| Status | Behaviour |
| --- | --- |
| `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `RESOURCE_EXHAUSTED` | retried, 3 attempts, 25 ms base, 1 s cap, full jitter |
| `INTERNAL` | retried **exactly once**, per Google's documented guidance |
| `UNAUTHENTICATED` | token refreshed once and resent; not charged to the retry budget |
| `ABORTED` inside a transaction | the closure re-runs |
| `ABORTED` outside one | terminal; there is nothing to re-run |
| everything else | terminal |

There is no `x-amz-crc32` equivalent, so unlike `nosql/dynamodb` there is no
response-integrity layer. TLS is the only guarantee on this path. That is a real
difference, not an oversight.

Beneath this client, the native transport replays a request once when a pooled
connection turns out to have been closed by the peer, so the honest worst case is
**attempts × 2 deliveries**. The mutation verbs are idempotent by construction —
`Insert` repeats as `ALREADY_EXISTS`, deleting an absent key succeeds, and
`Update` replaces rather than accumulates — so that is harmless for them. There
is no server-side arithmetic on this wire to be doubled, which is why the
`UpdateItem`-with-`ADD` hazard from `nosql/dynamodb` has no counterpart here. A
replayed transactional commit fails rather than double-writing, because the
handle is consumed.

`WithRetry(0, 0)` disables retrying. A cancelled context stops it immediately.

## Connections

Every request goes to one host, so the per-host cap is the whole pool. The
default is 4 idle connections; `WithMaxIdleConns(n)` should be set to the
concurrency the application runs.

`Close` is required rather than cosmetic: pooled native TLS handles outlive the
last request otherwise. A client built with `WithHTTPClient` is left alone by
`Close`, since its owner may still be using it.

The measured numbers live in the [`https` README](../../https/README.md), which
owns the transport.

## Configuration

| Option | Default |
| --- | --- |
| `WithEndpoint` | `DATASTORE_EMULATOR_HOST`, else `https://datastore.googleapis.com` |
| `WithDatabase` | the project's default database |
| `WithNamespace` | empty |
| `WithCredentials` / `WithTokenSource` | `GOOGLE_APPLICATION_CREDENTIALS` |
| `WithTimeout` | 10 s |
| `WithMaxIdleConns` | 4 |
| `WithRetry` | 3 attempts, 25 ms base |

A value with no scheme is taken as `http`, which is what `DATASTORE_EMULATOR_HOST`
carries. When the emulator variable is set and no endpoint is given, the client
sends no `Authorization` header at all: the emulator ignores it, and minting a
token it will not read would be pretending to test something.

## Not in scope

GQL, `reserveIds`, `SUM` and `AVG` aggregations, the admin API (index
management, import, export), auto-pagination, and Firestore native mode's
listeners, which Datastore mode does not have.

Property transformations — server-side increment and array-append — are
excluded deliberately: they exist on the wire only inside `commit`, and they
would reintroduce exactly the non-idempotent-retry hazard the rest of this
design avoids.

## Testing

```sh
go test ./nosql/datastore/
go test -tags force_tinygo_logic ./nosql/datastore/
```

Both commands run the same tests against a stub server that records what it was
sent, so request shapes, retry counts, and transaction sequencing are pinned
offline.

For a real server:

```sh
gcloud beta emulators datastore start --host-port=127.0.0.1:8081
DATASTORE_EMULATOR_HOST=127.0.0.1:8081 DATASTORE_PROJECT_ID=demo \
    tinygo run ./examples/datastoredemo
```

The emulator covers the codec, queries, transactions, and error paths. It does
**not** cover authentication, because it ignores `Authorization` entirely — a
sharper gap than DynamoDB Local's, where the signature is at least required to
be present and well formed. The token path needs one manual run against a real
project.

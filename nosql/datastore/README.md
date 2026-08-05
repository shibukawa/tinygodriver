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

`Int` accepts every Go integer type that fits `int64` on every platform.
`uint` is **not** among them: on a 64-bit platform it holds values `int64` does
not, and `Int` has no error to return, so admitting it meant
`Int(uint(math.MaxUint64))` storing `-1` silently. A `uint` caller writes
`Int(int64(n))` when the value is known to fit, or
`IntString(strconv.FormatUint(n, 10))` when it is not — and the latter fails at
encode time if it really is too wide.

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

That applies wherever a key appears, not just to an entity's own key: a
`KeyValue` stored as a property, one inside an `Array` — which is what
`ref IN (...)` needs — and one inside a nested entity all get the partition
attached on the way out. Marshalling a `Key` or a `Value` yourself produces one
*without* a partition, by design, because nothing below the client knows which
project it is for.

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

Every builder method returns a new `Query`, so a partially built query can be
shared without one caller's additions reaching another.

### AND and OR

Repeated `Filter` calls combine with `AND`. For a disjunction, build a condition
tree and attach it with `Where`:

```go
q := datastore.NewQuery("Task").
    Filter("owner", datastore.Equal, datastore.String("me")).
    Where(datastore.Or(
        datastore.Prop("state", datastore.Equal, datastore.String("new")),
        datastore.And(
            datastore.Prop("starred", datastore.Equal, datastore.Bool(true)),
            datastore.Prop("done", datastore.Equal, datastore.Bool(false)),
        ),
    ))
```

`Prop`, `And` and `Or` nest freely. `AncestorOf(key)` is a condition too, so an
ancestor restriction can sit inside a disjunction.

### Narrowing what comes back

```go
q = q.Project("title", "priority")   // only these properties, read from an index
q = q.KeysOnly()                     // keys alone, the cheapest read there is
q = q.DistinctOn("owner")            // collapse results sharing these properties
q = q.Offset(20)                     // skip, but see below
```

Every projected property must be indexed, because a projection query reads from
the index rather than from the entity.

`Offset` skips results the server has already read and billed you for.
`Batch.SkippedResults` reports how many, which is the number to look at when
deciding to move to a cursor instead — `Start` resumes without paying for what
came before.

Projection and distinct constraints are the service's and they are subtle, so
they are passed through unvalidated: a client-side check that was wrong would
refuse a query that works.

**Repeated `Where` calls, and a `Where` alongside a `Filter`, combine with
`AND`** — so an `Or` belongs inside one call, not spread across two.

A query is limited to `MaxDisjunctions` (30) once its filter is put in
disjunctive normal form, so nesting `Or` inside `And` multiplies rather than
adds. That bound is enforced by the service, not here: the expansion rule is
the service's, and a client-side count that disagreed would refuse a query that
works.

There is no iterator hiding round trips. `EndCursor` feeds `Start` explicitly,
the same shape as `storage/s3` and `nosql/dynamodb`.

### Aggregations

```go
n, err := client.Count(ctx, q)                    // int64
total, err := client.Sum(ctx, q, "celsius")       // Value: integer or double
mean, err := client.Avg(ctx, q, "celsius")        // Value: double, or null
```

All three use `runAggregationQuery`, and all three exist for the same reason:
the paging alternative costs a read per entity. Counting by paging can at least
be done keys-only; **summing by paging cannot**, because every entity has to
come back in full for the caller to add one property up. So paging to sum is
strictly more expensive than paging to count.

`Sum` and `Avg` return a `Value` rather than a Go number. A sum is an integer
when every summed value was an integer and a double otherwise, and an average
over nothing is null — where zero would be a different claim. Flattening either
would erase the integer-versus-double distinction the rest of this package
keeps.

They are available inside a transaction too, via `Tx.Count`, `Tx.Sum` and
`Tx.Avg`.

`Sum` and `Avg` shipped on 2026-08-04, and the "not in scope" list below still
excluded them for a day afterwards. A consumer read that line and wrote the
paging loop including them was meant to prevent. A test now fails if that list
names anything this package exports, which is why the list is only a list.

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

`RunReadOnly` is the same shape without the writes: several reads over one
consistent snapshot, taking no write locks. A closure that queues a mutation
inside one is refused rather than silently dropped.

**A transaction costs one fewer round trip than it looks.** There is no separate
`beginTransaction`: the first read asks to start one and its reply carries the
handle, and a closure that never reads folds its transaction into the commit.

| closure | round trips |
| --- | --- |
| one read, then commit | 2 |
| N reads, then commit | N+1 |
| writes only, no read | 1 |
| neither | 0 |

The last row is not a trick: no handle was ever taken, so there is nothing to
release. None of this restricts what a closure may do — a second read simply
uses the handle the first brought back.

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

`Client.MutationSize` gives that size without a throwaway marshal, and
`Client.CommitOverheadBytes` gives the request built around the mutations:

```go
batch, used := []datastore.Mutation{}, 0
for _, m := range mutations {
    n, err := client.MutationSize(m)
    if err != nil {
        return err
    }
    if used+n+client.CommitOverheadBytes(len(batch)+1) > datastore.MaxRequestBytes {
        client.Mutate(ctx, batch)
        batch, used = batch[:0], 0
    }
    batch, used = append(batch, m), used+n
}
```

The two account for the whole body exactly:

```text
CommitOverheadBytes(len(ms)) + Σ MutationSize(m) == bytes sent to :commit
```

`MutationSize` is a method on `Client` rather than on `Entity` because it
includes the key with its project, database and namespace attached, and only the
client knows those. An `Entity`-level figure would understate every mutation by
exactly the part the caller cannot see.

`CommitOverheadBytes` is the rest of the request: the mode, the array holding
the mutations, the comma between each pair, and the `databaseId` when the client
has one. Summing `MutationSize` alone undercounts by 42 + n bytes for n
mutations, plus the length of `"databaseId":"<name>",` when a database is named.
It takes a count rather than the mutations so chunking
stays a running total — the question at each step is whether one more fits, and
a whole-batch measure would re-encode everything to answer it.

A named database is counted twice, and both are real: every key carries the
partition, so it is inside each mutation, and the request carries it once more
at the top level.

Inside a transaction, use `Tx.CommitOverheadBytes` and chunk against
`MaxTransactionBytes`. That commit also carries either the handle its first read
returned or a `singleUseTransaction` block, which are different sizes, and only
the transaction knows which it is in.

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

`WithReadTime` reads as of a past instant. Within the past hour any
microsecond-granularity instant is legal; from one hour to seven days back only
whole-minute timestamps are, and only with point-in-time recovery enabled.
Truncate to a whole minute yourself for the older window — the client does not,
because that would change the instant you asked for, and the service refuses an
untruncated one as "read_time is too old", naming the age when the precision was
the problem.

A value with no scheme is taken as `http`, which is what `DATASTORE_EMULATOR_HOST`
carries. When the emulator variable is set and no endpoint is given, the client
sends no `Authorization` header at all: the emulator ignores it, and minting a
token it will not read would be pretending to test something.

## Not in scope

GQL, `reserveIds`, the admin API (index management, import, export),
auto-pagination, and Firestore native mode's listeners, which Datastore mode
does not have.

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

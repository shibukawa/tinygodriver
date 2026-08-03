# dynamodb — DynamoDB client for TinyGo

`aws-sdk-go-v2` does not build with TinyGo: its transport layer imports
`net/http/httputil`, and it reaches for the full `net/http.Transport` API, which
TinyGo declares as an empty struct. The dependency closure of the DynamoDB
service alone is 244 packages. This package speaks the JSON protocol directly
instead, over the SigV4 signer in [`cloud/aws`](../../cloud/aws).

```go
import "github.com/shibukawa/tinygodriver/nosql/dynamodb"

client, err := dynamodb.New(dynamodb.WithRegion("ap-northeast-1"))
defer client.Close()

item, err := client.GetItem(ctx, "users", dynamodb.Key{"pk": dynamodb.S("u#1")})
name, _ := item["name"].AsString()
```

## Implementation selection

Request building, signing, and JSON decoding are shared code. The builds differ
only in how a request reaches the network:

| Build | HTTP stack (`aws.Backend`) |
| --- | --- |
| Standard Go | `net/http` with `crypto/tls` |
| TinyGo, or `-tags force_tinygo_logic` | [`https`](../../https), TLS through the host OS |

## API

| Method | DynamoDB operation |
| --- | --- |
| `GetItem`, `PutItem`, `UpdateItem`, `DeleteItem` | the item API, with condition expressions |
| `Query`, `Scan` | one page per call |
| `BatchGetItem`, `BatchWriteItem` | up to 100 reads or 25 writes per request |
| `CreateTable`, `DeleteTable`, `DescribeTable`, `ListTables` | table administration |

Transactions, PartiQL, Streams and DAX are out of scope.

## Attribute values

`AttributeValue` is one attribute in its wire form. The codec is written out
rather than derived by reflection, so the supported types are visible in the
type declaration:

```go
item := dynamodb.Item{
	"pk":      dynamodb.S("u#1"),
	"age":     dynamodb.N(42),
	"active":  dynamodb.Bool(true),
	"tags":    dynamodb.SS("a", "b"),
	"profile": dynamodb.Map(map[string]dynamodb.AttributeValue{
		"city": dynamodb.S("東京"),
	}),
}
```

Numbers are held as text. DynamoDB numbers carry up to 38 significant digits,
which `float64` cannot represent, so the conversion happens where the caller
picks what to lose:

```go
n, ok := item["age"].AsInt()       // int64
f, ok := item["ratio"].AsFloat()   // float64, 15 digits
s, ok := item["big"].AsNumber()    // the stored text, lossless
```

Structs map through `dynamodbav` tags, the spelling `aws-sdk-go-v2` uses, so an
example written against the SDK ports over:

```go
type User struct {
	PK      string    `dynamodbav:"pk"`
	Age     int       `dynamodbav:"age"`
	Created time.Time `dynamodbav:"created"`
	Note    string    `dynamodbav:"note,omitempty"`
	Ignored string    `dynamodbav:"-"`
}

item, err := dynamodb.MarshalItem(user)
err = dynamodb.UnmarshalItem(item, &user)
```

Only the field names port over from the SDK's tag: options such as
`,stringset` and `,unixtime` are **not** read, so `[]string` becomes a list
(`L`), not a string set. Use `dynamodb.SS(...)` when you want a set.

`MarshalItem` and `UnmarshalItem` are the only reflection *this package* does,
and a program that does not call them does not link them — 24 KB and about
0.8 µs per item, measured under TinyGo 0.41.1.

Building items by hand is not, however, a reflection-free path. The request body
goes through `encoding/json` either way, and that is where the reflection and
the time actually are:

| | time | allocations |
| --- | --- | --- |
| `MarshalItem` (reflection) | 0.8 µs | 21 |
| `json.Marshal` of the item (either path) | 2.2 µs | 42 |
| by hand, end to end | 2.7 µs | 59 |
| through `MarshalItem`, end to end | 3.1 µs | 63 |

`encoding/json` and `reflect` are about 151 KB of a 1.45 MB TinyGo binary, and
they are linked whichever way you build items. Removing them would mean
replacing `encoding/json` for the whole request and response path, not just for
item mapping.

## Pagination

`Query` and `Scan` return one page. The loop stays in your code:

```go
var startKey dynamodb.Key
for {
	opts := []dynamodb.QueryOption{
		dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{
			":pk": dynamodb.S("u#1"),
		}),
	}
	if startKey != nil {
		opts = append(opts, dynamodb.WithExclusiveStartKey(startKey))
	}
	page, err := client.Query(ctx, "events", "pk = :pk", opts...)
	if err != nil {
		return err
	}
	use(page.Items)
	if !page.HasMore() {
		break
	}
	startKey = page.LastEvaluatedKey
}
```

An empty page with a continuation key is normal: a filter drops items after the
page has been read, and paid for.

The batch calls work the same way. What DynamoDB declined comes back in
`UnprocessedItems`, in a shape that can be sent again:

```go
result, err := client.BatchWriteItem(ctx, writes)
for err == nil && result.HasUnprocessed() {
	result, err = client.BatchWriteItem(ctx, result.UnprocessedItems)
}
```

## Errors

Failures are `*dynamodb.Error` with `Op`, `Table`, `StatusCode`, `Type`,
`Message` and `RequestID`, wrapping a sentinel:

```go
if errors.Is(err, dynamodb.ErrConditionalCheck) {
	// the condition refused the write, which is an answer, not a fault
}
```

`ErrItemNotFound`, `ErrResourceNotFound`, `ErrConditionalCheck`,
`ErrThroughputExceeded`, `ErrThrottled`, `ErrValidation`, `ErrTableInUse`,
`ErrTableNotFound`, `ErrRequestTooLarge`, `ErrTransactionConflict`,
`ErrBadCredentials`, `ErrChecksumMismatch`, `ErrServerFailure`.

A `GetItem` that matches nothing is `ErrItemNotFound` rather than an empty item:
DynamoDB answers a miss with a 200 and no `Item` member, which is too easy to
read as an item with no attributes.

Errors are matched on the exception name after the `#`, because both namespaces
appear on the wire: `com.amazonaws.dynamodb.v20120810#` for service errors and
`com.amazon.coral.service#` for authentication failures.

Every reply is checked against its `x-amz-crc32` header before it is decoded. A
mismatch is retried, then reported as `ErrChecksumMismatch`.

## Retries and idempotency

Throttling is a normal operating condition on a provisioned table, so retrying
is on by default: 3 attempts, exponential backoff with jitter, bounded by the
request context.

| Retried | Not retried |
| --- | --- |
| `ThrottlingException`, `ProvisionedThroughputExceededException` | `ValidationException` |
| `RequestLimitExceeded`, `TransactionConflictException` | `ConditionalCheckFailedException` |
| `InternalServerError`, `ServiceUnavailable`, 5xx | `ResourceNotFoundException` |
| a checksum mismatch, a connection failure | anything else the server named |

Retrying has a consequence worth stating plainly: **a write can be delivered
twice**. A request can reach DynamoDB and have its reply lost, and on TinyGo
builds the transport also replays a request once when a pooled connection turns
out to have been closed by the peer. The bound is `attempts × 2` deliveries.

`PutItem` and an `UpdateItem` that only `SET`s are idempotent, so this changes
nothing for them. An `UpdateItem` with `ADD` is not:

```go
// Not idempotent: a replay increments twice.
client.UpdateItem(ctx, "counters", key, "ADD hits :one", ...)

// Guarded: the second delivery fails its condition instead.
client.UpdateItem(ctx, "counters", key, "ADD hits :one",
	dynamodb.WithCondition("attribute_not_exists(seen_token)"), ...)
```

`WithRetry(1, 0)` disables client-level retrying. It does not make delivery
exactly-once — nothing over HTTP does — and on TinyGo builds the transport
replay remains until `Transport.DisableKeepAlives` is set.

## Limits

Exported so a caller batching work can chunk against them rather than copying
numbers out of AWS documentation into its own source, where they drift silently.

| Constant | Value |
| --- | --- |
| `MaxBatchGet` | 100 items per `BatchGetItem`, across tables |
| `MaxBatchWrite` | 25 put/delete requests per `BatchWriteItem`, across tables |
| `MaxItemBytes` | 400 KiB |
| `MaxRequestBytes` | 16 MiB |

## Connections

Connections are pooled, which matters more here than for object storage. A TLS
handshake to a regional endpoint measures around 90 ms while the round trip
itself is around 10 ms, so an unpooled client spends nine tenths of its time on
setup.

Every request goes to one host, so `WithMaxIdleConns` is effectively the whole
pool. The default is 4; set it to the number of operations you run at once,
since a call that finds no pooled connection pays the handshake.

`Close` releases the pooled connections, and with them the TLS handles held by
the host OS. See the [`https` connection reuse notes](../../https/README.md#connection-reuse)
for the idle timeout and the replay rule underneath all this.

## Configuration

```go
client, err := dynamodb.New(
	dynamodb.WithEndpoint("http://127.0.0.1:8000"),
	dynamodb.WithRegion("ap-northeast-1"),
	dynamodb.WithCredentials(aws.Credentials{AccessKeyID: id, SecretAccessKey: secret}),
	dynamodb.WithMaxIdleConns(8),
	dynamodb.WithTimeout(10*time.Second),
	dynamodb.WithRetry(3, 25*time.Millisecond),
)
```

Defaults come from the environment: `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`, `AWS_REGION` then
`AWS_DEFAULT_REGION`, and `AWS_ENDPOINT_URL_DYNAMODB` then `AWS_ENDPOINT_URL`.
There is no shared credentials file, no SSO, and no IMDS lookup.

## Testing

Unit tests run on both build paths and need nothing external:

```bash
go test ./nosql/dynamodb/ && go test -tags force_tinygo_logic ./nosql/dynamodb/
```

The integration tests need a server. DynamoDB Local is the one this suite
targets:

```bash
docker run -d -p 8000:8000 amazon/dynamodb-local \
	-jar DynamoDBLocal.jar -inMemory -sharedDb
```

`-sharedDb` is required: without it the server partitions data by access key and
region, so a table created with one credential is invisible to another.

```bash
DYNAMODB_TEST_ENDPOINT=http://127.0.0.1:8000 go test ./nosql/dynamodb/
```

The local server accepts any well-formed credentials without verifying the
signature, so it proves the request shapes and the decoding, not the signing.
Signing is covered by known-answer tests in `cloud/aws`, whose expected values
came from `aws-sdk-go-v2`'s own signer.

[`examples/dynamodbdemo`](../../examples/dynamodbdemo) runs the whole lifecycle
under either compiler.

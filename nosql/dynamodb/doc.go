// Package dynamodb is a DynamoDB client that builds with TinyGo.
//
// The maintained Go client cannot be used here: aws-sdk-go-v2 reaches for the
// full net/http.Transport API through smithy-go, which TinyGo declares as an
// empty struct, and its transport layer imports net/http/httputil, which does
// not compile under TinyGo at all. This package therefore speaks the DynamoDB
// JSON protocol directly, over the SigV4 signer in cloud/aws and whichever HTTP
// stack the build selects.
//
//	client, err := dynamodb.New(dynamodb.WithRegion("ap-northeast-1"))
//	defer client.Close()
//
//	item, err := client.GetItem(ctx, "users", dynamodb.Key{
//		"pk": dynamodb.S("u#1"),
//	})
//	name, _ := item["name"].AsString()
//
// # Attribute values
//
// AttributeValue carries one attribute in its wire form, built with S, N, B,
// Bool, Null, List, Map and the set constructors, and read back with the As
// accessors. The codec is hand-written rather than derived by reflection, so
// the supported type set is visible in the type declaration.
//
// Building items by hand is not a reflection-free path, and it is worth being
// precise about that: the request body still goes through encoding/json, which
// is reflection-based, so reflect is linked either way. Measured on tinygo
// 0.41.1, encoding/json and reflect are about 151 KB of a 1.45 MB binary
// whatever this package does.
//
// Numbers are held as text, because DynamoDB numbers carry up to 38 significant
// digits and float64 does not. AsInt and AsFloat convert where the caller has
// chosen what to lose; AsNumber returns the stored text.
//
// MarshalItem and UnmarshalItem map Go structs through dynamodbav tags, the
// same spelling aws-sdk-go-v2 uses. Only the field names port over: tag options
// beyond omitempty, such as the SDK's stringset and unixtime, are not read.
//
// They are the only reflection this package itself does, and a program that
// does not call them does not link them: 24 KB and around 0.8 us per item on
// tinygo 0.41.1. That is the whole saving from building items by hand, against
// the 2.2 us that json.Marshal costs either way.
//
// # Pagination and batches
//
// Query and Scan return one page. A truncated page carries LastEvaluatedKey,
// which feeds WithExclusiveStartKey, so the request loop stays in the caller's
// code rather than hidden inside a paginator.
//
// The batch calls return what they could not do, in UnprocessedKeys and
// UnprocessedItems, rather than retrying inside the call: a partial success is
// a result, and whether the rest still matters is the caller's decision.
// UnprocessedItems can be passed straight back to BatchWriteItem.
//
// # Retries and delivery
//
// Throttling is a normal operating condition on a provisioned table, not a
// fault, so retrying is on by default: three attempts with exponential backoff
// and jitter, bounded by the request context. Errors the server has already
// judged, such as a validation failure or a refused condition, are not retried.
//
// Retrying has a consequence worth stating plainly. A request can be delivered
// and its reply lost, so a retried write can be applied twice. On TinyGo builds
// the transport also replays a request once when a pooled connection turns out
// to have been closed by the peer, which multiplies the bound to attempts x 2.
// PutItem and an UpdateItem that only SETs are idempotent and unaffected; an
// UpdateItem with ADD is not, and should carry a condition expression. See
// WithRetry.
//
// # Connections
//
// Connections are pooled, which matters more here than for object storage: a
// TLS handshake is around 90 ms against a regional endpoint while the round
// trip itself is around 10 ms, so an unpooled client spends nine tenths of its
// time on setup. Every request goes to one host, so WithMaxIdleConns is
// effectively the whole pool: set it to the number of operations run at once.
//
// Close releases the pooled connections, and with them the TLS handles the host
// OS holds.
package dynamodb

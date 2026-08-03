---
id: api:datastore-client
type: api
title: Datastore Client
---
`Client` posts bearer-authenticated JSON to one Datastore endpoint and decodes the replies. Same construction and lifecycle as api:dynamodb-client; different verbs, because concept:dynamodb-datastore-mapping says the semantics do not follow the names.

```yaml
import_path: github.com/shibukawa/tinygodriver/nosql/datastore
constructor: func New(projectID string, opts ...Option) (*Client, error)
state: proposed 2026-08-02
wire:
  method: "POST /v1/projects/{projectId}:<rpc>, one RPC per request"
  content_type: application/json
  auth: "Authorization: Bearer <token>, from api:google-auth"
  accept_encoding: identity, as in api:dynamodb-client and for the same reason
methods:
  reads:
    - func (c *Client) Get(ctx, key Key, opts ...ReadOption) (*Entity, error)
    - func (c *Client) GetMulti(ctx, keys []Key, opts ...ReadOption) (*LookupResult, error)
  writes:
    - func (c *Client) Put(ctx, e Entity, opts ...WriteOption) (Key, error)
    - func (c *Client) Insert(ctx, e Entity, opts ...WriteOption) (Key, error)
    - func (c *Client) Update(ctx, e Entity, opts ...WriteOption) error
    - func (c *Client) Delete(ctx, key Key, opts ...WriteOption) error
    - func (c *Client) Mutate(ctx, ms []Mutation, opts ...WriteOption) (*CommitResult, error)
  queries:
    - func (c *Client) Run(ctx, q *Query, opts ...ReadOption) (*Batch, error)
    - func (c *Client) Count(ctx, q *Query, opts ...ReadOption) (int64, error)
  transactions:
    - func (c *Client) RunInTransaction(ctx, fn func(*Tx) error, opts ...TxOption) error
    - func (c *Client) RunReadOnly(ctx, fn func(*Tx) error) error
  keys:
    - func (c *Client) AllocateIDs(ctx, keys []Key) ([]Key, error)
  admin:
    - func (c *Client) Close() error
    - func (c *Client) ProjectID() string
    - func (c *Client) Endpoint() string
naming:
  Put: an upsert, which is the mutation verb it sends
  Insert_and_Update: named after the mutation verbs, so their preconditions are visible at the call site
  Mutate: the escape hatch when several verbs belong in one commit
  rejected: >
    GetItem, PutItem and Query, borrowed from api:dynamodb-client. The shapes
    differ enough that matching names would mislead; see
    concept:dynamodb-datastore-mapping.
entity_return: >
  Get returns *Entity, unlike GetItem which returns a bare Item. An Entity
  carries a key and a version alongside its properties, so there is state to
  point at, and a missing entity is ErrNoSuchEntity rather than a nil map.
types:
  Key, Entity, Value: data:datastore-value
  Query: "built by chaining: NewQuery(kind).Filter(...).Order(...).Limit(n).Start(cursor)"
  Batch: "struct { Entities []Entity; EndCursor Cursor; More MoreResults }, with HasMore()"
  Mutation: "built with InsertOp, UpdateOp, UpsertOp, DeleteOp"
  LookupResult: "struct { Found []Entity; Missing []Key; Deferred []Key }"
  Tx: "Get, GetMulti, Run, and the mutation verbs, queued until commit"
lookup_shape: >
  the wire returns found, missing and deferred lists rather than failing. Get
  turns a one-key lookup into ErrNoSuchEntity; GetMulti hands all three lists
  back, because a caller batching a thousand keys needs to know which came back.
deferred_keys: >
  the server may defer keys it did not read. GetMulti returns them rather than
  looping, matching how requirement:dynamodb-client-scope treats unprocessed
  batch keys: partial success is the caller's decision.
transaction_shape: >
  Tx queues mutations and sends them with the commit, so a closure that returns
  an error writes nothing and no rollback is needed on the happy path. A closure
  that panics or whose context expires triggers rollback. See
  decision:datastore-write-preconditions for what belongs inside one.
options:
  client: WithEndpoint, WithDatabase, WithNamespace, WithCredentials, WithCredentialsFile, WithTokenSource, WithTimeout, WithHTTPClient, WithRetry, WithMaxIdleConns
  read: WithEventualConsistency, WithReadTime, WithPropertyMask
  write: WithBaseVersion, WithUpdateTime, WithPropertyMask
  query: built on Query, not passed as options, because a query is a value worth reusing
  typing: >
    ReadOption, WriteOption and TxOption are separate interfaces, so a
    consistency option on a write is a compile error. Same discipline as
    api:dynamodb-client.
defaults:
  credentials: api:google-auth resolution from the environment when no option is given
  project: the constructor argument, then GOOGLE_CLOUD_PROJECT
  database: empty, the default database
  namespace: empty
  endpoint: DATASTORE_EMULATOR_HOST when set, else the public host
  timeout: 10s, matching api:dynamodb-client
  consistency: strong
  retry: requirement:datastore-retry-policy
  connections: 4 idle per host, released by Close; decision:dynamodb-connection-policy applies unchanged
errors:
  type: "*datastore.Error with Op, Kind, StatusCode, Status, Message"
  discrimination: the status string in the error body, not the HTTP code
  sentinels:
    - ErrNoSuchEntity, ErrAlreadyExists, ErrAborted, ErrFailedPrecondition
    - ErrInvalidArgument, ErrPermissionDenied, ErrUnauthenticated
    - ErrUnavailable, ErrDeadlineExceeded, ErrResourceExhausted, ErrInternal
    - ErrNoCredentials and ErrNoProject, aliased from cloud/google
  retryable: "(*Error).Retryable() reports whether sending it again could work"
lifecycle: >
  Close releases pooled TLS connections and drops the cached token. A client
  dropped without Close leaves native handles alive until the idle timeout,
  which is the whole reason this repository exists.
scope: requirement:datastore-client-scope
flow: flow:datastore-request
auth: api:google-auth
wire_reference: system:google-datastore
counterpart: api:dynamodb-client

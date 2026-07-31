---
id: api:dynamodb-client
type: api
title: DynamoDB Client
---
`Client` posts signed `application/x-amz-json-1.0` requests to one DynamoDB endpoint and decodes the JSON replies.

```yaml
import_path: github.com/shibukawa/tinygodriver/nosql/dynamodb
constructor: func New(opts ...Option) (*Client, error)
wire:
  method: POST to "/", one operation per request
  content_type: application/x-amz-json-1.0
  operation_header: "X-Amz-Target: DynamoDB_20120810.<Operation>"
  accept_encoding: >
    identity. gzip is not requested, because the tinygo http client does not
    transparently decompress and the payloads are small.
state: shipped 2026-07-31
methods:
  - func (c *Client) GetItem(ctx, table string, key Key, opts ...GetOption) (Item, error)
  - func (c *Client) PutItem(ctx, table string, item Item, opts ...WriteOption) (*WriteResult, error)
  - func (c *Client) UpdateItem(ctx, table string, key Key, update string, opts ...WriteOption) (*WriteResult, error)
  - func (c *Client) DeleteItem(ctx, table string, key Key, opts ...WriteOption) (*WriteResult, error)
  - func (c *Client) Query(ctx, table, keyCond string, opts ...QueryOption) (*Page, error)
  - func (c *Client) Scan(ctx, table string, opts ...ScanOption) (*Page, error)
  - func (c *Client) BatchGetItem(ctx, keys map[string][]Key, opts ...BatchOption) (*BatchGetResult, error)
  - func (c *Client) BatchWriteItem(ctx, writes map[string][]WriteRequest) (*BatchWriteResult, error)
  - func (c *Client) CreateTable(ctx, def TableDefinition) error
  - func (c *Client) DeleteTable(ctx, table string) error
  - func (c *Client) DescribeTable(ctx, table string) (*TableDescription, error)
  - func (c *Client) ListTables(ctx, opts ...ListOption) (*TableList, error)
  - func (c *Client) Close() error
  - func (c *Client) Region() string
  - func (c *Client) Endpoint() string
item_return: >
  GetItem returns Item, not *Item. Item is a map alias, so a pointer to it would
  add a level of indirection with no state to carry; absence is reported as
  ErrItemNotFound instead.
options:
  client: WithEndpoint, WithRegion, WithCredentials, WithCredentialsFromEnv, WithTimeout, WithHTTPClient, WithRetry, WithMaxIdleConns
  read: WithConsistentRead, WithProjection, WithExpressionNames
  write: WithCondition, WithReturnValues, WithExpressionNames, WithExpressionValues
  query_scan: WithIndex, WithFilter, WithExclusiveStartKey, WithLimit, WithExpressionValues
  query_only: WithScanForward
  list: WithLimit, WithStartTable
  typing: >
    each option value implements the interfaces of the operations it is valid
    for, so WithCondition on a Query is a compile error rather than a member
    DynamoDB rejects. The groups are GetOption, WriteOption, QueryOption,
    ScanOption, BatchOption and ListOption.
types:
  Key: map[string]AttributeValue, the partition and optional sort attribute
  Item: map[string]AttributeValue, see data:dynamodb-attribute-value
  Page: "struct { Items []Item; LastEvaluatedKey Key; Count, ScannedCount int }, with HasMore()"
  WriteRequest: "a put or a delete, built with PutRequest or DeleteRequest"
  batch_results: BatchGetResult and BatchWriteResult, each with HasUnprocessed()
  tables: TableDefinition, SecondaryIndex, KeyAttribute, TableDescription, TableList
defaults:
  credentials: aws.CredentialsFromEnv when no credentials option is given
  region: aws.RegionFromEnv; absent is aws.ErrNoRegion
  endpoint: AWS_ENDPOINT_URL_DYNAMODB, then AWS_ENDPOINT_URL, then the regional host
  timeout: 10s, shorter than the S3 default because these are small round trips
  retry: requirement:dynamodb-retry-policy
  connections: 4 idle per host, released by Close; see decision:dynamodb-connection-policy
errors:
  type: "*dynamodb.Error with Op, Table, StatusCode, Type, Message, RequestID"
  request_id: from x-amzn-RequestId
  discrimination: >
    the __type field of the JSON body, after the "#". Both namespaces appear:
    com.amazonaws.dynamodb.v20120810# for service errors and
    com.amazon.coral.service# for authentication failures, so match on the
    suffix only. Measured in requirement:dynamodb-driver-validation.
  sentinels:
    - ErrItemNotFound, ErrResourceNotFound, ErrConditionalCheck
    - ErrThroughputExceeded, ErrThrottled, ErrValidation
    - ErrTableInUse, ErrTableNotFound, ErrRequestTooLarge
    - ErrTransactionConflict, ErrBadCredentials, ErrChecksumMismatch, ErrServerFailure
    - ErrNoCredentials and ErrNoRegion, aliased from cloud/aws
  retryable: "(*Error).Retryable() reports whether sending it again could work"
  item_not_found: >
    GetItem with no match is a 200 with no Item member. It returns
    ErrItemNotFound rather than a nil item, so a missing item cannot be read as
    an empty one by accident.
lifecycle: >
  Close releases pooled TLS connections. A client that is dropped without Close
  leaves native handles alive until the idle timeout, which matters on the
  targets this repository exists for.
signing: api:aws-signer
scope: requirement:dynamodb-client-scope
integrity: requirement:dynamodb-retry-policy
flow: flow:dynamodb-request

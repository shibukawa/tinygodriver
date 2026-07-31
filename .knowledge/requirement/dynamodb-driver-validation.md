---
id: requirement:dynamodb-driver-validation
type: requirement
title: DynamoDB Access Validation Result
---
Measured state of the DynamoDB wire requirements under TinyGo; this is the evidence base for decision:dynamodb-json-codec and decision:dynamodb-connection-policy.

```yaml
priority: must
environment:
  date: 2026-07-30
  tinygo: 0.41.1 darwin/arm64, llvm 20.1.1
  go: 1.26.5
  sdk: aws-sdk-go-v2/service/dynamodb v1.62.2, smithy-go v1.27.5
aws_sdk_go_v2:
  verdict: fails, same first blocker as decision:no-aws-sdk-go-v2
  first_error: net/http/httputil, reached through smithy-go transport/http
  symbols: http.Transport.Dial, CloseIdleConnections, http.NewResponseController
  closure: 244 packages, including service/internal/endpoint-discovery
json_encoding:
  verdict: works, no gap against host go
  method: hand-written AttributeValue with MarshalJSON and UnmarshalJSON
  verified:
    - marshal of a nested item: S, N, B base64, BOOL, NULL, L, M, SS
    - byte-identical output to host go, 253 bytes for the same request
    - unmarshal into map[string]AV, including nested M and L
    - json.RawMessage retained for fields the driver does not decode
    - map[string]any decode of mixed number, string, bool and null
    - error envelope discrimination on the __type field
  finding: >
    encoding/json is not a blocker, which is what makes a JSON protocol service
    cheaper to hand-write than it looks. Contrast requirement:no-crypto-tls-on-tinygo,
    the blocker that actually shapes this repository.
reflection:
  verdict: works, so a struct-tag marshaler is available if wanted
  verified:
    - Type.NumField, StructField.Tag.Get, "-" skip on a struct tag
    - Value.String, Int, Float, Bool, Len, Elem, IsNil, Interface
    - Value.SetString and SetInt through a pointer, CanSet true
    - time.Time recognized through a type assertion on Value.Interface
  caveat: reflection costs binary size; decision:dynamodb-json-codec keeps it optional
response_integrity:
  x_amz_crc32: present on every reply, including error replies
  hash_crc32: crc32.ChecksumIEEE identical under both compilers
transport_cost:
  method: unsigned POSTs to dynamodb.ap-northeast-1.amazonaws.com through https, tinygo build
  before_connection_reuse: 499ms first call, then 87-110ms, measured 2026-07-30
  after_connection_reuse:
    measured_on: 2026-07-31, with requirement:connection-reuse merged
    sequential: 219ms first call, then 11-12ms
    control: 105-141ms per call with Transport.DisableKeepAlives
    concurrent: >
      four goroutines against the default MaxIdleConnsPerHost of 2 produced two
      pooled calls at 15-17ms and two fresh handshakes at 94-97ms. The per-host
      cap is the whole pool when every request goes to one host; see
      decision:dynamodb-connection-policy.
    post_idle: >
      141ms on the first call after 21s of idling, then 11ms, which is the
      20s IdleConnTimeout expiring the entry on lease as designed
  reading: >
    the residual 11-12ms is the round trip itself. Transport is no longer the
    dominant cost of a DynamoDB call, so the earlier per-request-connection
    decision is obsolete.
  upstream_record: metric:tls-handshake-cost
  probe_note: >
    the reply to an unsigned request is a real 400 with __type
    com.amazon.coral.service#MissingAuthenticationTokenException, so auth errors
    do not carry the dynamodb error namespace; see api:dynamodb-client
implementation_findings:
  measured_on: 2026-07-31, building nosql/dynamodb
  binary: tinygo builds examples/dynamodbdemo to 1.5 MB, including netdev and the TLS stack
  json_and_reflect: >
    the spike results held in real code: pointer-to-struct decoding, struct tags
    and generics all work under tinygo without a shim
  what_needed_care: >
    none of it was the encoding. The two things that took real thought were the
    two retry layers and the DescribeTable reply shape, and only a real server
    caught the second; see decision:dynamodb-local-endpoint.
conclusion: >
  nothing in the DynamoDB protocol blocks a TinyGo client, and since
  requirement:connection-reuse the transport does not dominate either. What is
  left to get right is pool sizing and the two layers of retry.
reproduce: >
  tinygo run a program that marshals an item, decodes a captured reply, and
  posts once to the regional endpoint

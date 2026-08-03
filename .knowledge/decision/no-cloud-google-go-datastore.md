---
id: decision:no-cloud-google-go-datastore
type: decision
title: Datastore Access Is Hand-Written REST, Not The Official Client
---
`nosql/datastore` implements the Datastore v1 JSON transport directly instead of wrapping `cloud.google.com/go/datastore`, for the same reason `nosql/dynamodb` does not wrap aws-sdk-go-v2.

```yaml
state: proposed
proposed_on: 2026-08-02
measured_with: tinygo 0.41.1 darwin/arm64, go 1.26.5
finding: >
  Google ships two Go clients for this API, not one, and both fail under tinygo
  for unrelated reasons. Neither failure is fixable from outside the library.
cloud_google_go_datastore:
  what: the idiomatic client, cloud.google.com/go/datastore v1.26.0
  transport: gRPC only, no REST fallback
  verdict: does not build under tinygo
  first_error:
    package: google.golang.org/grpc/internal/credentials
    message: "cfg.Clone undefined (type *tls.Config has no field or method Clone)"
  cause: >
    gRPC is TLS-only and tinygo ships crypto/tls as a stub. This is
    requirement:no-crypto-tls-on-tinygo surfacing one layer up.
  scale: 465 packages in the closure, against 244 for the DynamoDB SDK
  why_injection_does_not_help: >
    grpc.DialOption cannot remove the credentials package from the build, and
    the client exposes no transport seam at all
google_api_go_client:
  what: >
    google.golang.org/api/datastore/v1, the generated REST client, from
    google.golang.org/api v0.291.0. It exists, it speaks the JSON transport, and
    it is the closest thing to a supported alternative.
  verdict: does not build under tinygo either
  first_error:
    package: cloud.google.com/go/compute/metadata
    messages:
      - "unknown field Dial in struct literal of type http.Transport"
      - "transport.IdleConnTimeout undefined"
      - "undefined: net.Resolver"
  cause: >
    not gRPC. This is the tinygo empty net/http.Transport, the same blocker
    decision:no-aws-sdk-go-v2 hit third in its dependency order, reached through
    the credential layer rather than the request layer.
  scale: 428 packages, 64 of them grpc, still in the closure of a REST client
  deprecated_constructor_does_not_help: >
    datastore.New(*http.Client) bypasses the option and transport machinery at
    the call site, but the generated package imports google.golang.org/api/internal
    unconditionally, so the closure and the error are identical. Measured, not assumed.
  reading: >
    the generated types are not salvageable in isolation either, for the same
    reason. Reusing the schema would mean vendoring generated code away from its
    package, which is a fork.
chosen: >
  hand-written client over api:https-transport, the same stack storage/s3 and
  nosql/dynamodb already run on
what_makes_this_cheaper_than_it_looks:
  - >
    the wire form is proto3 JSON, and requirement:dynamodb-driver-validation
    already measured encoding/json and reflect as sound under tinygo
  - >
    one POST per RPC with a bearer header is a simpler request builder than
    SigV4; see api:google-auth for where the cost moved instead
  - the operation set is eight methods, all on one path prefix
consequences:
  - >
    the answer to "is there a REST library" is yes, and it does not help. Worth
    recording, because the obvious next question after the gRPC finding has a
    non-obvious answer.
  - no external dependency enters go.mod, matching decision:no-aws-sdk-go-v2
  - the API is this repository's own, see api:datastore-client
  - >
    protobuf semantics leak into the API surface anyway, because proto3 JSON
    encodes int64 as a string and oneof as exactly one member; see
    data:datastore-value
  - >
    gRPC-only features have no substitute, so anything the REST transport does
    not carry is out of requirement:datastore-client-scope by construction
related: decision:no-aws-sdk-go-v2, system:google-datastore

package datastore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/google"
)

// stub is a Datastore-shaped server that records what it was sent and replies
// from a queue.
type stub struct {
	mu       sync.Mutex
	requests []stubRequest
	replies  []stubReply
	next     int
}

type stubRequest struct {
	Op   string
	Auth string
	Body map[string]any
	Raw  []byte
}

type stubReply struct {
	status int
	body   string
}

func newStub(replies ...stubReply) *stub { return &stub{replies: replies} }

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	op := r.URL.Path
	if i := strings.LastIndex(op, ":"); i >= 0 {
		op = op[i+1:]
	}

	s.mu.Lock()
	s.requests = append(s.requests, stubRequest{
		Op:   op,
		Auth: r.Header.Get("Authorization"),
		Body: body,
		Raw:  raw,
	})
	reply := stubReply{status: 200, body: "{}"}
	if s.next < len(s.replies) {
		reply = s.replies[s.next]
		s.next++
	} else if len(s.replies) > 0 {
		reply = s.replies[len(s.replies)-1]
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reply.status)
	fmt.Fprint(w, reply.body)
}

func (s *stub) calls() []stubRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]stubRequest(nil), s.requests...)
}

func (s *stub) ops() []string {
	var out []string
	for _, r := range s.calls() {
		out = append(out, r.Op)
	}
	return out
}

func newTestClient(t *testing.T, s *stub, opts ...Option) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(s)
	t.Cleanup(server.Close)

	all := append([]Option{
		WithEndpoint(server.URL),
		WithHTTPClient(server.Client()),
		WithTokenSource(google.StaticTokenSource(google.Token{
			Value:  "test-token",
			Expiry: time.Now().Add(time.Hour),
		})),
		WithRetry(3, time.Millisecond),
	}, opts...)

	client, err := New("test-project", all...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	// Make backoff deterministic and instant.
	client.randFloat = func() float64 { return 0 }
	return client, server
}

func errorBody(status, message string) string {
	return fmt.Sprintf(`{"error":{"code":0,"status":%q,"message":%q}}`, status, message)
}

func TestGetSendsLookupAndDecodes(t *testing.T) {
	s := newStub(stubReply{200, `{
		"found": [{
			"entity": {
				"key": {"partitionId":{"projectId":"test-project"},"path":[{"kind":"User","name":"alice"}]},
				"properties": {"name":{"stringValue":"Alice"},"age":{"integerValue":"30"}}
			},
			"version": "17"
		}]
	}`})
	client, _ := newTestClient(t, s)

	entity, err := client.Get(context.Background(), NameKey("User", "alice"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if name, _ := entity.Properties["name"].AsString(); name != "Alice" {
		t.Errorf("name = %q", name)
	}
	if age, _ := entity.Properties["age"].AsInt(); age != 30 {
		t.Errorf("age = %d", age)
	}
	if entity.Version != 17 {
		t.Errorf("Version = %d, want 17", entity.Version)
	}

	call := s.calls()[0]
	if call.Op != "lookup" {
		t.Errorf("op = %q", call.Op)
	}
	if call.Auth != "Bearer test-token" {
		t.Errorf("Authorization = %q", call.Auth)
	}
	// The client supplies the project; a Key does not carry one.
	keys := call.Body["keys"].([]any)
	partition := keys[0].(map[string]any)["partitionId"].(map[string]any)
	if partition["projectId"] != "test-project" {
		t.Errorf("projectId = %v", partition["projectId"])
	}
}

func TestGetMissingIsErrNoSuchEntity(t *testing.T) {
	s := newStub(stubReply{200, `{"missing":[{"entity":{"key":{"path":[{"kind":"User","name":"bob"}]}}}]}`})
	client, _ := newTestClient(t, s)

	_, err := client.Get(context.Background(), NameKey("User", "bob"))
	if !errors.Is(err, ErrNoSuchEntity) {
		t.Fatalf("err = %v, want ErrNoSuchEntity", err)
	}
}

func TestGetMultiReturnsAllThreeLists(t *testing.T) {
	s := newStub(stubReply{200, `{
		"found":   [{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}},"version":"1"}],
		"missing": [{"entity":{"key":{"path":[{"kind":"K","name":"b"}]}}}],
		"deferred":[{"path":[{"kind":"K","name":"c"}]}]
	}`})
	client, _ := newTestClient(t, s)

	result, err := client.GetMulti(context.Background(),
		[]Key{NameKey("K", "a"), NameKey("K", "b"), NameKey("K", "c")})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Found) != 1 || len(result.Missing) != 1 || len(result.Deferred) != 1 {
		t.Fatalf("found %d, missing %d, deferred %d",
			len(result.Found), len(result.Missing), len(result.Deferred))
	}
	if !result.HasDeferred() {
		t.Error("HasDeferred = false")
	}
	// Deferred keys are handed back, not retried inside the call.
	if got := len(s.calls()); got != 1 {
		t.Errorf("%d calls; deferred keys must not be retried internally", got)
	}
}

func TestLookupKeyLimitIsCheckedLocally(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)
	keys := make([]Key, MaxLookupKeys+1)
	for i := range keys {
		keys[i] = IDKey("K", int64(i+1))
	}
	if _, err := client.GetMulti(context.Background(), keys); !errors.Is(err, ErrTooManyKeys) {
		t.Errorf("err = %v, want ErrTooManyKeys", err)
	}
	if len(s.calls()) != 0 {
		t.Error("a request the server would reject was still sent")
	}
}

func TestPutSendsUpsert(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"version":"5"}],"indexUpdates":2}`})
	client, _ := newTestClient(t, s)

	entity := NewEntity(NameKey("User", "alice")).Set("name", String("Alice"))
	key, err := client.Put(context.Background(), entity)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !key.Equal(NameKey("User", "alice")) {
		t.Errorf("key = %v", key)
	}

	body := s.calls()[0].Body
	if body["mode"] != "NON_TRANSACTIONAL" {
		t.Errorf("mode = %v", body["mode"])
	}
	mutation := body["mutations"].([]any)[0].(map[string]any)
	if _, ok := mutation["upsert"]; !ok {
		t.Errorf("Put did not send an upsert: %v", mutation)
	}
}

func TestInsertUpdateDeleteVerbs(t *testing.T) {
	cases := []struct {
		name string
		send func(*Client) error
		verb string
	}{
		{"insert", func(c *Client) error {
			_, err := c.Insert(context.Background(), NewEntity(NameKey("K", "n")))
			return err
		}, "insert"},
		{"update", func(c *Client) error {
			return c.Update(context.Background(), NewEntity(NameKey("K", "n")))
		}, "update"},
		{"delete", func(c *Client) error {
			return c.Delete(context.Background(), NameKey("K", "n"))
		}, "delete"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
			client, _ := newTestClient(t, s)
			if err := c.send(client); err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			mutation := s.calls()[0].Body["mutations"].([]any)[0].(map[string]any)
			if _, ok := mutation[c.verb]; !ok {
				t.Errorf("%s sent %v", c.name, mutation)
			}
		})
	}
}

func TestInsertOnExistingKeyIsErrAlreadyExists(t *testing.T) {
	s := newStub(stubReply{409, errorBody("ALREADY_EXISTS", "entity already exists")})
	client, _ := newTestClient(t, s)

	_, err := client.Insert(context.Background(), NewEntity(NameKey("K", "n")))
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	// ALREADY_EXISTS is a 409 and must not be retried, unlike ABORTED which
	// shares the code.
	if got := len(s.calls()); got != 1 {
		t.Errorf("%d attempts; ALREADY_EXISTS is terminal", got)
	}
}

func TestBaseVersionPrecondition(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"version":"9"}]}`})
	client, _ := newTestClient(t, s)

	err := client.Update(context.Background(), NewEntity(NameKey("K", "n")), WithBaseVersion(8))
	if err != nil {
		t.Fatal(err)
	}
	mutation := s.calls()[0].Body["mutations"].([]any)[0].(map[string]any)
	if mutation["baseVersion"] != "8" {
		t.Errorf("baseVersion = %v, want the string \"8\"", mutation["baseVersion"])
	}
}

func TestIncompleteKeyRules(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"key":{"path":[{"kind":"K","id":"5001"}]},"version":"1"}]}`})
	client, _ := newTestClient(t, s)
	ctx := context.Background()

	// Insert accepts one and gets the allocated key back.
	key, err := client.Insert(ctx, NewEntity(IncompleteKey("K")))
	if err != nil {
		t.Fatalf("Insert with incomplete key: %v", err)
	}
	if key.Path[0].ID != 5001 {
		t.Errorf("allocated key = %v", key)
	}

	// Update and Delete do not.
	if err := client.Update(ctx, NewEntity(IncompleteKey("K"))); !errors.Is(err, ErrIncompleteKey) {
		t.Errorf("Update err = %v, want ErrIncompleteKey", err)
	}
	if err := client.Delete(ctx, IncompleteKey("K")); !errors.Is(err, ErrIncompleteKey) {
		t.Errorf("Delete err = %v, want ErrIncompleteKey", err)
	}
	if _, err := client.Get(ctx, IncompleteKey("K")); !errors.Is(err, ErrIncompleteKey) {
		t.Errorf("Get err = %v, want ErrIncompleteKey", err)
	}
}

func TestRunQueryBuildsFilterAndDecodesBatch(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{
		"entityResults":[
			{"entity":{"key":{"path":[{"kind":"Task","id":"1"}]},"properties":{"done":{"booleanValue":false}}},"version":"3"}
		],
		"endCursor":"Q1B2",
		"moreResults":"NOT_FINISHED",
		"skippedResults":2
	}}`})
	client, _ := newTestClient(t, s)

	q := NewQuery("Task").
		Filter("done", Equal, Bool(false)).
		Filter("priority", GreaterThanEqual, Int(3)).
		Order("created").
		Limit(10)
	batch, err := client.Run(context.Background(), q)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(batch.Entities) != 1 {
		t.Fatalf("entities = %d", len(batch.Entities))
	}
	if batch.EndCursor != "Q1B2" || !batch.HasMore() || batch.SkippedResults != 2 {
		t.Errorf("batch = %+v", batch)
	}

	query := s.calls()[0].Body["query"].(map[string]any)
	// Two filters compose with AND; Datastore has no OR on this wire.
	composite := query["filter"].(map[string]any)["compositeFilter"].(map[string]any)
	if composite["op"] != "AND" || len(composite["filters"].([]any)) != 2 {
		t.Errorf("filter = %v", composite)
	}
	if query["limit"] != float64(10) {
		t.Errorf("limit = %v", query["limit"])
	}
}

func TestSingleFilterIsNotWrappedInComposite(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	_, err := client.Run(context.Background(), NewQuery("Task").Filter("done", Equal, Bool(true)))
	if err != nil {
		t.Fatal(err)
	}
	filter := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)
	if _, ok := filter["propertyFilter"]; !ok {
		t.Errorf("a single filter was wrapped: %v", filter)
	}
}

func TestAncestorQueryCarriesPartitionedKey(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	parent := NameKey("Account", "acme")
	_, err := client.Run(context.Background(), NewQuery("Task").Ancestor(parent))
	if err != nil {
		t.Fatal(err)
	}
	filter := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)
	property := filter["propertyFilter"].(map[string]any)
	if property["op"] != "HAS_ANCESTOR" {
		t.Errorf("op = %v", property["op"])
	}
	// A key inside a filter needs the project like any other key.
	key := property["value"].(map[string]any)["keyValue"].(map[string]any)
	partition := key["partitionId"].(map[string]any)
	if partition["projectId"] != "test-project" {
		t.Errorf("ancestor key lost its partition: %v", partition)
	}
}

func TestQueryIsImmutable(t *testing.T) {
	base := NewQuery("Task").Filter("a", Equal, Int(1))
	left := base.Filter("b", Equal, Int(2))
	right := base.Filter("c", Equal, Int(3))
	if len(base.conditions) != 1 {
		t.Errorf("base gained conditions: %d", len(base.conditions))
	}
	if len(left.conditions) != 2 || len(right.conditions) != 2 {
		t.Errorf("branches = %d, %d", len(left.conditions), len(right.conditions))
	}
	// The branches must hold different conditions, not one shared slice.
	leftProp := left.conditions[1].(propCondition).property
	rightProp := right.conditions[1].(propCondition).property
	if leftProp == rightProp {
		t.Error("branches share state")
	}
}

func TestCursorPagination(t *testing.T) {
	s := newStub(
		stubReply{200, `{"batch":{"entityResults":[{"entity":{"key":{"path":[{"kind":"T","id":"1"}]},"properties":{}}}],"endCursor":"c1","moreResults":"NOT_FINISHED"}}`},
		stubReply{200, `{"batch":{"entityResults":[{"entity":{"key":{"path":[{"kind":"T","id":"2"}]},"properties":{}}}],"endCursor":"c2","moreResults":"NO_MORE_RESULTS"}}`},
	)
	client, _ := newTestClient(t, s)
	ctx := context.Background()

	var seen []int64
	q := NewQuery("T")
	for {
		batch, err := client.Run(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range batch.Entities {
			seen = append(seen, e.Key.Path[0].ID)
		}
		if !batch.HasMore() {
			break
		}
		q = q.Start(batch.EndCursor)
	}
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Errorf("paged ids = %v", seen)
	}
	if got := s.calls()[1].Body["query"].(map[string]any)["startCursor"]; got != "c1" {
		t.Errorf("second page startCursor = %v", got)
	}
}

func TestCountUsesAggregationQuery(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"count":{"integerValue":"42"}}}]}}`})
	client, _ := newTestClient(t, s)

	n, err := client.Count(context.Background(), NewQuery("Task"))
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 42 {
		t.Errorf("count = %d", n)
	}
	if s.calls()[0].Op != "runAggregationQuery" {
		t.Errorf("op = %q", s.calls()[0].Op)
	}
}

func TestEventualConsistencyIsOptIn(t *testing.T) {
	s := newStub(stubReply{200, `{"found":[]}`}, stubReply{200, `{"found":[]}`})
	client, _ := newTestClient(t, s)
	ctx := context.Background()

	_, _ = client.GetMulti(ctx, []Key{NameKey("K", "a")})
	if _, present := s.calls()[0].Body["readOptions"]; present {
		t.Error("strong consistency should send no readOptions at all")
	}

	_, _ = client.GetMulti(ctx, []Key{NameKey("K", "a")}, WithEventualConsistency())
	options := s.calls()[1].Body["readOptions"].(map[string]any)
	if options["readConsistency"] != "EVENTUAL" {
		t.Errorf("readOptions = %v", options)
	}
}

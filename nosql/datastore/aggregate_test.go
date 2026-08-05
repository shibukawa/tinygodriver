package datastore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSumSendsASumAggregation(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"sum":{"integerValue":"120"}}}]}}`})
	client, _ := newTestClient(t, s)

	got, err := client.Sum(context.Background(), NewQuery("Reading"), "celsius")
	if err != nil {
		t.Fatalf("Sum: %v", err)
	}
	if n, ok := got.AsInt(); !ok || n != 120 {
		t.Errorf("sum = %v", got)
	}

	call := s.calls()[0]
	if call.Op != "runAggregationQuery" {
		t.Errorf("op = %q", call.Op)
	}
	agg := call.Body["aggregationQuery"].(map[string]any)["aggregations"].([]any)[0].(map[string]any)
	if agg["alias"] != "sum" {
		t.Errorf("alias = %v", agg["alias"])
	}
	property := agg["sum"].(map[string]any)["property"].(map[string]any)
	if property["name"] != "celsius" {
		t.Errorf("property = %v", property)
	}
}

// TestSumKeepsIntegerAndDoubleApart is why Sum returns a Value: the service
// answers with whichever the data was, and flattening it here would erase the
// same distinction the rest of the package keeps.
func TestSumKeepsIntegerAndDoubleApart(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"sum":{"doubleValue":12.5}}}]}}`})
	client, _ := newTestClient(t, s)

	got, err := client.Sum(context.Background(), NewQuery("Reading"), "celsius")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != KindDouble {
		t.Fatalf("kind = %v, want double", got.Kind())
	}
	if f, ok := got.AsFloat(); !ok || f != 12.5 {
		t.Errorf("sum = %v", got)
	}
	if _, ok := got.AsInt(); ok {
		t.Error("a double sum should not read as an integer")
	}
}

func TestAvgSendsAnAvgAggregation(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"avg":{"doubleValue":21.25}}}]}}`})
	client, _ := newTestClient(t, s)

	got, err := client.Avg(context.Background(), NewQuery("Reading"), "celsius")
	if err != nil {
		t.Fatalf("Avg: %v", err)
	}
	if f, ok := got.AsFloat(); !ok || f != 21.25 {
		t.Errorf("avg = %v", got)
	}
	agg := s.calls()[0].Body["aggregationQuery"].(map[string]any)["aggregations"].([]any)[0].(map[string]any)
	if _, ok := agg["avg"]; !ok {
		t.Errorf("aggregation = %v", agg)
	}
}

// TestAvgOfNothingIsNull pins why Avg returns a Value rather than a float64:
// zero would be a different claim from "no data".
func TestAvgOfNothingIsNull(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"avg":{"nullValue":null}}}]}}`})
	client, _ := newTestClient(t, s)

	got, err := client.Avg(context.Background(), NewQuery("Reading"), "celsius")
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsNull() {
		t.Errorf("avg over no entities = %v, want null", got)
	}
}

func TestAggregationsNeedAProperty(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)
	if _, err := client.Sum(context.Background(), NewQuery("K"), ""); err == nil {
		t.Error("Sum accepted an empty property")
	}
	if _, err := client.Avg(context.Background(), NewQuery("K"), ""); err == nil {
		t.Error("Avg accepted an empty property")
	}
	if len(s.calls()) != 0 {
		t.Error("a request the server would reject was still sent")
	}
}

func TestAggregationsWorkInsideATransaction(t *testing.T) {
	s := newStub(
		stubReply{200, `{"transaction":"dHgtMQ==","batch":{"aggregationResults":[{"aggregateProperties":{"sum":{"integerValue":"7"}}}]}}`},
		stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"avg":{"doubleValue":3.5}}}]}}`},
		stubReply{200, `{}`},
	)
	client, _ := newTestClient(t, s)

	err := client.RunReadOnly(context.Background(), func(tx *Tx) error {
		sum, err := tx.Sum(context.Background(), NewQuery("Reading"), "celsius")
		if err != nil {
			return err
		}
		if n, _ := sum.AsInt(); n != 7 {
			t.Errorf("sum = %v", sum)
		}
		avg, err := tx.Avg(context.Background(), NewQuery("Reading"), "celsius")
		if err != nil {
			return err
		}
		if f, _ := avg.AsFloat(); f != 3.5 {
			t.Errorf("avg = %v", avg)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunReadOnly: %v", err)
	}
	// The first aggregation starts the transaction; the second uses the handle
	// its reply carried.
	first := s.calls()[0].Body["readOptions"].(map[string]any)
	if _, ok := first["newTransaction"]; !ok {
		t.Errorf("the first aggregation did not start a transaction: %v", first)
	}
	second := s.calls()[1].Body["readOptions"].(map[string]any)
	if second["transaction"] != "dHgtMQ==" {
		t.Errorf("the second aggregation did not carry the handle: %v", second)
	}
}

func TestMutationSizeMatchesTheEncodedMutation(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
	client, _ := newTestClient(t, s)

	entity := NewEntity(NameKey("Task", "first")).
		Set("title", String("a reasonably long title to make the size interesting")).
		Set("payload", Blob(make([]byte, 256)))
	m := UpsertOp(entity)

	size, err := client.MutationSize(m)
	if err != nil {
		t.Fatalf("MutationSize: %v", err)
	}
	if size <= 0 {
		t.Fatalf("size = %d", size)
	}

	// The figure must match what actually goes on the wire, so compare it
	// against the mutation the client sends rather than against itself.
	if _, err := client.Put(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	sent := s.calls()[0].Body["mutations"].([]any)[0]
	raw, err := jsonMarshal(sent)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != size {
		t.Errorf("MutationSize = %d, the mutation on the wire is %d bytes", size, len(raw))
	}

	// And it must include the partition, which is the part an Entity-level
	// figure could not see.
	if !strings.Contains(string(raw), "test-project") {
		t.Error("the sized mutation carried no project")
	}
}

func TestMutationSizeReportsEncodingErrors(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)
	// An incomplete key is legal only in an insert.
	if _, err := client.MutationSize(UpdateOp(NewEntity(IncompleteKey("K")))); err == nil {
		t.Error("MutationSize accepted a mutation the client would refuse to send")
	}
}

// jsonMarshal re-encodes what the stub decoded, so a size can be compared
// against the bytes the client actually produced.
func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// TestCommitOverheadCompletesTheRequest asserts the identity the two size
// methods exist to give a caller:
//
//	CommitOverheadBytes(n) + Σ MutationSize == the bytes sent to :commit
//
// A caller chunking against MaxRequestBytes by summing MutationSize alone
// undercounts by this envelope, which is what the reporter measured downstream
// before there was anything here to ask. It is compared against the raw request
// body rather than a re-encoding of it, since the question is what the server
// receives.
func TestCommitOverheadCompletesTheRequest(t *testing.T) {
	for _, n := range []int{1, 2, 10, 100, 1000} {
		s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
		client, _ := newTestClient(t, s)

		ms, sum := sizedMutations(t, client, n)
		if _, err := client.Mutate(context.Background(), ms); err != nil {
			t.Fatal(err)
		}

		body := len(s.calls()[0].Raw)
		if got := client.CommitOverheadBytes(n) + sum; got != body {
			t.Errorf("n=%d: overhead+mutations = %d, the commit body is %d bytes", n, got, body)
		}
	}
}

// TestCommitOverheadCountsTheDatabaseTwice pins the one part of this that looks
// like double counting and is not. Every key carries the database, so a named
// database is inside each mutation, and the request carries it once more at the
// top level where MutationSize cannot see it.
func TestCommitOverheadCountsTheDatabaseTwice(t *testing.T) {
	plain := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
	plainClient, _ := newTestClient(t, plain)
	named := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
	namedClient, _ := newTestClient(t, named, WithDatabase("my-named-database"))

	const n = 3
	for _, c := range []struct {
		name   string
		client *Client
		stub   *stub
	}{{"default", plainClient, plain}, {"named", namedClient, named}} {
		ms, sum := sizedMutations(t, c.client, n)
		if _, err := c.client.Mutate(context.Background(), ms); err != nil {
			t.Fatal(err)
		}
		body := len(c.stub.calls()[0].Raw)
		if got := c.client.CommitOverheadBytes(n) + sum; got != body {
			t.Errorf("%s: overhead+mutations = %d, the commit body is %d bytes", c.name, got, body)
		}
	}

	// The request-level databaseId is the overhead difference, and it is
	// counted once however many mutations ride along.
	const databaseID = `"databaseId":"my-named-database",`
	for _, n := range []int{1, 100} {
		diff := namedClient.CommitOverheadBytes(n) - plainClient.CommitOverheadBytes(n)
		if diff != len(databaseID) {
			t.Errorf("n=%d: the named database changed the envelope by %d, expected %d", n, diff, len(databaseID))
		}
	}
}

// TestTxCommitOverheadCoversBothTransactionShapes checks the figure a caller
// chunking inside a transaction needs.
//
// The commit carries either the handle a read returned or a
// singleUseTransaction block, and those are different sizes, which is why this
// is a Tx method: the same argument that put MutationSize on Client rather than
// Entity, since only the transaction knows which shape it is in.
func TestTxCommitOverheadCoversBothTransactionShapes(t *testing.T) {
	t.Run("write only, folded into the commit", func(t *testing.T) {
		s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
		client, _ := newTestClient(t, s)

		var overhead, sum int
		err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
			ms, total := sizedMutations(t, client, 4)
			tx.Mutate(ms...)
			overhead, sum = tx.CommitOverheadBytes(len(ms)), total
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertCommitAccountedFor(t, s, overhead, sum)
	})

	t.Run("read first, committed against the handle", func(t *testing.T) {
		s := newStub(
			stubReply{200, `{"found":[{"entity":{"key":{"partitionId":{"projectId":"test-project"},` +
				`"path":[{"kind":"Task","name":"first"}]},"properties":{}},"version":"1"}],` +
				`"transaction":"aGFuZGxlLWJ5dGVzLWhlcmU="}`},
			stubReply{200, `{"mutationResults":[{"version":"1"}]}`},
		)
		client, _ := newTestClient(t, s)

		var overhead, sum int
		err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
			if _, err := tx.Get(context.Background(), NameKey("Task", "first")); err != nil {
				return err
			}
			ms, total := sizedMutations(t, client, 4)
			tx.Mutate(ms...)
			overhead, sum = tx.CommitOverheadBytes(len(ms)), total
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		assertCommitAccountedFor(t, s, overhead, sum)
	})
}

// assertCommitAccountedFor checks the identity against the commit the stub
// received, whichever request that turned out to be.
func assertCommitAccountedFor(t *testing.T, s *stub, overhead, sum int) {
	t.Helper()
	for _, c := range s.calls() {
		if c.Op != "commit" {
			continue
		}
		if got := overhead + sum; got != len(c.Raw) {
			t.Errorf("overhead+mutations = %d, the commit body is %d bytes:\n%s", got, len(c.Raw), c.Raw)
		}
		return
	}
	t.Fatalf("no commit was sent; ops were %v", s.ops())
}

// TestReadmeChunkingLoopNeverOverflows runs the chunking loop the README
// documents, against a limit small enough to exercise it.
//
// The loop is what a consumer copies, and the version before this change summed
// MutationSize alone and so undercounted every commit by the envelope. A worked
// example that is subtly wrong is worse than none, so the example is executed
// rather than eyeballed: with a named database, varying entity sizes, and a
// 2000-byte limit, no commit it produces may exceed that limit.
func TestReadmeChunkingLoopNeverOverflows(t *testing.T) {
	const limit = 2000

	s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
	client, _ := newTestClient(t, s, WithDatabase("my-named-database"))

	var mutations []Mutation
	for i := 0; i < 200; i++ {
		e := NewEntity(NameKey("Task", fmt.Sprintf("key-%04d", i))).
			Set("s", String(strings.Repeat("v", i%97)))
		mutations = append(mutations, UpsertOp(e))
	}

	ctx := context.Background()
	batch, used := []Mutation{}, 0
	for _, m := range mutations {
		n, err := client.MutationSize(m)
		if err != nil {
			t.Fatal(err)
		}
		if used+n+client.CommitOverheadBytes(len(batch)+1) > limit {
			if _, err := client.Mutate(ctx, batch); err != nil {
				t.Fatal(err)
			}
			batch, used = batch[:0], 0
		}
		batch, used = append(batch, m), used+n
	}
	if _, err := client.Mutate(ctx, batch); err != nil {
		t.Fatal(err)
	}

	calls := s.calls()
	if len(calls) < 5 {
		t.Fatalf("only %d commits; the limit was not exercised", len(calls))
	}
	largest := 0
	for i, c := range calls {
		if len(c.Raw) > limit {
			t.Errorf("commit %d is %d bytes, over the %d limit", i, len(c.Raw), limit)
		}
		if len(c.Raw) > largest {
			largest = len(c.Raw)
		}
	}
	t.Logf("%d commits, largest %d bytes against a %d limit", len(calls), largest, limit)
}

// sizedMutations builds n upserts and their total measured size.
func sizedMutations(t *testing.T, client *Client, n int) ([]Mutation, int) {
	t.Helper()
	var ms []Mutation
	sum := 0
	for i := 0; i < n; i++ {
		e := NewEntity(NameKey("Task", fmt.Sprintf("key-%04d", i))).
			Set("s", String(strings.Repeat("v", 64)))
		m := UpsertOp(e)
		size, err := client.MutationSize(m)
		if err != nil {
			t.Fatalf("MutationSize: %v", err)
		}
		ms = append(ms, m)
		sum += size
	}
	return ms, sum
}

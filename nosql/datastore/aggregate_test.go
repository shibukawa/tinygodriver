package datastore

import (
	"context"
	"encoding/json"
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
		stubReply{200, beganTransaction},
		stubReply{200, `{"batch":{"aggregationResults":[{"aggregateProperties":{"sum":{"integerValue":"7"}}}]}}`},
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
	// Both aggregations must carry the transaction handle.
	for _, call := range s.calls()[1:3] {
		options, ok := call.Body["readOptions"].(map[string]any)
		if !ok || options["transaction"] != "dHgtMQ==" {
			t.Errorf("%s did not carry the handle: %v", call.Op, call.Body["readOptions"])
		}
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

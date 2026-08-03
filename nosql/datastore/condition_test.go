package datastore

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"
)

// TestIntCannotWrap is the regression for the one silent wrong write this
// package had. Int(uint(MaxUint64)) stored "-1" and reported no error, because
// Int converted through int64 and ~uint admitted values int64 cannot hold.
//
// The fix is in the type constraint, so the proof is that the wrapping call no
// longer compiles. What can be asserted at runtime is the other half: every
// type the constraint still admits round-trips exactly.
func TestIntCannotWrap(t *testing.T) {
	// datastore.Int(uint(math.MaxUint64)) is now a compile error.
	// The two supported ways to carry a large unsigned value:
	if got, _ := Int(int64(math.MaxInt64)).AsNumber(); got != "9223372036854775807" {
		t.Errorf("MaxInt64 = %q", got)
	}
	raw, err := json.Marshal(IntString(strconv.FormatUint(math.MaxUint64, 10)))
	if err == nil {
		t.Errorf("a uint64 above MaxInt64 encoded as %s; it has no representation", raw)
	}

	// Everything the constraint still admits fits int64 on every platform.
	cases := []struct {
		name string
		got  Value
		want string
	}{
		{"int", Int(42), "42"},
		{"negative", Int(-1), "-1"},
		{"int64 max", Int(int64(math.MaxInt64)), "9223372036854775807"},
		{"int64 min", Int(int64(math.MinInt64)), "-9223372036854775808"},
		{"uint8 max", Int(uint8(math.MaxUint8)), "255"},
		{"uint16 max", Int(uint16(math.MaxUint16)), "65535"},
		{"uint32 max", Int(uint32(math.MaxUint32)), "4294967295"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, ok := c.got.AsNumber()
			if !ok || text != c.want {
				t.Errorf("got %q, want %q", text, c.want)
			}
			if _, err := json.Marshal(c.got); err != nil {
				t.Errorf("encode: %v", err)
			}
		})
	}
}

func TestOrProducesADisjunction(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	q := NewQuery("Task").Where(Or(
		Prop("state", Equal, String("new")),
		Prop("priority", GreaterThanEqual, Int(8)),
	))
	if _, err := client.Run(context.Background(), q); err != nil {
		t.Fatalf("Run: %v", err)
	}
	filter := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)
	composite, ok := filter["compositeFilter"].(map[string]any)
	if !ok {
		t.Fatalf("filter = %v", filter)
	}
	if composite["op"] != "OR" {
		t.Errorf("op = %v, want OR", composite["op"])
	}
	if n := len(composite["filters"].([]any)); n != 2 {
		t.Errorf("%d operands", n)
	}
}

// TestWhereAndFilterCombineWithAnd pins the rule that keeps Or scoped: an Or
// belongs inside one call, because separate calls are ANDed.
func TestWhereAndFilterCombineWithAnd(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	q := NewQuery("Task").
		Filter("done", Equal, Bool(false)).
		Where(Or(
			Prop("state", Equal, String("new")),
			Prop("state", Equal, String("open")),
		))
	if _, err := client.Run(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	outer := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)["compositeFilter"].(map[string]any)
	if outer["op"] != "AND" {
		t.Fatalf("outer op = %v, want AND", outer["op"])
	}
	operands := outer["filters"].([]any)
	if len(operands) != 2 {
		t.Fatalf("%d operands", len(operands))
	}
	// The second operand is the nested disjunction.
	nested, ok := operands[1].(map[string]any)["compositeFilter"].(map[string]any)
	if !ok {
		t.Fatalf("second operand = %v", operands[1])
	}
	if nested["op"] != "OR" {
		t.Errorf("nested op = %v, want OR", nested["op"])
	}
}

func TestNestedAndOr(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	q := NewQuery("Task").Where(And(
		Prop("owner", Equal, String("me")),
		Or(
			Prop("priority", GreaterThan, Int(5)),
			And(
				Prop("starred", Equal, Bool(true)),
				Prop("done", Equal, Bool(false)),
			),
		),
	))
	if _, err := client.Run(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	root := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)["compositeFilter"].(map[string]any)
	if root["op"] != "AND" || len(root["filters"].([]any)) != 2 {
		t.Fatalf("root = %v", root)
	}
	or := root["filters"].([]any)[1].(map[string]any)["compositeFilter"].(map[string]any)
	if or["op"] != "OR" {
		t.Fatalf("or = %v", or)
	}
	inner := or["filters"].([]any)[1].(map[string]any)["compositeFilter"].(map[string]any)
	if inner["op"] != "AND" || len(inner["filters"].([]any)) != 2 {
		t.Errorf("inner AND = %v", inner)
	}
}

// TestCompositeOfOneIsUnwrapped keeps the request saying what the tree means:
// a wrapper around a single operand would read as if it meant more.
func TestCompositeOfOneIsUnwrapped(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	q := NewQuery("Task").Where(Or(Prop("a", Equal, Int(1))))
	if _, err := client.Run(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	filter := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)
	if _, wrapped := filter["compositeFilter"]; wrapped {
		t.Errorf("a one-operand Or was wrapped: %v", filter)
	}
	if _, ok := filter["propertyFilter"]; !ok {
		t.Errorf("filter = %v", filter)
	}
}

func TestEmptyCompositeIsRejected(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)

	for _, c := range []Condition{And(), Or()} {
		_, err := client.Run(context.Background(), NewQuery("Task").Where(c))
		if !errors.Is(err, ErrEmptyCondition) {
			t.Errorf("err = %v, want ErrEmptyCondition", err)
		}
	}
	if len(s.calls()) != 0 {
		t.Error("a meaningless filter was still sent")
	}
}

func TestAncestorOfCarriesThePartition(t *testing.T) {
	s := newStub(stubReply{200, `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`})
	client, _ := newTestClient(t, s)

	// Through a disjunction, so the key value is rendered inside a nested tree
	// rather than at the top level.
	q := NewQuery("Task").Where(Or(
		AncestorOf(NameKey("Account", "acme")),
		Prop("public", Equal, Bool(true)),
	))
	if _, err := client.Run(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	or := s.calls()[0].Body["query"].(map[string]any)["filter"].(map[string]any)["compositeFilter"].(map[string]any)
	ancestor := or["filters"].([]any)[0].(map[string]any)["propertyFilter"].(map[string]any)
	key := ancestor["value"].(map[string]any)["keyValue"].(map[string]any)
	partition := key["partitionId"].(map[string]any)
	if partition["projectId"] != "test-project" {
		t.Errorf("a key inside a disjunction lost its partition: %v", partition)
	}
}

func TestBadOperatorInACondition(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)
	_, err := client.Run(context.Background(), NewQuery("Task").Where(Prop("a", "sideways", Int(1))))
	if !errors.Is(err, ErrBadOperator) {
		t.Errorf("err = %v, want ErrBadOperator", err)
	}
	_, err = client.Run(context.Background(), NewQuery("Task").Where(Prop("", Equal, Int(1))))
	if !errors.Is(err, ErrBadOperator) {
		t.Errorf("unnamed property err = %v", err)
	}
}

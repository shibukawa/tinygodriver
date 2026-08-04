package datastore

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// keyWithoutPartition matches a keyValue whose object starts with "path",
// meaning no partitionId preceded it. json.Marshal orders struct fields as
// declared, and wireKey declares partitionId first, so this is exact rather
// than heuristic.
var keyWithoutPartition = regexp.MustCompile(`"keyValue":\{"path"`)

func countKeys(raw string) int { return strings.Count(raw, `"keyValue"`) }

func requestBody(t *testing.T, s *stub, i int) string {
	t.Helper()
	raw, err := json.Marshal(s.calls()[i].Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestEveryKeyOnTheWireCarriesItsPartition is the regression for a bug that was
// partial in exactly the way that hides: a key that was directly a filter's
// value got the partition, and the identical key one level down did not.
//
// A Key deliberately carries only what identifies an entity — the project,
// database and namespace are the client's — so nothing below the client can
// attach them. That makes "every path that sends a value must go through the
// client's encoder" a rule rather than a convenience, and the four positions
// below are the ones that exist.
func TestEveryKeyOnTheWireCarriesItsPartition(t *testing.T) {
	other := NameKey("Other", "o-1")
	ctx := context.Background()

	cases := []struct {
		name     string
		reply    string
		send     func(*Client) error
		wantKeys int
	}{
		{
			name:  "filter value is a key",
			reply: `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`,
			send: func(c *Client) error {
				_, err := c.Run(ctx, NewQuery("T").Where(Prop("ref", Equal, KeyValue(other))))
				return err
			},
			wantKeys: 1,
		},
		{
			// This is `where ref in {refs}`, which a generator emits for
			// "entities holding any of these references".
			name:  "filter value is an array of keys",
			reply: `{"batch":{"moreResults":"NO_MORE_RESULTS"}}`,
			send: func(c *Client) error {
				_, err := c.Run(ctx, NewQuery("T").Where(
					Prop("ref", In, Array(KeyValue(other), KeyValue(NameKey("Other", "o-2"))))))
				return err
			},
			wantKeys: 2,
		},
		{
			// The one that writes data: a stored reference to another entity.
			name:  "entity property is a key",
			reply: `{"mutationResults":[{"version":"1"}]}`,
			send: func(c *Client) error {
				_, err := c.Put(ctx, NewEntity(NameKey("T", "x")).Set("ref", KeyValue(other)))
				return err
			},
			wantKeys: 1, // the entity's own key is "key", not "keyValue"
		},
		{
			name:  "key inside a nested entity value",
			reply: `{"mutationResults":[{"version":"1"}]}`,
			send: func(c *Client) error {
				_, err := c.Put(ctx, NewEntity(NameKey("T", "x")).
					Set("meta", Nested(Entity{Properties: map[string]Value{
						"ref": KeyValue(other),
					}})))
				return err
			},
			wantKeys: 1,
		},
		{
			name:  "key inside an array inside a nested entity",
			reply: `{"mutationResults":[{"version":"1"}]}`,
			send: func(c *Client) error {
				_, err := c.Put(ctx, NewEntity(NameKey("T", "x")).
					Set("meta", Nested(Entity{Properties: map[string]Value{
						"refs": Array(KeyValue(other)),
					}})))
				return err
			},
			wantKeys: 1,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStub(stubReply{200, c.reply})
			client, _ := newTestClient(t, s)
			if err := c.send(client); err != nil {
				t.Fatal(err)
			}
			body := requestBody(t, s, 0)
			if got := countKeys(body); got != c.wantKeys {
				t.Fatalf("%d keys on the wire, want %d: %s", got, c.wantKeys, body)
			}
			if loc := keyWithoutPartition.FindString(body); loc != "" {
				t.Errorf("a key went out with no partitionId: %s", body)
			}
			// Count partitions attached to keyValue members specifically. The
			// request itself also carries a top-level partitionId, so a global
			// count of projectId would not say what this is asserting.
			if n := strings.Count(body, `"keyValue":{"partitionId"`); n != c.wantKeys {
				t.Errorf("%d of %d keyValues carry a partition: %s", n, c.wantKeys, body)
			}
		})
	}
}

// TestNamespaceOverrideSurvivesNesting checks the other half of a partition:
// a key's own namespace must beat the client's, wherever the key sits.
func TestNamespaceOverrideSurvivesNesting(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"}]}`})
	client, _ := newTestClient(t, s, WithNamespace("default-ns"))

	tenant := NameKey("Other", "o-1").WithNamespace("tenant-a")
	_, err := client.Put(context.Background(), NewEntity(NameKey("T", "x")).
		Set("refs", Array(KeyValue(tenant))))
	if err != nil {
		t.Fatal(err)
	}
	body := requestBody(t, s, 0)
	if !strings.Contains(body, `"namespaceId":"tenant-a"`) {
		t.Errorf("a nested key lost its own namespace: %s", body)
	}
	if !strings.Contains(body, `"namespaceId":"default-ns"`) {
		t.Errorf("the entity key lost the client namespace: %s", body)
	}
}

// TestEncodeValueKeepsTheUnionRules makes sure routing through the client's
// encoder did not lose the checks the public codec performs.
func TestEncodeValueKeepsTheUnionRules(t *testing.T) {
	partition := &wirePartitionID{ProjectID: "p"}

	if _, err := encodeValue(Value{}, partition); err != ErrEmptyValue {
		t.Errorf("empty value: err = %v", err)
	}
	s, n := "x", "1"
	if _, err := encodeValue(Value{String: &s, Integer: &n}, partition); err != ErrAmbiguousValue {
		t.Errorf("two members: err = %v", err)
	}
	withKey := Nested(NewEntity(NameKey("K", "n")))
	if _, err := encodeValue(withKey, partition); err == nil {
		t.Error("an embedded entity carrying a key was accepted")
	}
	// A malformed value nested inside an array must still be caught.
	if _, err := encodeValue(Array(Value{}), partition); err != ErrEmptyValue {
		t.Errorf("empty value inside an array: err = %v", err)
	}
	if _, err := encodeValue(Nested(Entity{Properties: map[string]Value{"bad": {}}}), partition); err == nil {
		t.Error("an empty value inside a nested entity was accepted")
	}
}

// TestEncodeValueMatchesTheCodecWhereThereIsNoKey pins that the two encoders do
// not drift for the values that cannot contain a key. Those go through
// Value.MarshalJSON unchanged, and this is what says so.
func TestEncodeValueMatchesTheCodecWhereThereIsNoKey(t *testing.T) {
	partition := &wirePartitionID{ProjectID: "p"}
	for _, v := range []Value{
		String("s"), Int(1), Float(1.5), Bool(true), Null(),
		Blob([]byte{1, 2}), GeoPoint(1, 2), Unindexed(String("x")),
	} {
		want, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		got, err := encodeValue(v, partition)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("encodeValue = %s, codec = %s", got, want)
		}
	}
}

// TestExcludeFromIndexesSurvivesTheKeyPaths checks the field that rides
// alongside the union member, on the three branches that build JSON by hand.
func TestExcludeFromIndexesSurvivesTheKeyPaths(t *testing.T) {
	partition := &wirePartitionID{ProjectID: "p"}
	for _, v := range []Value{
		Unindexed(KeyValue(NameKey("K", "n"))),
		Unindexed(Array(String("a"))),
		Unindexed(Nested(Entity{Properties: map[string]Value{"a": String("b")}})),
	} {
		raw, err := encodeValue(v, partition)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"excludeFromIndexes":true`) {
			t.Errorf("lost excludeFromIndexes: %s", raw)
		}
	}
}

//go:build !tinygo

package dynamodb_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/aws"
	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

// TestIntegration runs the whole surface against a real endpoint. It is skipped
// unless DYNAMODB_TEST_ENDPOINT is set, for example against DynamoDB Local:
//
//	docker run -d -p 8000:8000 amazon/dynamodb-local \
//		-jar DynamoDBLocal.jar -inMemory -sharedDb
//	DYNAMODB_TEST_ENDPOINT=http://127.0.0.1:8000 go test ./nosql/dynamodb/
//
// -sharedDb is required: without it the server partitions data by access key
// and region, so a table created with one credential is invisible to another.
//
// The local server accepts any well-formed credentials without checking the
// signature, so this proves the request shapes and the decoding, not the
// signing. Signing is covered by the known-answer tests in cloud/aws and by a
// run against a real regional endpoint.
func TestIntegration(t *testing.T) {
	endpoint := os.Getenv("DYNAMODB_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DYNAMODB_TEST_ENDPOINT to run integration tests")
	}
	region := os.Getenv("DYNAMODB_TEST_REGION")
	if region == "" {
		region = "ap-northeast-1"
	}

	opts := []dynamodb.Option{
		dynamodb.WithEndpoint(endpoint),
		dynamodb.WithRegion(region),
	}
	if id := os.Getenv("DYNAMODB_TEST_ACCESS_KEY"); id != "" {
		opts = append(opts, dynamodb.WithCredentials(aws.Credentials{
			AccessKeyID:     id,
			SecretAccessKey: os.Getenv("DYNAMODB_TEST_SECRET_KEY"),
		}))
	} else {
		opts = append(opts, dynamodb.WithCredentials(aws.Credentials{
			AccessKeyID: "local", SecretAccessKey: "local",
		}))
	}

	client, err := dynamodb.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	table := fmt.Sprintf("tinygodriver-test-%d", time.Now().UnixNano())
	ctx := context.Background()

	createTable(t, client, table)
	defer func() {
		if err := client.DeleteTable(ctx, table); err != nil {
			t.Errorf("DeleteTable: %v", err)
		}
	}()

	t.Run("PutAndGet", func(t *testing.T) {
		// A key with multi-byte text, an empty string, binary, a nested map and
		// a number too precise for float64: the cases a hand-written codec is
		// most likely to get wrong.
		item := dynamodb.Item{
			"sensor":  dynamodb.S("室温 (room-1)"),
			"at":      dynamodb.N(1),
			"note":    dynamodb.S(""),
			"blob":    dynamodb.B([]byte{0, 1, 254, 255}),
			"precise": dynamodb.NString("123456789012345678901234567890"),
			"nested": dynamodb.Map(map[string]dynamodb.AttributeValue{
				"list": dynamodb.List(dynamodb.S("a"), dynamodb.Null(), dynamodb.Bool(false)),
				"tags": dynamodb.SS("x", "y"),
			}),
		}
		if _, err := client.PutItem(ctx, table, item); err != nil {
			t.Fatalf("PutItem: %v", err)
		}

		got, err := client.GetItem(ctx, table, dynamodb.Key{
			"sensor": dynamodb.S("室温 (room-1)"),
			"at":     dynamodb.N(1),
		}, dynamodb.WithConsistentRead(true))
		if err != nil {
			t.Fatalf("GetItem: %v", err)
		}
		if v, ok := got["note"].AsString(); !ok || v != "" {
			t.Errorf("empty string did not round-trip: %v, %v", v, ok)
		}
		if v, ok := got["precise"].AsNumber(); !ok || v != "123456789012345678901234567890" {
			t.Errorf("precise number = %q", v)
		}
		if v, ok := got["blob"].AsBytes(); !ok || len(v) != 4 || v[3] != 255 {
			t.Errorf("binary = %v", v)
		}
		nested, ok := got["nested"].AsMap()
		if !ok {
			t.Fatalf("nested = %v", got["nested"])
		}
		if list, ok := nested["list"].AsList(); !ok || len(list) != 3 || !list[1].IsNull() {
			t.Errorf("nested list = %v", nested["list"])
		}
	})

	t.Run("MissingItem", func(t *testing.T) {
		_, err := client.GetItem(ctx, table, dynamodb.Key{
			"sensor": dynamodb.S("nope"), "at": dynamodb.N(0),
		})
		if !errors.Is(err, dynamodb.ErrItemNotFound) {
			t.Errorf("err = %v, want ErrItemNotFound", err)
		}
	})

	t.Run("ConditionRefused", func(t *testing.T) {
		item := dynamodb.Item{"sensor": dynamodb.S("cond"), "at": dynamodb.N(1)}
		if _, err := client.PutItem(ctx, table, item); err != nil {
			t.Fatal(err)
		}
		_, err := client.PutItem(ctx, table, item,
			dynamodb.WithCondition("attribute_not_exists(sensor)"))
		if !errors.Is(err, dynamodb.ErrConditionalCheck) {
			t.Errorf("err = %v, want ErrConditionalCheck", err)
		}
	})

	t.Run("UpdateAndDelete", func(t *testing.T) {
		key := dynamodb.Key{"sensor": dynamodb.S("upd"), "at": dynamodb.N(1)}
		if _, err := client.PutItem(ctx, table, dynamodb.Item{
			"sensor": dynamodb.S("upd"), "at": dynamodb.N(1), "celsius": dynamodb.N(20),
		}); err != nil {
			t.Fatal(err)
		}
		result, err := client.UpdateItem(ctx, table, key, "SET celsius = :c",
			dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":c": dynamodb.N(22.5)}),
			dynamodb.WithReturnValues("ALL_NEW"))
		if err != nil {
			t.Fatalf("UpdateItem: %v", err)
		}
		if v, ok := result.Attributes["celsius"].AsFloat(); !ok || v != 22.5 {
			t.Errorf("ALL_NEW attributes = %v", result.Attributes)
		}
		if _, err := client.DeleteItem(ctx, table, key); err != nil {
			t.Fatalf("DeleteItem: %v", err)
		}
		if _, err := client.GetItem(ctx, table, key, dynamodb.WithConsistentRead(true)); !errors.Is(err, dynamodb.ErrItemNotFound) {
			t.Errorf("after delete: err = %v, want ErrItemNotFound", err)
		}
	})

	t.Run("BatchAndQueryPages", func(t *testing.T) {
		var writes []dynamodb.WriteRequest
		for i := 1; i <= 10; i++ {
			writes = append(writes, dynamodb.PutRequest(dynamodb.Item{
				"sensor": dynamodb.S("batch"), "at": dynamodb.N(i),
			}))
		}
		result, err := client.BatchWriteItem(ctx, map[string][]dynamodb.WriteRequest{table: writes})
		if err != nil {
			t.Fatalf("BatchWriteItem: %v", err)
		}
		for result.HasUnprocessed() {
			if result, err = client.BatchWriteItem(ctx, result.UnprocessedItems); err != nil {
				t.Fatalf("retry unprocessed: %v", err)
			}
		}

		got, err := client.BatchGetItem(ctx, map[string][]dynamodb.Key{
			table: {
				{"sensor": dynamodb.S("batch"), "at": dynamodb.N(1)},
				{"sensor": dynamodb.S("batch"), "at": dynamodb.N(2)},
			},
		}, dynamodb.WithConsistentRead(true))
		if err != nil {
			t.Fatalf("BatchGetItem: %v", err)
		}
		if len(got.Items[table]) != 2 {
			t.Errorf("batch get returned %d items, want 2", len(got.Items[table]))
		}

		// Three pages of four, which exercises the continuation key against a
		// real server rather than a fixture.
		var (
			seen      int
			pages     int
			startKey  dynamodb.Key
			ascending []int64
		)
		for {
			opts := []dynamodb.QueryOption{
				dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{
					":s": dynamodb.S("batch"),
				}),
				dynamodb.WithLimit(4),
			}
			if startKey != nil {
				opts = append(opts, dynamodb.WithExclusiveStartKey(startKey))
			}
			page, err := client.Query(ctx, table, "sensor = :s", opts...)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			pages++
			seen += len(page.Items)
			for _, item := range page.Items {
				n, _ := item["at"].AsInt()
				ascending = append(ascending, n)
			}
			if !page.HasMore() {
				break
			}
			startKey = page.LastEvaluatedKey
		}
		if seen != 10 || pages < 3 {
			t.Errorf("query saw %d items over %d pages, want 10 over at least 3", seen, pages)
		}
		for i := 1; i < len(ascending); i++ {
			if ascending[i] < ascending[i-1] {
				t.Errorf("sort key out of order: %v", ascending)
				break
			}
		}

		// Descending, which is the option most likely to be silently ignored.
		page, err := client.Query(ctx, table, "sensor = :s",
			dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":s": dynamodb.S("batch")}),
			dynamodb.WithScanForward(false), dynamodb.WithLimit(1))
		if err != nil {
			t.Fatalf("descending Query: %v", err)
		}
		if n, _ := page.Items[0]["at"].AsInt(); n != 10 {
			t.Errorf("descending query started at %d, want 10", n)
		}
	})

	t.Run("StructMapping", func(t *testing.T) {
		type sample struct {
			Sensor  string            `dynamodbav:"sensor"`
			At      int64             `dynamodbav:"at"`
			Celsius float64           `dynamodbav:"celsius"`
			Taken   time.Time         `dynamodbav:"taken"`
			Labels  map[string]string `dynamodbav:"labels"`
			Skipped string            `dynamodbav:"-"`
		}
		want := sample{
			Sensor: "struct", At: 1, Celsius: 21.5,
			Taken:  time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
			Labels: map[string]string{"room": "1"},
		}
		item, err := dynamodb.MarshalItem(want)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.PutItem(ctx, table, item); err != nil {
			t.Fatal(err)
		}
		got, err := client.GetItem(ctx, table, dynamodb.Key{
			"sensor": dynamodb.S("struct"), "at": dynamodb.N(1),
		}, dynamodb.WithConsistentRead(true))
		if err != nil {
			t.Fatal(err)
		}
		var back sample
		if err := dynamodb.UnmarshalItem(got, &back); err != nil {
			t.Fatal(err)
		}
		if !back.Taken.Equal(want.Taken) || back.Celsius != want.Celsius || back.Labels["room"] != "1" {
			t.Errorf("round trip = %+v, want %+v", back, want)
		}
	})

	t.Run("ValidationError", func(t *testing.T) {
		// A key that does not match the schema: the server answers with a real
		// error document, which is what the mapping is for.
		_, err := client.GetItem(ctx, table, dynamodb.Key{"wrong": dynamodb.S("key")})
		if !errors.Is(err, dynamodb.ErrValidation) {
			t.Fatalf("err = %v, want ErrValidation", err)
		}
		var e *dynamodb.Error
		if errors.As(err, &e) && e.Message == "" {
			t.Error("validation error carries no message")
		}
	})

	t.Run("UnknownTable", func(t *testing.T) {
		_, err := client.GetItem(ctx, "no-such-table-"+table, dynamodb.Key{
			"sensor": dynamodb.S("x"), "at": dynamodb.N(1),
		})
		if !errors.Is(err, dynamodb.ErrResourceNotFound) {
			t.Errorf("err = %v, want ErrResourceNotFound", err)
		}
	})

	t.Run("ListTables", func(t *testing.T) {
		list, err := client.ListTables(ctx)
		if err != nil {
			t.Fatalf("ListTables: %v", err)
		}
		found := false
		for _, name := range list.Names {
			if name == table {
				found = true
			}
		}
		if !found {
			t.Errorf("%s missing from %v", table, list.Names)
		}
	})
}

func createTable(t *testing.T, client *dynamodb.Client, table string) {
	t.Helper()
	ctx := context.Background()

	err := client.CreateTable(ctx, dynamodb.TableDefinition{
		Name:         table,
		PartitionKey: dynamodb.KeyAttribute{Name: "sensor", Type: dynamodb.TypeString},
		SortKey:      &dynamodb.KeyAttribute{Name: "at", Type: dynamodb.TypeNumber},
	})
	if err != nil && !errors.Is(err, dynamodb.ErrTableInUse) {
		t.Fatalf("CreateTable: %v", err)
	}

	for i := 0; i < 30; i++ {
		desc, err := client.DescribeTable(ctx, table)
		if err != nil {
			t.Fatalf("DescribeTable: %v", err)
		}
		if desc.Active() {
			if len(desc.Keys) != 2 {
				t.Errorf("key schema = %+v", desc.Keys)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("table %s did not become active", table)
}

// TestIntegrationRejectsBadCredentials runs only against a real AWS endpoint,
// which is the only server that checks a signature: DynamoDB Local accepts
// anything well-formed.
func TestIntegrationRejectsBadCredentials(t *testing.T) {
	endpoint := os.Getenv("DYNAMODB_TEST_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("set DYNAMODB_TEST_AWS_ENDPOINT to a real regional endpoint to run this")
	}
	client, err := dynamodb.New(
		dynamodb.WithEndpoint(endpoint),
		dynamodb.WithRegion(strings.Split(strings.TrimPrefix(endpoint, "https://dynamodb."), ".")[0]),
		dynamodb.WithCredentials(aws.Credentials{AccessKeyID: "AKIAnotreal", SecretAccessKey: "wrong"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.GetItem(context.Background(), "any", dynamodb.Key{"pk": dynamodb.S("x")})
	if !errors.Is(err, dynamodb.ErrBadCredentials) {
		t.Errorf("err = %v, want ErrBadCredentials", err)
	}
}

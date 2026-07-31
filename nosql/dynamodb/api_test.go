//go:build !tinygo

package dynamodb_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

func TestWriteRequestShapes(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"Attributes":{"pk":{"S":"u#1"},"score":{"N":"7"}}}`)
	})
	client := newClient(t, srv.URL)
	ctx := context.Background()
	key := dynamodb.Key{"pk": dynamodb.S("u#1")}

	result, err := client.PutItem(ctx, "users", dynamodb.Item{"pk": dynamodb.S("u#1")},
		dynamodb.WithCondition("attribute_not_exists(pk)"),
		dynamodb.WithReturnValues("ALL_OLD"))
	if err != nil {
		t.Fatalf("PutItem: %v", err)
	}
	if v, ok := result.Attributes["score"].AsInt(); !ok || v != 7 {
		t.Errorf("returned attributes = %v", result.Attributes)
	}

	if _, err := client.UpdateItem(ctx, "users", key, "SET #n = :name",
		dynamodb.WithExpressionNames(map[string]string{"#n": "name"}),
		dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":name": dynamodb.S("shibu")})); err != nil {
		t.Fatalf("UpdateItem: %v", err)
	}
	if _, err := client.DeleteItem(ctx, "users", key); err != nil {
		t.Fatalf("DeleteItem: %v", err)
	}

	reqs := srv.requests()
	if got := reqs[0].Body["ConditionExpression"]; got != "attribute_not_exists(pk)" {
		t.Errorf("ConditionExpression = %v", got)
	}
	if got := reqs[0].Body["ReturnValues"]; got != "ALL_OLD" {
		t.Errorf("ReturnValues = %v", got)
	}
	if _, ok := reqs[0].Body["Key"]; ok {
		t.Error("PutItem sent a Key member")
	}

	if got := reqs[1].Body["UpdateExpression"]; got != "SET #n = :name" {
		t.Errorf("UpdateExpression = %v", got)
	}
	names, _ := reqs[1].Body["ExpressionAttributeNames"].(map[string]any)
	if names["#n"] != "name" {
		t.Errorf("ExpressionAttributeNames = %v", reqs[1].Body["ExpressionAttributeNames"])
	}
	values, _ := reqs[1].Body["ExpressionAttributeValues"].(map[string]any)
	if values[":name"] == nil {
		t.Errorf("ExpressionAttributeValues = %v", reqs[1].Body["ExpressionAttributeValues"])
	}

	if reqs[2].Target != "DynamoDB_20120810.DeleteItem" {
		t.Errorf("delete target = %q", reqs[2].Target)
	}
	if _, ok := reqs[2].Body["Item"]; ok {
		t.Error("DeleteItem sent an Item member")
	}
}

func TestConditionalCheckFailedIsItsOwnError(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		exception(w, 400, "com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException",
			"The conditional request failed")
	})
	client := newClient(t, srv.URL)

	_, err := client.PutItem(context.Background(), "users", dynamodb.Item{"pk": dynamodb.S("u#1")},
		dynamodb.WithCondition("attribute_not_exists(pk)"))
	if !errors.Is(err, dynamodb.ErrConditionalCheck) {
		t.Fatalf("err = %v, want ErrConditionalCheck", err)
	}
	if got := len(srv.requests()); got != 1 {
		t.Errorf("sent %d requests: a refused condition is an answer, not a fault", got)
	}
}

func TestQueryPaginatesExplicitly(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			writeJSON(w, 200, `{"Items":[{"pk":{"S":"u#1"},"sk":{"N":"1"}}],
				"Count":1,"ScannedCount":1,
				"LastEvaluatedKey":{"pk":{"S":"u#1"},"sk":{"N":"1"}}}`)
			return
		}
		writeJSON(w, 200, `{"Items":[{"pk":{"S":"u#1"},"sk":{"N":"2"}}],"Count":1,"ScannedCount":1}`)
	})
	client := newClient(t, srv.URL)

	var items []dynamodb.Item
	var startKey dynamodb.Key
	for {
		opts := []dynamodb.QueryOption{
			dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":pk": dynamodb.S("u#1")}),
			dynamodb.WithLimit(1),
		}
		if startKey != nil {
			opts = append(opts, dynamodb.WithExclusiveStartKey(startKey))
		}
		page, err := client.Query(context.Background(), "events", "pk = :pk", opts...)
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		items = append(items, page.Items...)
		if !page.HasMore() {
			break
		}
		startKey = page.LastEvaluatedKey
	}

	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	reqs := srv.requests()
	if len(reqs) != 2 {
		t.Fatalf("sent %d requests, want 2", len(reqs))
	}
	if got := reqs[0].Body["KeyConditionExpression"]; got != "pk = :pk" {
		t.Errorf("KeyConditionExpression = %v", got)
	}
	if _, ok := reqs[0].Body["ExclusiveStartKey"]; ok {
		t.Error("the first page sent an ExclusiveStartKey")
	}
	if _, ok := reqs[1].Body["ExclusiveStartKey"]; !ok {
		t.Error("the second page did not carry the continuation key")
	}
}

func TestScanUsesIndexAndFilter(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"Items":[],"Count":0,"ScannedCount":9}`)
	})
	client := newClient(t, srv.URL)

	page, err := client.Scan(context.Background(), "events",
		dynamodb.WithIndex("by-city"),
		dynamodb.WithFilter("city = :c"),
		dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":c": dynamodb.S("東京")}))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// An empty page that scanned nine items is normal: the filter runs after
	// the read, so it drops items the call already paid for.
	if page.ScannedCount != 9 || len(page.Items) != 0 {
		t.Errorf("page = %+v", page)
	}
	if page.HasMore() {
		t.Error("HasMore on a page with no continuation key")
	}

	body := srv.requests()[0].Body
	if body["IndexName"] != "by-city" || body["FilterExpression"] != "city = :c" {
		t.Errorf("request = %v", body)
	}
}

func TestBatchGetItem(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{
			"Responses":{"users":[{"pk":{"S":"u#1"}}]},
			"UnprocessedKeys":{"users":{"Keys":[{"pk":{"S":"u#2"}}]}}}`)
	})
	client := newClient(t, srv.URL)

	result, err := client.BatchGetItem(context.Background(), map[string][]dynamodb.Key{
		"users": {{"pk": dynamodb.S("u#1")}, {"pk": dynamodb.S("u#2")}},
	}, dynamodb.WithConsistentRead(true))
	if err != nil {
		t.Fatalf("BatchGetItem: %v", err)
	}
	if len(result.Items["users"]) != 1 {
		t.Errorf("items = %v", result.Items)
	}
	if !result.HasUnprocessed() || len(result.UnprocessedKeys["users"]) != 1 {
		t.Errorf("unprocessed keys = %v", result.UnprocessedKeys)
	}

	request, _ := srv.requests()[0].Body["RequestItems"].(map[string]any)
	users, _ := request["users"].(map[string]any)
	if keys, _ := users["Keys"].([]any); len(keys) != 2 {
		t.Errorf("sent %v", users)
	}
	if users["ConsistentRead"] != true {
		t.Errorf("ConsistentRead = %v", users["ConsistentRead"])
	}
}

func TestBatchWriteItemShapeRoundTrips(t *testing.T) {
	// The unprocessed items come back in the same shape they were sent, which
	// is what makes them re-sendable without rebuilding.
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"UnprocessedItems":{"users":[
			{"PutRequest":{"Item":{"pk":{"S":"u#2"}}}},
			{"DeleteRequest":{"Key":{"pk":{"S":"u#3"}}}}]}}`)
	})
	client := newClient(t, srv.URL)

	result, err := client.BatchWriteItem(context.Background(), map[string][]dynamodb.WriteRequest{
		"users": {
			dynamodb.PutRequest(dynamodb.Item{"pk": dynamodb.S("u#1")}),
			dynamodb.DeleteRequest(dynamodb.Key{"pk": dynamodb.S("u#0")}),
		},
	})
	if err != nil {
		t.Fatalf("BatchWriteItem: %v", err)
	}
	if !result.HasUnprocessed() || len(result.UnprocessedItems["users"]) != 2 {
		t.Fatalf("unprocessed = %v", result.UnprocessedItems)
	}
	if result.UnprocessedItems["users"][0].Put == nil {
		t.Error("first unprocessed item is not a put")
	}
	if result.UnprocessedItems["users"][1].Delete == nil {
		t.Error("second unprocessed item is not a delete")
	}

	// Send them back, which is the documented way to finish a partial batch.
	if _, err := client.BatchWriteItem(context.Background(), result.UnprocessedItems); err != nil {
		t.Fatalf("resend: %v", err)
	}

	var sent struct {
		RequestItems map[string][]struct {
			Put *struct {
				Item map[string]json.RawMessage `json:"Item"`
			} `json:"PutRequest"`
			Delete *struct {
				Key map[string]json.RawMessage `json:"Key"`
			} `json:"DeleteRequest"`
		} `json:"RequestItems"`
	}
	if err := json.Unmarshal(srv.requests()[0].Raw, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	writes := sent.RequestItems["users"]
	if len(writes) != 2 || writes[0].Put == nil || writes[1].Delete == nil {
		t.Errorf("request shape = %s", srv.requests()[0].Raw)
	}
}

func TestCreateTableRequest(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"TableDescription":{"TableName":"events","TableStatus":"CREATING"}}`)
	})
	client := newClient(t, srv.URL)

	err := client.CreateTable(context.Background(), dynamodb.TableDefinition{
		Name:         "events",
		PartitionKey: dynamodb.KeyAttribute{Name: "pk", Type: dynamodb.TypeString},
		SortKey:      &dynamodb.KeyAttribute{Name: "sk", Type: dynamodb.TypeNumber},
		GlobalIndexes: []dynamodb.SecondaryIndex{{
			Name:         "by-city",
			PartitionKey: dynamodb.KeyAttribute{Name: "city", Type: dynamodb.TypeString},
			// The sort key repeats the table's, which must not produce a second
			// attribute definition.
			SortKey: &dynamodb.KeyAttribute{Name: "sk", Type: dynamodb.TypeNumber},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	var sent struct {
		TableName            string `json:"TableName"`
		BillingMode          string `json:"BillingMode"`
		AttributeDefinitions []struct {
			AttributeName string `json:"AttributeName"`
			AttributeType string `json:"AttributeType"`
		} `json:"AttributeDefinitions"`
		KeySchema []struct {
			AttributeName string `json:"AttributeName"`
			KeyType       string `json:"KeyType"`
		} `json:"KeySchema"`
		GlobalSecondaryIndexes []struct {
			IndexName  string `json:"IndexName"`
			Projection struct {
				ProjectionType string `json:"ProjectionType"`
			} `json:"Projection"`
		} `json:"GlobalSecondaryIndexes"`
		ProvisionedThroughput *struct{} `json:"ProvisionedThroughput"`
	}
	if err := json.Unmarshal(srv.requests()[0].Raw, &sent); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if sent.BillingMode != "PAY_PER_REQUEST" {
		t.Errorf("BillingMode = %q, want the on-demand default", sent.BillingMode)
	}
	if sent.ProvisionedThroughput != nil {
		t.Error("an on-demand table sent a throughput setting")
	}
	if len(sent.AttributeDefinitions) != 3 {
		t.Errorf("attribute definitions = %+v, want pk, sk and city once each", sent.AttributeDefinitions)
	}
	if len(sent.KeySchema) != 2 || sent.KeySchema[0].KeyType != "HASH" || sent.KeySchema[1].KeyType != "RANGE" {
		t.Errorf("key schema = %+v", sent.KeySchema)
	}
	if len(sent.GlobalSecondaryIndexes) != 1 || sent.GlobalSecondaryIndexes[0].Projection.ProjectionType != "ALL" {
		t.Errorf("global indexes = %+v", sent.GlobalSecondaryIndexes)
	}
}

func TestDescribeAndListTables(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		if n == 1 {
			// DescribeTable answers with Table; CreateTable answers with
			// TableDescription. Both spellings are decoded, and this fixture
			// is the one DescribeTable actually sends.
			writeJSON(w, 200, `{"Table":{"TableName":"events","TableStatus":"ACTIVE",
				"ItemCount":3,"TableSizeBytes":128,"CreationDateTime":1785000000.5,
				"KeySchema":[{"AttributeName":"pk","KeyType":"HASH"}]}}`)
			return
		}
		writeJSON(w, 200, `{"TableNames":["events","users"],"LastEvaluatedTableName":"users"}`)
	})
	client := newClient(t, srv.URL)

	desc, err := client.DescribeTable(context.Background(), "events")
	if err != nil {
		t.Fatalf("DescribeTable: %v", err)
	}
	if !desc.Active() || desc.ItemCount != 3 || desc.SizeBytes != 128 {
		t.Errorf("description = %+v", desc)
	}
	if desc.CreatedAt.IsZero() || desc.CreatedAt.Year() != 2026 {
		t.Errorf("CreatedAt = %v", desc.CreatedAt)
	}
	if len(desc.Keys) != 1 || desc.Keys[0].Name != "pk" {
		t.Errorf("keys = %+v", desc.Keys)
	}

	list, err := client.ListTables(context.Background(), dynamodb.WithLimit(2))
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}
	if !reflect.DeepEqual(list.Names, []string{"events", "users"}) {
		t.Errorf("names = %v", list.Names)
	}
	if list.LastEvaluatedName != "users" {
		t.Errorf("LastEvaluatedName = %q", list.LastEvaluatedName)
	}
	if got := srv.requests()[1].Body["Limit"]; got != float64(2) {
		t.Errorf("Limit = %v", got)
	}
}

func TestDeleteTable(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{}`)
	})
	client := newClient(t, srv.URL)

	if err := client.DeleteTable(context.Background(), "events"); err != nil {
		t.Fatalf("DeleteTable: %v", err)
	}
	if got := srv.requests()[0].Body["TableName"]; got != "events" {
		t.Errorf("TableName = %v", got)
	}
}

// TestStructMappingThroughTheClient checks the reflection path end to end, since
// MarshalItem is the entry point most application code will use.
func TestStructMappingThroughTheClient(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request, n int) {
		writeJSON(w, 200, `{"Item":{"pk":{"S":"u#1"},"age":{"N":"42"},"score":{"N":"0"},
			"active":{"BOOL":false},"tags":{"L":[]},"blob":{"B":""},
			"created":{"S":"2026-07-31T12:00:00Z"},"profile":{"M":{"city":{"S":"東京"}}},
			"optional":{"NULL":true},"labels":{"M":{}}}}`)
	})
	client := newClient(t, srv.URL)

	item, err := dynamodb.MarshalItem(user{PK: "u#1", Age: 42, Profile: profile{City: "東京"}})
	if err != nil {
		t.Fatalf("MarshalItem: %v", err)
	}
	if _, err := client.PutItem(context.Background(), "users", item); err != nil {
		t.Fatalf("PutItem: %v", err)
	}

	got, err := client.GetItem(context.Background(), "users", dynamodb.Key{"pk": dynamodb.S("u#1")})
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	var out user
	if err := dynamodb.UnmarshalItem(got, &out); err != nil {
		t.Fatalf("UnmarshalItem: %v", err)
	}
	if out.PK != "u#1" || out.Age != 42 || out.Profile.City != "東京" {
		t.Errorf("decoded = %+v", out)
	}
}

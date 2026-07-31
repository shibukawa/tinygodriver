// Command dynamodbdemo exercises the nosql/dynamodb client against DynamoDB or
// DynamoDB Local: it creates a table, writes items one by one and in a batch,
// reads them back, queries a partition, and cleans up after itself.
//
// One source, both compilers:
//
//	go run ./examples/dynamodbdemo
//	tinygo build -o dynamodbdemo ./examples/dynamodbdemo && ./dynamodbdemo
//
// Configure it through the usual AWS environment variables. Against a local
// server, where any well-formed credentials are accepted:
//
//	docker run -p 8000:8000 amazon/dynamodb-local -jar DynamoDBLocal.jar -inMemory -sharedDb
//	AWS_ENDPOINT_URL_DYNAMODB=http://127.0.0.1:8000 \
//	AWS_ACCESS_KEY_ID=local AWS_SECRET_ACCESS_KEY=local \
//	AWS_REGION=ap-northeast-1 ./dynamodbdemo
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/aws"
	_ "github.com/shibukawa/tinygodriver/netdev"
	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

// reading is what this demo stores: one sensor, many samples over time.
type reading struct {
	Sensor  string    `dynamodbav:"sensor"`
	At      int64     `dynamodbav:"at"`
	Celsius float64   `dynamodbav:"celsius"`
	Flags   []string  `dynamodbav:"flags,omitempty"`
	Taken   time.Time `dynamodbav:"taken"`
}

func main() {
	if err := run(); err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}

func run() error {
	table := env("DYNAMODB_TABLE", "tinygodriver-demo")

	client, err := dynamodb.New()
	if err != nil {
		return err
	}
	defer client.Close()
	fmt.Printf("backend=%s endpoint=%s region=%s\n\n", aws.Backend, client.Endpoint(), client.Region())

	ctx := context.Background()
	if err := ensureTable(ctx, client, table); err != nil {
		return err
	}
	defer func() {
		if err := client.DeleteTable(ctx, table); err != nil {
			fmt.Println("delete table:", err)
		} else {
			fmt.Println("deleted table", table)
		}
	}()

	now := time.Now().UTC().Truncate(time.Second)
	first := reading{Sensor: "room-1", At: now.Unix(), Celsius: 21.5, Taken: now}

	item, err := dynamodb.MarshalItem(first)
	if err != nil {
		return err
	}
	if _, err := client.PutItem(ctx, table, item); err != nil {
		return err
	}
	fmt.Printf("put   %s at %d\n", first.Sensor, first.At)

	// The same write again, refused this time: the condition is what makes a
	// put an insert.
	_, err = client.PutItem(ctx, table, item,
		dynamodb.WithCondition("attribute_not_exists(sensor)"))
	if !errors.Is(err, dynamodb.ErrConditionalCheck) {
		return fmt.Errorf("conditional put: %v, want ErrConditionalCheck", err)
	}
	fmt.Println("put   refused by its condition, as intended")

	// A batch pays one round trip for many items, which matters more than it
	// looks when every call is a network hop.
	var batch []dynamodb.WriteRequest
	for i := 1; i <= 5; i++ {
		later := now.Add(time.Duration(i) * time.Minute)
		next, err := dynamodb.MarshalItem(reading{
			Sensor: "room-1", At: later.Unix(), Celsius: 21.5 + float64(i)/10, Taken: later,
		})
		if err != nil {
			return err
		}
		batch = append(batch, dynamodb.PutRequest(next))
	}
	result, err := client.BatchWriteItem(ctx, map[string][]dynamodb.WriteRequest{table: batch})
	if err != nil {
		return err
	}
	if result.HasUnprocessed() {
		fmt.Printf("batch %d of %d written, %d unprocessed\n",
			len(batch)-len(result.UnprocessedItems[table]), len(batch),
			len(result.UnprocessedItems[table]))
	} else {
		fmt.Printf("batch %d items written\n", len(batch))
	}

	got, err := client.GetItem(ctx, table, dynamodb.Key{
		"sensor": dynamodb.S(first.Sensor),
		"at":     dynamodb.N(first.At),
	})
	if err != nil {
		return err
	}
	var back reading
	if err := dynamodb.UnmarshalItem(got, &back); err != nil {
		return err
	}
	fmt.Printf("get   %s %.1fC taken %s\n", back.Sensor, back.Celsius, back.Taken.Format(time.RFC3339))

	if _, err := client.GetItem(ctx, table, dynamodb.Key{
		"sensor": dynamodb.S("no-such-sensor"),
		"at":     dynamodb.N(0),
	}); !errors.Is(err, dynamodb.ErrItemNotFound) {
		return fmt.Errorf("missing item: %v, want ErrItemNotFound", err)
	}
	fmt.Println("get   a missing item reports ErrItemNotFound")

	// One page per call: the loop stays here rather than inside the client.
	var (
		count     int
		startKey  dynamodb.Key
		pageCount int
	)
	for {
		opts := []dynamodb.QueryOption{
			dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{
				":s": dynamodb.S("room-1"),
			}),
			dynamodb.WithLimit(2),
		}
		if startKey != nil {
			opts = append(opts, dynamodb.WithExclusiveStartKey(startKey))
		}
		page, err := client.Query(ctx, table, "sensor = :s", opts...)
		if err != nil {
			return err
		}
		count += len(page.Items)
		pageCount++
		if !page.HasMore() {
			break
		}
		startKey = page.LastEvaluatedKey
	}
	fmt.Printf("query %d items over %d pages\n", count, pageCount)

	if _, err := client.UpdateItem(ctx, table, dynamodb.Key{
		"sensor": dynamodb.S(first.Sensor),
		"at":     dynamodb.N(first.At),
	}, "SET celsius = :c",
		dynamodb.WithExpressionValues(map[string]dynamodb.AttributeValue{":c": dynamodb.N(22.0)}),
		dynamodb.WithReturnValues("ALL_NEW")); err != nil {
		return err
	}
	fmt.Println("update celsius set to 22.0")

	if _, err := client.DeleteItem(ctx, table, dynamodb.Key{
		"sensor": dynamodb.S(first.Sensor),
		"at":     dynamodb.N(first.At),
	}); err != nil {
		return err
	}
	fmt.Println("delete first reading")
	return nil
}

// ensureTable creates the demo table and waits for it, since a table is not
// usable the moment CreateTable returns.
func ensureTable(ctx context.Context, client *dynamodb.Client, table string) error {
	err := client.CreateTable(ctx, dynamodb.TableDefinition{
		Name:         table,
		PartitionKey: dynamodb.KeyAttribute{Name: "sensor", Type: dynamodb.TypeString},
		SortKey:      &dynamodb.KeyAttribute{Name: "at", Type: dynamodb.TypeNumber},
	})
	switch {
	case err == nil:
		fmt.Println("created table", table)
	case errors.Is(err, dynamodb.ErrTableInUse):
		fmt.Println("using existing table", table)
	default:
		return err
	}

	for i := 0; i < 30; i++ {
		desc, err := client.DescribeTable(ctx, table)
		if err != nil {
			return err
		}
		if desc.Active() {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("table did not become active")
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

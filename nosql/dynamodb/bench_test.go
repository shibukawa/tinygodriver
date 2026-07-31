//go:build !tinygo

package dynamodb_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/nosql/dynamodb"
)

// These compare the two ways to build an item, so the cost of the reflection
// path is a number rather than an assumption. Both end in json.Marshal, because
// that is what the client does with the item, and it is where the reflection
// this package does not control lives.

type benchReading struct {
	Sensor  string    `dynamodbav:"sensor"`
	At      int64     `dynamodbav:"at"`
	Celsius float64   `dynamodbav:"celsius"`
	Taken   time.Time `dynamodbav:"taken"`
	Note    string    `dynamodbav:"note"`
}

var benchSample = benchReading{
	Sensor: "room-1", At: 1785432762, Celsius: 21.5,
	Taken: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
	Note:  "nominal",
}

// benchSink keeps the compiler from eliminating the work.
var benchSink []byte

func BenchmarkBuildItemManual(b *testing.B) {
	for i := 0; i < b.N; i++ {
		item := dynamodb.Item{
			"sensor":  dynamodb.S(benchSample.Sensor),
			"at":      dynamodb.N(benchSample.At),
			"celsius": dynamodb.N(benchSample.Celsius),
			"taken":   dynamodb.S(benchSample.Taken.Format(time.RFC3339Nano)),
			"note":    dynamodb.S(benchSample.Note),
		}
		out, err := json.Marshal(item)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

func BenchmarkBuildItemReflect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		item, err := dynamodb.MarshalItem(benchSample)
		if err != nil {
			b.Fatal(err)
		}
		out, err := json.Marshal(item)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

// The two halves separately, so the reflection cost is visible against the
// json.Marshal it sits on top of.
func BenchmarkMarshalItemOnly(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := dynamodb.MarshalItem(benchSample); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONMarshalOnly(b *testing.B) {
	item, err := dynamodb.MarshalItem(benchSample)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := json.Marshal(item)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

var benchWire = []byte(`{"sensor":{"S":"room-1"},"at":{"N":"1785432762"},` +
	`"celsius":{"N":"21.5"},"taken":{"S":"2026-07-31T12:00:00Z"},"note":{"S":"nominal"}}`)

func BenchmarkReadItemManual(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var item dynamodb.Item
		if err := json.Unmarshal(benchWire, &item); err != nil {
			b.Fatal(err)
		}
		var out benchReading
		out.Sensor, _ = item["sensor"].AsString()
		out.At, _ = item["at"].AsInt()
		out.Celsius, _ = item["celsius"].AsFloat()
		out.Note, _ = item["note"].AsString()
		if text, ok := item["taken"].AsString(); ok {
			out.Taken, _ = time.Parse(time.RFC3339Nano, text)
		}
		if out.Sensor == "" {
			b.Fatal("decoded nothing")
		}
	}
}

func BenchmarkReadItemReflect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var item dynamodb.Item
		if err := json.Unmarshal(benchWire, &item); err != nil {
			b.Fatal(err)
		}
		var out benchReading
		if err := dynamodb.UnmarshalItem(item, &out); err != nil {
			b.Fatal(err)
		}
		if out.Sensor == "" {
			b.Fatal("decoded nothing")
		}
	}
}

//go:build !tinygo

package datastore_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

// These mirror the DynamoDB benchmarks: the same reading, built manually and
// through reflection, ending in json.Marshal because that is what the client
// does with an entity. The reflection path's cost is a number rather than an
// assumption.

type benchReading struct {
	Sensor  string    `datastore:"sensor"`
	At      int64     `datastore:"at"`
	Celsius float64   `datastore:"celsius"`
	Taken   time.Time `datastore:"taken"`
	Note    string    `datastore:"note"`
}

var benchSample = benchReading{
	Sensor: "room-1", At: 1785432762, Celsius: 21.5,
	Taken: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
	Note:  "nominal",
}

// benchSink keeps the compiler from eliminating the work.
var benchSink []byte

func BenchmarkBuildEntityManual(b *testing.B) {
	for i := 0; i < b.N; i++ {
		entity := datastore.Entity{Properties: map[string]datastore.Value{
			"sensor":  datastore.String(benchSample.Sensor),
			"at":      datastore.Int(benchSample.At),
			"celsius": datastore.Float(benchSample.Celsius),
			"taken":   datastore.Time(benchSample.Taken),
			"note":    datastore.String(benchSample.Note),
		}}
		out, err := json.Marshal(entity)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

func BenchmarkBuildEntityReflect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		entity, err := datastore.MarshalEntity(benchSample)
		if err != nil {
			b.Fatal(err)
		}
		out, err := json.Marshal(entity)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

// The two halves separately, so the reflection cost is visible against the
// json.Marshal it sits on top of.
func BenchmarkMarshalEntityOnly(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := datastore.MarshalEntity(benchSample); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJSONMarshalOnly(b *testing.B) {
	entity, err := datastore.MarshalEntity(benchSample)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, err := json.Marshal(entity)
		if err != nil {
			b.Fatal(err)
		}
		benchSink = out
	}
}

var benchWire = []byte(`{"properties":{` +
	`"sensor":{"stringValue":"room-1"},` +
	`"at":{"integerValue":"1785432762"},` +
	`"celsius":{"doubleValue":21.5},` +
	`"taken":{"timestampValue":"2026-07-31T12:00:00Z"},` +
	`"note":{"stringValue":"nominal"}}}`)

func BenchmarkReadEntityManual(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var entity datastore.Entity
		if err := json.Unmarshal(benchWire, &entity); err != nil {
			b.Fatal(err)
		}
		var out benchReading
		out.Sensor, _ = entity.Properties["sensor"].AsString()
		out.At, _ = entity.Properties["at"].AsInt()
		out.Celsius, _ = entity.Properties["celsius"].AsFloat()
		out.Note, _ = entity.Properties["note"].AsString()
		out.Taken, _ = entity.Properties["taken"].AsTime()
		if out.Sensor == "" {
			b.Fatal("decoded nothing")
		}
	}
}

func BenchmarkReadEntityReflect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		var entity datastore.Entity
		if err := json.Unmarshal(benchWire, &entity); err != nil {
			b.Fatal(err)
		}
		var out benchReading
		if err := datastore.UnmarshalEntity(entity, &out); err != nil {
			b.Fatal(err)
		}
		if out.Sensor == "" {
			b.Fatal("decoded nothing")
		}
	}
}

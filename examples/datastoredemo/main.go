// Command datastoredemo exercises nosql/datastore against the Datastore
// emulator, or against a real project when credentials are present.
//
//	gcloud beta emulators datastore start --host-port=127.0.0.1:8081
//	DATASTORE_EMULATOR_HOST=127.0.0.1:8081 DATASTORE_PROJECT_ID=demo \
//	    tinygo run ./examples/datastoredemo
//
// Against a real project the emulator variable is unset and
// GOOGLE_APPLICATION_CREDENTIALS names a service account key file. That path
// signs a self-signed JWT; the emulator ignores Authorization entirely, so a
// green run here does not exercise the token.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/shibukawa/tinygodriver/cloud/google"
	"github.com/shibukawa/tinygodriver/nosql/datastore"
)

const kind = "DemoTask"

func main() {
	if err := run(); err != nil {
		fmt.Println("FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("all steps passed")
}

func run() error {
	ctx := context.Background()

	project := google.ProjectIDFromEnv()
	client, err := datastore.New(project)
	if err != nil {
		return fmt.Errorf("New: %w", err)
	}
	defer client.Close()

	fmt.Printf("http backend  = %s\n", google.Backend)
	fmt.Printf("rsa backend   = %s\n", google.SignerBackend())
	fmt.Printf("project       = %s\n", client.ProjectID())
	fmt.Printf("endpoint      = %s\n\n", client.Endpoint())

	parent := datastore.NameKey("DemoAccount", "acme")

	// Write a spread of types, so an encoding mistake shows up as a mismatch
	// rather than as a plausible-looking value.
	first := parent.Child(datastore.PathElement{Kind: kind, Name: "first"})
	entity := datastore.NewEntity(first).
		Set("title", datastore.String("こんにちは, world")).
		Set("done", datastore.Bool(false)).
		Set("priority", datastore.Int(3)).
		Set("ratio", datastore.Float(0.5)).
		Set("big", datastore.IntString("9007199254740993")).
		Set("payload", datastore.Blob([]byte{0, 1, 2, 255})).
		Set("empty", datastore.String("")).
		Set("created", datastore.Time(time.Unix(1_800_000_000, 0).UTC())).
		Set("nothing", datastore.Null()).
		Set("tags", datastore.Array(datastore.String("a"), datastore.String("b"))).
		Set("meta", datastore.Nested(datastore.Entity{Properties: map[string]datastore.Value{
			"note": datastore.String("nested"),
		}}))

	if _, err := client.Put(ctx, entity); err != nil {
		return fmt.Errorf("Put: %w", err)
	}
	fmt.Println("put            ok")

	got, err := client.Get(ctx, first)
	if err != nil {
		return fmt.Errorf("Get: %w", err)
	}
	if err := checkRoundTrip(got); err != nil {
		return err
	}
	fmt.Printf("get            ok (version %d)\n", got.Version)

	// Insert on a key that exists is the closest thing this wire has to a
	// condition expression.
	if _, err := client.Insert(ctx, entity); !errors.Is(err, datastore.ErrAlreadyExists) {
		return fmt.Errorf("Insert on an existing key: got %v, want ErrAlreadyExists", err)
	}
	fmt.Println("insert guard   ok")

	// A second entity, so a query has more than one thing to find.
	second := parent.Child(datastore.PathElement{Kind: kind, Name: "second"})
	if _, err := client.Put(ctx, datastore.NewEntity(second).
		Set("title", datastore.String("second")).
		Set("done", datastore.Bool(false)).
		Set("priority", datastore.Int(1))); err != nil {
		return fmt.Errorf("Put second: %w", err)
	}

	// Ancestor query, paged one entity at a time so the cursor is exercised.
	query := datastore.NewQuery(kind).Ancestor(parent).Limit(1)
	seen := 0
	for {
		batch, err := client.Run(ctx, query)
		if err != nil {
			return fmt.Errorf("Run: %w", err)
		}
		seen += len(batch.Entities)
		if !batch.HasMore() || len(batch.Entities) == 0 {
			break
		}
		query = query.Start(batch.EndCursor)
	}
	if seen != 2 {
		return fmt.Errorf("paged query saw %d entities, want 2", seen)
	}
	fmt.Printf("paged query    ok (%d entities over 2 pages)\n", seen)

	// A read-modify-write, which needs a transaction because the predicate runs
	// in Go rather than on the server.
	err = client.RunInTransaction(ctx, func(tx *datastore.Tx) error {
		current, err := tx.Get(ctx, first)
		if err != nil {
			return err
		}
		priority, _ := current.Properties["priority"].AsInt()
		if priority != 3 {
			return fmt.Errorf("priority = %d, want 3", priority)
		}
		current.Properties["priority"] = datastore.Int(priority + 1)
		tx.Put(*current)
		return nil
	})
	if err != nil {
		return fmt.Errorf("RunInTransaction: %w", err)
	}
	after, err := client.Get(ctx, first)
	if err != nil {
		return err
	}
	if priority, _ := after.Properties["priority"].AsInt(); priority != 4 {
		return fmt.Errorf("after transaction priority = %d, want 4", priority)
	}
	fmt.Println("transaction    ok")

	// An incomplete key, completed by the server.
	allocated, err := client.Insert(ctx, datastore.NewEntity(datastore.IncompleteKey(kind)).
		Set("title", datastore.String("allocated")))
	if err != nil {
		return fmt.Errorf("Insert incomplete: %w", err)
	}
	if allocated.Incomplete() {
		return fmt.Errorf("server returned an incomplete key: %s", allocated)
	}
	fmt.Printf("allocated id   ok (%s)\n", allocated)

	if err := client.Delete(ctx, allocated); err != nil {
		return fmt.Errorf("Delete: %w", err)
	}
	// Deleting an absent key succeeds, which is what makes a delete replay-safe.
	if err := client.Delete(ctx, allocated); err != nil {
		return fmt.Errorf("second Delete should succeed: %w", err)
	}
	if _, err := client.Get(ctx, allocated); !errors.Is(err, datastore.ErrNoSuchEntity) {
		return fmt.Errorf("after delete: got %v, want ErrNoSuchEntity", err)
	}
	fmt.Println("delete         ok")

	for _, key := range []datastore.Key{first, second} {
		if err := client.Delete(ctx, key); err != nil {
			return fmt.Errorf("cleanup %s: %w", key, err)
		}
	}
	fmt.Println("cleanup        ok")
	return nil
}

func checkRoundTrip(e *datastore.Entity) error {
	if title, _ := e.Properties["title"].AsString(); title != "こんにちは, world" {
		return fmt.Errorf("multibyte string round trip: %q", title)
	}
	if s, ok := e.Properties["empty"].AsString(); !ok || s != "" {
		return fmt.Errorf("empty string round trip: %q, %v", s, ok)
	}
	if big, _ := e.Properties["big"].AsNumber(); big != "9007199254740993" {
		return fmt.Errorf("int64 beyond float64 precision: %q", big)
	}
	payload, _ := e.Properties["payload"].AsBytes()
	if len(payload) != 4 || payload[3] != 255 {
		return fmt.Errorf("blob round trip: %v", payload)
	}
	if !e.Properties["nothing"].IsNull() {
		return errors.New("null round trip lost the null")
	}
	if _, ok := e.Properties["absent"]; ok {
		return errors.New("an absent property came back present")
	}
	tags, _ := e.Properties["tags"].AsArray()
	if len(tags) != 2 {
		return fmt.Errorf("array round trip: %v", tags)
	}
	meta, ok := e.Properties["meta"].AsEntity()
	if !ok {
		return errors.New("nested entity round trip failed")
	}
	if note, _ := meta.Properties["note"].AsString(); note != "nested" {
		return fmt.Errorf("nested property: %q", note)
	}
	if ratio, _ := e.Properties["ratio"].AsFloat(); ratio != 0.5 {
		return fmt.Errorf("double round trip: %v", ratio)
	}
	return nil
}

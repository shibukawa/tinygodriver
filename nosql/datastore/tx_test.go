package datastore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTransactionStartsInTheReadAndCarriesTheHandle(t *testing.T) {
	// Two round trips, not three: the read starts the transaction and its reply
	// carries the handle the commit then uses.
	s := newStub(
		stubReply{200, `{"transaction":"dHgtMQ==","found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{"n":{"integerValue":"1"}}},"version":"4"}]}`},
		stubReply{200, `{"mutationResults":[{"version":"5"}]}`},
	)
	client, _ := newTestClient(t, s)

	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		entity, err := tx.Get(context.Background(), NameKey("K", "a"))
		if err != nil {
			return err
		}
		n, _ := entity.Properties["n"].AsInt()
		// The predicate runs here, in Go, between a read and a commit that
		// share a snapshot. That is what a condition expression would have done
		// on the server, and it is why a transaction is required rather than
		// optional.
		if n != 1 {
			return errors.New("unexpected value")
		}
		tx.Put(NewEntity(NameKey("K", "a")).Set("n", Int(n+1)))
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}

	ops := s.ops()
	want := []string{"lookup", "commit"}
	if strings.Join(ops, ",") != strings.Join(want, ",") {
		t.Fatalf("ops = %v, want %v", ops, want)
	}

	// The first read asks to start a transaction and names no handle, because
	// there is not one yet.
	lookup := s.calls()[0].Body["readOptions"].(map[string]any)
	if _, ok := lookup["newTransaction"]; !ok {
		t.Errorf("the read did not start a transaction: %v", lookup)
	}
	if _, ok := lookup["transaction"]; ok {
		t.Errorf("the read named a handle it could not have: %v", lookup)
	}
	commit := s.calls()[1].Body
	if commit["mode"] != "TRANSACTIONAL" {
		t.Errorf("mode = %v", commit["mode"])
	}
	if commit["transaction"] != "dHgtMQ==" {
		t.Errorf("commit did not carry the handle: %v", commit["transaction"])
	}
}

// TestTransactionQueuesUntilCommit is what makes a failing closure write
// nothing: the mutations never left the process.
func TestTransactionQueuesUntilCommit(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)

	sentinel := errors.New("caller changed their mind")
	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		tx.Put(NewEntity(NameKey("K", "a")))
		tx.Delete(NameKey("K", "b"))
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the closure's error", err)
	}
	// Nothing at all went out. The mutations were queued and the transaction
	// was never started, so there is not even a handle to roll back.
	if ops := s.ops(); len(ops) != 0 {
		t.Fatalf("ops = %v; a failed closure that never read must send nothing", ops)
	}
}

// TestAbortedRerunsTheClosure is the behaviour that separates this from a
// request-level retry: the reads have to happen again, not just the commit.
func TestAbortedRerunsTheClosure(t *testing.T) {
	s := newStub(
		stubReply{200, `{"transaction":"dHgtMQ==","found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}},"version":"1"}]}`},
		stubReply{409, errorBody("ABORTED", "contention")},
		stubReply{200, `{}`}, // rollback
		stubReply{200, `{"transaction":"dHgtMg==","found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}},"version":"2"}]}`},
		stubReply{200, `{"mutationResults":[{"version":"3"}]}`},
	)
	client, _ := newTestClient(t, s)

	runs, reads := 0, 0
	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		runs++
		if _, err := tx.Get(context.Background(), NameKey("K", "a")); err != nil {
			return err
		}
		reads++
		tx.Put(NewEntity(NameKey("K", "a")).Set("n", Int(runs)))
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}
	if runs != 2 {
		t.Errorf("closure ran %d times, want 2", runs)
	}
	if reads != 2 {
		t.Errorf("the closure read %d times; a re-run must read again, not just recommit", reads)
	}
}

func TestAbortedGivesUpAfterTheBudget(t *testing.T) {
	// A write-only closure folds its transaction into the commit, so an attempt
	// is one request. Two attempts, both aborted.
	s := newStub(
		stubReply{409, errorBody("ABORTED", "contention")},
		stubReply{409, errorBody("ABORTED", "contention")},
	)
	client, _ := newTestClient(t, s)

	runs := 0
	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		runs++
		tx.Put(NewEntity(NameKey("K", "a")))
		return nil
	}, WithTxRetries(2))
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if runs != 2 {
		t.Errorf("closure ran %d times, want 2", runs)
	}
}

func TestNonAbortedErrorDoesNotRerun(t *testing.T) {
	s := newStub(stubReply{400, errorBody("INVALID_ARGUMENT", "bad")})
	client, _ := newTestClient(t, s)

	runs := 0
	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		runs++
		tx.Put(NewEntity(NameKey("K", "a")))
		return nil
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v", err)
	}
	if runs != 1 {
		t.Errorf("closure ran %d times; only ABORTED re-runs", runs)
	}
}

func TestTxIsUnusableAfterTheClosure(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)

	var escaped *Tx
	if err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		escaped = tx
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.Get(context.Background(), NameKey("K", "a")); !errors.Is(err, ErrTxClosed) {
		t.Errorf("escaped Tx err = %v, want ErrTxClosed", err)
	}
}

func TestReadOnlyTransaction(t *testing.T) {
	s := newStub(
		stubReply{200, `{"transaction":"dHgtMQ==","found":[]}`},
		stubReply{200, `{}`},
	)
	client, _ := newTestClient(t, s)

	err := client.RunReadOnly(context.Background(), func(tx *Tx) error {
		_, err := tx.GetMulti(context.Background(), []Key{NameKey("K", "a")})
		return err
	})
	if err != nil {
		t.Fatalf("RunReadOnly: %v", err)
	}
	// The read starts the transaction, so the readOnly option rides on it.
	readOptions := s.calls()[0].Body["readOptions"].(map[string]any)
	started, ok := readOptions["newTransaction"].(map[string]any)
	if !ok {
		t.Fatalf("the read did not start a transaction: %v", readOptions)
	}
	if _, ok := started["readOnly"]; !ok {
		t.Errorf("newTransaction = %v, want readOnly", started)
	}
	if ops := s.ops(); strings.Join(ops, ",") != "lookup,commit" {
		t.Errorf("ops = %v, want two round trips", ops)
	}
}

func TestReadOnlyTransactionRefusesWrites(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)

	err := client.RunReadOnly(context.Background(), func(tx *Tx) error {
		tx.Put(NewEntity(NameKey("K", "a")))
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v, want a read-only refusal", err)
	}
	if ops := s.ops(); strings.Contains(strings.Join(ops, ","), "commit") {
		t.Errorf("ops = %v; a read-only transaction must not commit writes", ops)
	}
}

// TestTransactionRollsBackOnFailure keeps a started handle from holding locks
// until it times out. The handle now comes from a read's reply rather than from
// a begin this client issued, which is the part the fold changed.
func TestTransactionRollsBackOnFailure(t *testing.T) {
	s := newStub(
		stubReply{200, `{"transaction":"dHgtMQ==","found":[]}`},
		stubReply{200, `{}`},
	)
	client, _ := newTestClient(t, s)

	_ = client.RunInTransaction(context.Background(), func(tx *Tx) error {
		if _, err := tx.GetMulti(context.Background(), []Key{NameKey("K", "a")}); err != nil {
			return err
		}
		return errors.New("nope")
	})
	ops := s.ops()
	if strings.Join(ops, ",") != "lookup,rollback" {
		t.Fatalf("ops = %v, want a rollback of the handle the read started", ops)
	}
	if s.calls()[1].Body["transaction"] != "dHgtMQ==" {
		t.Errorf("rolled back the wrong handle: %v", s.calls()[1].Body)
	}
}

// TestNothingIsSentWhenNothingHappens: a closure that neither reads nor writes
// takes no handle, so there is none to release.
func TestNothingIsSentWhenNothingHappens(t *testing.T) {
	s := newStub()
	client, _ := newTestClient(t, s)

	if err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if ops := s.ops(); len(ops) != 0 {
		t.Errorf("ops = %v, want none", ops)
	}
}

// TestWriteOnlyTransactionFoldsIntoTheCommit is the other half of the fold: a
// closure with no reads is atomic in one round trip, where it used to take
// three.
func TestWriteOnlyTransactionFoldsIntoTheCommit(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"},{"version":"2"}]}`})
	client, _ := newTestClient(t, s)

	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		tx.Put(NewEntity(NameKey("K", "a")))
		tx.Delete(NameKey("K", "b"))
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}
	ops := s.ops()
	if strings.Join(ops, ",") != "commit" {
		t.Fatalf("ops = %v, want one round trip", ops)
	}
	body := s.calls()[0].Body
	if body["mode"] != "TRANSACTIONAL" {
		t.Errorf("mode = %v; the writes must still be atomic", body["mode"])
	}
	single, ok := body["singleUseTransaction"].(map[string]any)
	if !ok {
		t.Fatalf("no singleUseTransaction: %v", body)
	}
	if _, ok := single["readWrite"]; !ok {
		t.Errorf("singleUseTransaction = %v, want readWrite", single)
	}
	if _, ok := body["transaction"]; ok {
		t.Errorf("the commit named a handle it never took: %v", body)
	}
}

// TestSecondReadUsesTheHandleFromTheFirst: the fold does not restrict what a
// closure may do. A second read simply uses the handle the first one brought
// back, and the begin is still paid for once, inside a read.
func TestSecondReadUsesTheHandleFromTheFirst(t *testing.T) {
	s := newStub(
		stubReply{200, `{"transaction":"dHgtMQ==","found":[]}`},
		stubReply{200, `{"found":[]}`},
		stubReply{200, `{"mutationResults":[{"version":"1"}]}`},
	)
	client, _ := newTestClient(t, s)

	err := client.RunInTransaction(context.Background(), func(tx *Tx) error {
		ctx := context.Background()
		if _, err := tx.GetMulti(ctx, []Key{NameKey("K", "a")}); err != nil {
			return err
		}
		if _, err := tx.GetMulti(ctx, []Key{NameKey("K", "b")}); err != nil {
			return err
		}
		tx.Put(NewEntity(NameKey("K", "a")))
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}
	ops := s.ops()
	if strings.Join(ops, ",") != "lookup,lookup,commit" {
		t.Fatalf("ops = %v, want three round trips for two reads", ops)
	}
	second := s.calls()[1].Body["readOptions"].(map[string]any)
	if second["transaction"] != "dHgtMQ==" {
		t.Errorf("the second read did not use the handle: %v", second)
	}
	if _, ok := second["newTransaction"]; ok {
		t.Errorf("the second read tried to start another transaction: %v", second)
	}
}

func TestMutateSendsSeveralVerbsInOneCommit(t *testing.T) {
	s := newStub(stubReply{200, `{"mutationResults":[{"version":"1"},{"version":"2"},{"version":"3"}]}`})
	client, _ := newTestClient(t, s)

	result, err := client.Mutate(context.Background(), []Mutation{
		InsertOp(NewEntity(NameKey("K", "a"))),
		UpsertOp(NewEntity(NameKey("K", "b"))),
		DeleteOp(NameKey("K", "c")),
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if len(result.Keys) != 3 || len(result.Versions) != 3 {
		t.Fatalf("result = %+v", result)
	}
	mutations := s.calls()[0].Body["mutations"].([]any)
	if len(mutations) != 3 {
		t.Fatalf("%d mutations in one commit", len(mutations))
	}
	for i, verb := range []string{"insert", "upsert", "delete"} {
		if _, ok := mutations[i].(map[string]any)[verb]; !ok {
			t.Errorf("mutation %d is not a %s: %v", i, verb, mutations[i])
		}
	}
}

func TestAllocateIDs(t *testing.T) {
	s := newStub(stubReply{200, `{"keys":[{"path":[{"kind":"K","id":"7001"}]},{"path":[{"kind":"K","id":"7002"}]}]}`})
	client, _ := newTestClient(t, s)

	keys, err := client.AllocateIDs(context.Background(), []Key{IncompleteKey("K"), IncompleteKey("K")})
	if err != nil {
		t.Fatalf("AllocateIDs: %v", err)
	}
	if len(keys) != 2 || keys[0].Path[0].ID != 7001 {
		t.Errorf("keys = %v", keys)
	}

	// A complete key has nothing to allocate.
	if _, err := client.AllocateIDs(context.Background(), []Key{NameKey("K", "n")}); err == nil {
		t.Error("AllocateIDs accepted a complete key")
	}
}

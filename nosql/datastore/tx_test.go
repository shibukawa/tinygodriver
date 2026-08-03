package datastore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const beganTransaction = `{"transaction":"dHgtMQ=="}`

func TestTransactionBeginsCommitsAndCarriesTheHandle(t *testing.T) {
	s := newStub(
		stubReply{200, beganTransaction},
		stubReply{200, `{"found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{"n":{"integerValue":"1"}}},"version":"4"}]}`},
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
	want := []string{"beginTransaction", "lookup", "commit"}
	if strings.Join(ops, ",") != strings.Join(want, ",") {
		t.Fatalf("ops = %v, want %v", ops, want)
	}

	lookup := s.calls()[1].Body["readOptions"].(map[string]any)
	if lookup["transaction"] != "dHgtMQ==" {
		t.Errorf("read did not carry the handle: %v", lookup)
	}
	commit := s.calls()[2].Body
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
	s := newStub(
		stubReply{200, beganTransaction},
		stubReply{200, `{}`}, // rollback
	)
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
	ops := s.ops()
	if strings.Join(ops, ",") != "beginTransaction,rollback" {
		t.Fatalf("ops = %v; a failed closure must not commit", ops)
	}
}

// TestAbortedRerunsTheClosure is the behaviour that separates this from a
// request-level retry: the reads have to happen again, not just the commit.
func TestAbortedRerunsTheClosure(t *testing.T) {
	s := newStub(
		stubReply{200, beganTransaction},
		stubReply{200, `{"found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}},"version":"1"}]}`},
		stubReply{409, errorBody("ABORTED", "contention")},
		stubReply{200, `{}`}, // rollback
		stubReply{200, beganTransaction},
		stubReply{200, `{"found":[{"entity":{"key":{"path":[{"kind":"K","name":"a"}]},"properties":{}},"version":"2"}]}`},
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
	// Two full attempts: begin, commit-aborted, rollback, twice over.
	s := newStub(
		stubReply{200, beganTransaction},
		stubReply{409, errorBody("ABORTED", "contention")},
		stubReply{200, `{}`},
		stubReply{200, beganTransaction},
		stubReply{409, errorBody("ABORTED", "contention")},
		stubReply{200, `{}`},
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
	s := newStub(
		stubReply{200, beganTransaction},
		stubReply{400, errorBody("INVALID_ARGUMENT", "bad")},
		stubReply{200, `{}`},
	)
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
	s := newStub(stubReply{200, beganTransaction}, stubReply{200, `{}`})
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
		stubReply{200, beganTransaction},
		stubReply{200, `{"found":[]}`},
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
	begin := s.calls()[0].Body
	options, ok := begin["transactionOptions"].(map[string]any)
	if !ok {
		t.Fatalf("beginTransaction sent no options: %v", begin)
	}
	if _, ok := options["readOnly"]; !ok {
		t.Errorf("transactionOptions = %v, want readOnly", options)
	}
}

func TestReadOnlyTransactionRefusesWrites(t *testing.T) {
	s := newStub(stubReply{200, beganTransaction}, stubReply{200, `{}`})
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

// TestTransactionRollsBackOnFailure keeps a failed handle from holding locks
// until it times out.
func TestTransactionRollsBackOnFailure(t *testing.T) {
	s := newStub(stubReply{200, beganTransaction}, stubReply{200, `{}`})
	client, _ := newTestClient(t, s)

	_ = client.RunInTransaction(context.Background(), func(tx *Tx) error {
		return errors.New("nope")
	})
	if ops := s.ops(); len(ops) != 2 || ops[1] != "rollback" {
		t.Errorf("ops = %v, want a rollback", ops)
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

package integrationtest

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBindingProofLifecycle(t *testing.T) {
	var b Binding
	cmd, e := b.Create("owner", "saved-revision")
	if e != nil {
		t.Fatal(e)
	}
	code := strings.TrimPrefix(cmd, "/bind ")
	if !b.Pending("saved-revision") || b.Pending("other") {
		t.Fatal("identity fence")
	}
	called := 0
	commit := func(owner string) error {
		called++
		if owner != "owner" {
			t.Fatal("wrong owner")
		}
		return nil
	}
	if b.Consume(code, "other", commit) == nil || b.Consume("wrong", "saved-revision", commit) == nil || called != 0 {
		t.Fatal("invalid proof accepted")
	}
	if b.Consume(code, "saved-revision", func(string) error { return errors.New("rollback") }) == nil {
		t.Fatal("commit failure hidden")
	}
	if e = b.Consume(code, "saved-revision", commit); e != nil {
		t.Fatal(e)
	}
	if b.Consume(code, "saved-revision", commit) == nil || called != 1 {
		t.Fatal("replayed proof")
	}
	cmd, _ = b.Create("owner", "saved-revision")
	code = strings.TrimPrefix(cmd, "/bind ")
	b.expires = time.Now().Add(-time.Second)
	if b.Consume(code, "saved-revision", commit) == nil {
		t.Fatal("expired proof")
	}
	cmd, _ = b.Create("owner", "saved-revision")
	code = strings.TrimPrefix(cmd, "/bind ")
	b.Clear()
	if b.Consume(code, "saved-revision", commit) == nil {
		t.Fatal("cleared proof")
	}
	old, _ := b.Create("owner", "saved-revision")
	b.Create("owner", "saved-revision")
	if b.Consume(strings.TrimPrefix(old, "/bind "), "saved-revision", commit) == nil {
		t.Fatal("replaced proof")
	}
}

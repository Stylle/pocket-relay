package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSMBIntegration(t *testing.T) {
	if os.Getenv("POCKET_SMB_TEST") != "1" {
		t.Skip("isolated SMB fixture not enabled")
	}
	a := testApp(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := store{Kind: "smb", URL: "127.0.0.1", Root: "relay/pocket-test", User: "guest"}
	if e := a.storeProbe(ctx, s); e != nil {
		t.Fatal("SMB write/delete probe failed", e)
	}
	source := filepath.Join(a.dir, "tmp", "smb-source")
	if e := os.WriteFile(source, []byte("smb-loopback-ok"), 0600); e != nil {
		t.Fatal(e)
	}
	id := randomID()
	if e := a.put(ctx, s, id, source); e != nil {
		t.Fatal("SMB upload", e)
	}
	dest, e := os.CreateTemp(filepath.Join(a.dir, "tmp"), "smb-read-")
	if e != nil {
		t.Fatal(e)
	}
	defer dest.Close()
	if e = a.fetch(ctx, s, id, dest, 100); e != nil {
		t.Fatal("SMB download", e)
	}
	b, e := os.ReadFile(dest.Name())
	if e != nil || string(b) != "smb-loopback-ok" {
		t.Fatal("SMB round trip mismatch")
	}
	if e = a.remove(ctx, s, id); e != nil {
		t.Fatal("SMB delete", e)
	}
}

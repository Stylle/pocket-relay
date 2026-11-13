package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPortableBackupIncludesRemoteAttachments(t *testing.T) {
	a := testApp(t)
	token, uid, _ := register(t, a, "portable-admin")
	data := []byte("remote attachment fixture")
	hash := sha256.Sum256(data)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer remote.Close()
	st := store{ID: "remote", Kind: "webdav", Name: "Remote fixture", URL: remote.URL}
	encoded, _ := json.Marshal(st)
	_, e := a.db.Exec("INSERT INTO stores(id,value) VALUES(?,?)", st.ID, seal(a.key, string(encoded)))
	if e != nil {
		t.Fatal(e)
	}
	id := randomID()
	_, e = a.db.Exec("INSERT INTO files(id,user_id,store_id,name,mime,size,sha256) VALUES(?,?,?,?,?,?,?)", id, uid, st.ID, "test.txt", "text/plain", len(data), hex.EncodeToString(hash[:]))
	if e != nil {
		t.Fatal(e)
	}
	password := "portable-backup-password"
	response := req(a, "POST", "/api/admin/backup", token, map[string]string{"password": password})
	if response.Code != 200 {
		t.Fatalf("export status: %d", response.Code)
	}
	var plain bytes.Buffer
	if e = decryptBackup(&plain, bytes.NewReader(response.Body.Bytes()), password); e != nil {
		t.Fatal(e)
	}
	stage := t.TempDir()
	os.Mkdir(filepath.Join(stage, "blobs"), 0700)
	archive := filepath.Join(t.TempDir(), "backup.zip")
	os.WriteFile(archive, plain.Bytes(), 0600)
	if e = extractBackup(archive, stage); e != nil {
		t.Fatal(e)
	}
	if e = validateBackup(stage); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(stage, "blobs", id))
	if e != nil || !bytes.Equal(got, data) {
		t.Fatal("remote bytes were not bundled")
	}
	restored, e := openApp(stage)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.db.Close()
	var storeID string
	restored.db.QueryRow("SELECT store_id FROM files WHERE id=?", id).Scan(&storeID)
	if storeID != "local" {
		t.Fatal("restored attachment still depends on remote storage")
	}
	var originalStore string
	a.db.QueryRow("SELECT store_id FROM files WHERE id=?", id).Scan(&originalStore)
	if originalStore != "remote" {
		t.Fatal("export must not change live attachment location")
	}
	remote.Close()
	response = req(a, "POST", "/api/admin/backup", token, map[string]string{"password": password})
	if response.Code == 200 {
		t.Fatal("unavailable remote data must fail the complete backup")
	}
}

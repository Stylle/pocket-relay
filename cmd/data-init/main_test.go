//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRejectLinksAndBroadDirectories(t *testing.T) {
	for _, dir := range []string{"/", "/etc", "/home", "/tmp"} {
		if prepare(dir) == nil {
			t.Fatalf("accepted broad directory %s", dir)
		}
	}
	if os.Geteuid() != 0 {
		t.Skip("ownership repair requires root; container CI checks it separately")
	}
	dir, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(outside, "untouched")
	if err := os.WriteFile(file, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(file, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if prepare(dir) == nil {
		t.Fatal("accepted symlink")
	}
	info, _ := os.Stat(file)
	if info.Sys().(*syscall.Stat_t).Uid != 0 || info.Mode().Perm() != 0644 {
		t.Fatal("modified symlink target")
	}
	if err := os.Remove(filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if prepare(dir) == nil {
		t.Fatal("accepted hard link")
	}
}

func TestRepairDedicatedData(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "app.db")
	if err := os.WriteFile(file, []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := prepare(dir); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{dir, file} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != 10001 || stat.Gid != 10001 {
			t.Fatal("wrong owner")
		}
		want := os.FileMode(0600)
		if info.IsDir() {
			want = 0700
		}
		if info.Mode().Perm() != want {
			t.Fatal("wrong mode")
		}
	}
}

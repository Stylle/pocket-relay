//go:build linux

// Linux container bootstrap only. The HTTP service never runs as root.
package main

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func prepare(dir string) error {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	// Only use a dedicated application directory, never a system mount/root.
	if dir == "/" || strings.Count(filepath.Clean(dir), "/") < 2 && dir != "/data" {
		return fmt.Errorf("data must be a dedicated directory")
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("data must not be a symlink")
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	mounts, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	err = rejectNestedMounts(dir, mounts)
	mounts.Close()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	base := info.Sys().(*syscall.Stat_t).Dev
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("data contains a symlink; refusing ownership changes")
		}
		f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Dev != base || (!info.IsDir() && (!info.Mode().IsRegular() || stat.Nlink != 1)) {
			return fmt.Errorf("data contains a nested mount, special file or hard link; refusing ownership changes")
		}
		if stat.Uid != 10001 || stat.Gid != 10001 {
			if err = f.Chown(10001, 10001); err != nil {
				return fmt.Errorf("cannot initialize data ownership: %w", err)
			}
		}
		mode := fs.FileMode(0600)
		if info.IsDir() {
			mode = 0700
		}
		if info.Mode().Perm() != mode {
			if err = f.Chmod(mode); err != nil {
				return fmt.Errorf("cannot initialize data permissions: %w", err)
			}
		}
		return nil
	})
}

// Device numbers alone do not detect a bind mount from the same filesystem.
// Check the mount table before changing any ownership, including data itself.
func rejectNestedMounts(dir string, table io.Reader) error {
	scanner := bufio.NewScanner(table)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			return fmt.Errorf("invalid mount table")
		}
		point := filepath.Clean(unescape.Replace(fields[4]))
		if strings.HasPrefix(point, dir+"/") {
			return fmt.Errorf("data contains a nested mount; refusing ownership changes")
		}
	}
	return scanner.Err()
}

func main() {
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "/data"
	}
	if err := prepare(dir); err != nil {
		log.Fatal("Cannot prepare dedicated data directory. Check read-only mounts, NAS ACL or root-squash; do not use a shared/system folder: ", err)
	}
}

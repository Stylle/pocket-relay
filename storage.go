package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type store struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password,omitempty"`
	Domain   string `json:"domain"`
	Root     string `json:"root"`
}

func (a *app) getStore(id string) (store, error) {
	if id == "local" {
		return store{ID: id, Kind: "local", Name: "本地 data/blobs"}, nil
	}
	var enc string
	if e := a.db.QueryRow(`SELECT value FROM stores WHERE id=?`, id).Scan(&enc); e != nil {
		return store{}, e
	}
	p, e := unseal(a.key, enc)
	if e != nil {
		return store{}, e
	}
	var s store
	e = json.Unmarshal([]byte(p), &s)
	return s, e
}
func validateStore(s store) error {
	if len(s.Name) > 100 || len(s.Password) > 1024 || strings.ContainsAny(s.User+s.Domain+s.Root+s.URL+s.Password, "\r\n\x00") {
		return errors.New("存储参数不正确")
	}
	if s.Kind == "webdav" {
		u, e := url.Parse(s.URL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("WebDAV 地址必须为 http(s) URL，请单独填写账户密码")
		}
	}
	if s.Kind == "smb" {
		if s.URL == "" || strings.ContainsAny(s.URL, "/\\ @") || strings.HasPrefix(s.URL, "-") {
			return errors.New("SMB 主机格式错误，仅填写主机名或 IP")
		}
		if net.ParseIP(s.URL) == nil && strings.Contains(s.URL, ":") {
			return errors.New("SMB 默认连接 445 端口，请勿填写 smb:// 前缀")
		}
		if s.Root == "" {
			return errors.New("SMB 路径请填写 共享名/子目录")
		}
	}
	if s.Kind != "smb" && s.Kind != "webdav" {
		return errors.New("不支持的存储类型")
	}
	for _, part := range strings.Split(strings.ReplaceAll(s.Root, "\\", "/"), "/") {
		if part == ".." || part == "." {
			return errors.New("存储目录不能包含 . 或 ..")
		}
	}
	return nil
}

// Remote errors returned to the browser never contain credentials or URLs.
var davClient = &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}

func (a *app) dav(ctx context.Context, s store, method, key string, body io.Reader) (*http.Response, error) {
	u, e := url.Parse(strings.TrimRight(s.URL, "/") + "/")
	if e != nil {
		return nil, e
	}
	u.Path = path.Join(u.Path, s.Root, key)
	if key == "" {
		u.Path += "/"
	}
	r, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, e
	}
	r.SetBasicAuth(s.User, s.Password)
	if f, ok := body.(*os.File); ok {
		if info, e := f.Stat(); e == nil {
			r.ContentLength = info.Size()
		}
	}
	return davClient.Do(r)
}
func (a *app) remoteCommand(ctx context.Context, s store, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "rclone", append(args, "--config", "", "--contimeout", "10s", "--timeout", "60s", "--retries", "1", "--low-level-retries", "1", "--stats", "0")...)
	// rclone obscure is obfuscation, not encryption. The persisted credential is
	// AES-GCM encrypted by this app; only this short-lived child receives it.
	obscure := exec.CommandContext(ctx, "rclone", "obscure", "-")
	obscure.Stdin = strings.NewReader(s.Password)
	encoded, e := obscure.Output()
	if e != nil {
		encoded = nil
	}
	cmd.Env = append(os.Environ(), "RCLONE_CONFIG_PR_TYPE=smb", "RCLONE_CONFIG_PR_HOST="+s.URL, "RCLONE_CONFIG_PR_USER="+s.User, "RCLONE_CONFIG_PR_PASS="+strings.TrimSpace(string(encoded)), "RCLONE_CONFIG_PR_DOMAIN="+s.Domain)
	if os.Getenv("POCKET_SMB_TEST") == "1" {
		cmd.Stderr = os.Stderr
	}
	return cmd
}
func (a *app) put(ctx context.Context, s store, key, source string) error {
	switch s.Kind {
	case "local":
		return os.Rename(source, filepath.Join(a.dir, "blobs", key))
	case "webdav":
		if s.Root != "" {
			parts := strings.Split(strings.Trim(s.Root, "/"), "/")
			for i := range parts {
				prefix := s
				prefix.Root = strings.Join(parts[:i+1], "/")
				resp, e := a.dav(ctx, prefix, "MKCOL", "", nil)
				if e != nil {
					return e
				}
				resp.Body.Close()
				if resp.StatusCode != 201 && resp.StatusCode != 405 {
					return fmt.Errorf("WebDAV mkdir %d", resp.StatusCode)
				}
			}
		}
		f, e := os.Open(source)
		if e != nil {
			return e
		}
		defer f.Close()
		resp, e := a.dav(ctx, s, "PUT", key, f)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("WebDAV upload %d", resp.StatusCode)
		}
		return nil
	case "smb":
		return a.remoteCommand(ctx, s, "copyto", source, "pr:"+path.Join(s.Root, key)).Run()
	}
	return errors.New("unknown store")
}
func (a *app) remove(ctx context.Context, s store, key string) error {
	switch s.Kind {
	case "local":
		e := os.Remove(filepath.Join(a.dir, "blobs", key))
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return e
	case "webdav":
		resp, e := a.dav(ctx, s, "DELETE", key, nil)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		return errors.New("remote delete failed")
	case "smb":
		return a.remoteCommand(ctx, s, "deletefile", "pr:"+path.Join(s.Root, key)).Run()
	}
	return errors.New("unknown store")
}

// Download remotes to a bounded disk cache to support video Range requests.
// It is removed when the HTTP request finishes, never loaded wholesale in RAM.
func (a *app) fetch(ctx context.Context, s store, key string, dest *os.File, limit int64) error {
	var src io.ReadCloser
	if s.Kind == "webdav" {
		resp, e := a.dav(ctx, s, "GET", key, nil)
		if e != nil {
			return e
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return errors.New("remote unavailable")
		}
		src = resp.Body
	} else if s.Kind == "smb" {
		cmd := a.remoteCommand(ctx, s, "cat", "pr:"+path.Join(s.Root, key))
		cmd.Stdout = &boundedWriter{dest, limit}
		return cmd.Run()
	} else {
		return errors.New("not remote")
	}
	defer src.Close()
	n, e := io.Copy(dest, io.LimitReader(src, limit+1))
	if e == nil && n > limit {
		e = errors.New("remote object exceeds recorded size")
	}
	return e
}

type boundedWriter struct {
	w         io.Writer
	remaining int64
}

func (w *boundedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, errors.New("remote object exceeds recorded size")
	}
	n, e := w.w.Write(b)
	w.remaining -= int64(n)
	return n, e
}
func (a *app) storeProbe(ctx context.Context, s store) error {
	if e := validateStore(s); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Join(a.dir, "tmp"), "probe-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.WriteString("pocket-relay-storage-test"); e != nil {
		f.Close()
		return e
	}
	f.Close()
	key := "probe-" + randomID()
	if e = a.put(ctx, s, key, name); e != nil {
		return e
	}
	defer a.remove(ctx, s, key)
	readback, e := os.CreateTemp(filepath.Join(a.dir, "tmp"), "probe-read-")
	if e != nil {
		return e
	}
	defer os.Remove(readback.Name())
	defer readback.Close()
	if e = a.fetch(ctx, s, key, readback, int64(len("pocket-relay-storage-test"))); e != nil {
		return e
	}
	b, e := os.ReadFile(readback.Name())
	if e != nil || string(b) != "pocket-relay-storage-test" {
		return errors.New("storage readback mismatch")
	}
	return a.remove(ctx, s, key)
}

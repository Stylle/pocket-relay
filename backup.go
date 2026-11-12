package main

import (
	"archive/zip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const chunkSize = 1 << 20
const backupLimit = int64(20 << 30)

func backupCipher(password string, salt []byte) (cipher.AEAD, error) {
	key, e := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if e != nil {
		return nil, e
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func encryptBackup(out io.Writer, in io.Reader, password string) error {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return e
	}
	g, e := backupCipher(password, salt)
	if e != nil {
		return e
	}
	header := append([]byte("PRB1"), salt...)
	if _, e = out.Write(header); e != nil {
		return e
	}
	buf := make([]byte, chunkSize)
	var index uint64
	for {
		n, readErr := io.ReadFull(in, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return readErr
		}
		nonce := make([]byte, g.NonceSize())
		if _, e = rand.Read(nonce); e != nil {
			return e
		}
		aad := make([]byte, 28)
		copy(aad, header)
		binary.BigEndian.PutUint64(aad[20:], index)
		payload := g.Seal(nonce, nonce, buf[:n], aad)
		var size [4]byte
		binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
		if _, e = out.Write(size[:]); e != nil {
			return e
		}
		if _, e = out.Write(payload); e != nil {
			return e
		}
		index++
		if n == 0 {
			return nil
		}
	}
}
func decryptBackup(out io.Writer, in io.Reader, password string) error {
	header := make([]byte, 20)
	if _, e := io.ReadFull(in, header); e != nil {
		return e
	}
	if string(header[:4]) != "PRB1" {
		return errors.New("unsupported backup format")
	}
	g, e := backupCipher(password, header[4:])
	if e != nil {
		return e
	}
	var index uint64
	var total int64
	for {
		var size [4]byte
		if _, e = io.ReadFull(in, size[:]); e != nil {
			return e
		}
		n := binary.BigEndian.Uint32(size[:])
		if n < uint32(g.NonceSize()+g.Overhead()) || n > chunkSize+uint32(g.NonceSize()+g.Overhead()) {
			return errors.New("invalid backup record")
		}
		payload := make([]byte, n)
		if _, e = io.ReadFull(in, payload); e != nil {
			return e
		}
		aad := make([]byte, 28)
		copy(aad, header)
		binary.BigEndian.PutUint64(aad[20:], index)
		plain, e := g.Open(nil, payload[:g.NonceSize()], payload[g.NonceSize():], aad)
		if e != nil {
			return errors.New("wrong password or damaged backup")
		}
		index++
		if len(plain) == 0 {
			var extra [1]byte
			n, e := in.Read(extra[:])
			if n != 0 || e != io.EOF {
				return errors.New("trailing backup data")
			}
			return nil
		}
		total += int64(len(plain))
		if total > backupLimit {
			return errors.New("backup too large")
		}
		if _, e = out.Write(plain); e != nil {
			return e
		}
	}
}
func zipAdd(z *zip.Writer, name, source string) error {
	f, e := os.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	h.SetMode(0600)
	out, e := z.CreateHeader(h)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, f)
	return e
}
func validBackupPassword(p string) bool { return len(p) >= 12 && len(p) <= 256 }
func (a *app) exportBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validBackupPassword(in.Password) {
		fail(w, 400, "备份密码至少 12 个字符，请妥善保管")
		return
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	dir, e := os.MkdirTemp(filepath.Join(a.dir, "tmp"), "backup-")
	if e != nil {
		fail(w, 507, "磁盘无法写入")
		return
	}
	defer os.RemoveAll(dir)
	snapshot := filepath.Join(dir, "app.db")
	if _, e = a.db.Exec(`VACUUM INTO ?`, snapshot); e != nil {
		fail(w, 500, "创建数据库快照失败")
		return
	}
	// Website backups must not restore/revive previously revoked browser logins.
	copyDB, copyErr := sql.Open("sqlite", snapshot)
	if copyErr == nil {
		_, copyErr = copyDB.Exec(`DELETE FROM remembered_sessions`)
		if closeErr := copyDB.Close(); copyErr == nil {
			copyErr = closeErr
		}
	}
	if copyErr != nil {
		fail(w, 500, "清理备份登录状态失败")
		return
	}
	f, e := os.Create(filepath.Join(dir, "backup.zip"))
	if e != nil {
		fail(w, 507, "磁盘无法写入")
		return
	}
	defer f.Close()
	z := zip.NewWriter(f)
	if e = zipAdd(z, "app.db", snapshot); e == nil {
		e = zipAdd(z, "master.key", filepath.Join(a.dir, "master.key"))
	}
	if e == nil {
		var rows *sql.Rows
		rows, e = a.db.Query(`SELECT id FROM files WHERE store_id='local' AND deleted=0`)
		if e == nil {
			for rows.Next() {
				var id string
				if e = rows.Scan(&id); e != nil {
					break
				}
				if e = zipAdd(z, "blobs/"+id, filepath.Join(a.dir, "blobs", id)); e != nil {
					break
				}
			}
			if rows.Err() != nil {
				e = rows.Err()
			}
			rows.Close()
		}
	}
	if closeErr := z.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		fail(w, 500, "备份失败，部分本地文件缺失或磁盘空间不足")
		return
	}
	if _, e = f.Seek(0, io.SeekStart); e != nil {
		fail(w, 500, "读取备份失败")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pocket-relay-%s.prb"`, time.Now().Format("20060102-150405")))
	// A truncated stream is rejected on restore; no success-shaped partial ZIP.
	_ = encryptBackup(w, f, in.Password)
}

var objectID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func extractBackup(source, dest string) error {
	z, e := zip.OpenReader(source)
	if e != nil {
		return e
	}
	defer z.Close()
	if len(z.File) > 1000000 {
		return errors.New("too many files")
	}
	seen := map[string]bool{}
	var total int64
	for _, entry := range z.File {
		n := entry.Name
		if seen[n] || !entry.Mode().IsRegular() || (n != "app.db" && n != "master.key" && !(strings.HasPrefix(n, "blobs/") && objectID.MatchString(strings.TrimPrefix(n, "blobs/")))) {
			return errors.New("unexpected backup entry")
		}
		seen[n] = true
		if entry.UncompressedSize64 > uint64(backupLimit) {
			return errors.New("backup too large")
		}
		total += int64(entry.UncompressedSize64)
		if total > backupLimit {
			return errors.New("backup too large")
		}
		src, e := entry.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(filepath.Join(dest, filepath.FromSlash(n)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			src.Close()
			return e
		}
		_, e = io.Copy(out, io.LimitReader(src, int64(entry.UncompressedSize64)+1))
		closeErr := out.Close()
		src.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if !seen["app.db"] || !seen["master.key"] {
		return errors.New("incomplete backup")
	}
	return nil
}
func validateBackup(dir string) error {
	key, e := os.ReadFile(filepath.Join(dir, "master.key"))
	if e != nil || len(key) != 32 {
		return errors.New("invalid backup key")
	}
	db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "app.db"))+"?mode=ro")
	if e != nil {
		return e
	}
	defer db.Close()
	var integrity string
	if e = db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); e != nil || integrity != "ok" {
		return errors.New("damaged database")
	}
	var raw string
	if e = db.QueryRow(`SELECT value FROM config WHERE id=1`).Scan(&raw); e != nil {
		return e
	}
	var s settings
	if json.Unmarshal([]byte(raw), &s) != nil || !validateSettings(s) {
		return errors.New("invalid settings")
	}
	rows, e := db.Query(`SELECT secret FROM users UNION ALL SELECT value FROM stores`)
	if e != nil {
		return e
	}
	for rows.Next() {
		var encoded string
		if rows.Scan(&encoded) != nil {
			rows.Close()
			return errors.New("invalid secrets")
		}
		if _, e = unseal(key, encoded); e != nil {
			rows.Close()
			return e
		}
	}
	rows.Close()
	// Backups before Telegram support do not have this table.
	var hasTelegram int
	if e = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='telegram'`).Scan(&hasTelegram); e != nil {
		return e
	}
	if hasTelegram > 0 {
		var encoded string
		e = db.QueryRow(`SELECT value FROM telegram WHERE id=1`).Scan(&encoded)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			plain, err := unseal(key, encoded)
			if err != nil {
				return errors.New("invalid Telegram secret")
			}
			var cfg telegramConfig
			if json.Unmarshal([]byte(plain), &cfg) != nil {
				return errors.New("invalid Telegram configuration")
			}
		}
	}
	var admins int
	if e = db.QueryRow(`SELECT COUNT(*) FROM users WHERE admin=1 AND expires=0`).Scan(&admins); e != nil || admins != 1 {
		return errors.New("invalid administrator")
	}
	rows, e = db.Query(`SELECT id,size,sha256 FROM files WHERE store_id='local' AND deleted=0`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id, digest string
		var size int64
		if rows.Scan(&id, &size, &digest) != nil || !objectID.MatchString(id) {
			return errors.New("invalid file index")
		}
		f, e := os.Open(filepath.Join(dir, "blobs", id))
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, f)
		f.Close()
		if e != nil || n != size || fmt.Sprintf("%x", h.Sum(nil)) != digest {
			return errors.New("file integrity mismatch")
		}
	}
	return rows.Err()
}
func (a *app) restoreBackup(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.admin(w, r); !ok {
		return
	}
	password, decodeErr := url.PathUnescape(r.Header.Get("X-Backup-Password"))
	if decodeErr != nil {
		fail(w, 400, "备份密码格式不正确")
		return
	}
	if !validBackupPassword(password) {
		fail(w, 400, "请填写至少 12 位的备份密码")
		return
	}
	a.gate.Lock()
	defer a.gate.Unlock()
	r.Body = http.MaxBytesReader(w, r.Body, backupLimit+64<<20)
	dir, e := os.MkdirTemp(a.dir, "restore-")
	if e != nil {
		fail(w, 507, "磁盘空间不足")
		return
	}
	keep := false
	defer func() {
		if !keep {
			os.RemoveAll(dir)
		}
	}()
	os.Mkdir(filepath.Join(dir, "blobs"), 0700)
	f, e := os.OpenFile(filepath.Join(dir, "archive.zip"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		fail(w, 507, "磁盘无法写入")
		return
	}
	e = decryptBackup(f, r.Body, password)
	f.Close()
	if e != nil {
		fail(w, 400, "备份密码不正确、文件损坏或超过 20 GB 限制")
		return
	}
	if e = extractBackup(filepath.Join(dir, "archive.zip"), dir); e != nil {
		fail(w, 400, "备份结构无效")
		return
	}
	os.Remove(filepath.Join(dir, "archive.zip"))
	if e = validateBackup(dir); e != nil {
		fail(w, 400, "备份完整性验证失败，当前数据未修改")
		return
	}
	manifest, _ := json.Marshal(map[string]string{"stage": filepath.Base(dir), "previous": "previous-" + randomID()})
	if e = os.WriteFile(filepath.Join(a.dir, "restore-ready.json"), manifest, 0600); e != nil {
		fail(w, 507, "无法保存恢复计划")
		return
	}
	keep = true
	a.mu.Lock()
	a.maintenance = true
	a.mu.Unlock()
	reply(w, 200, map[string]string{"message": "备份校验完成，服务正在重启。请使用备份中的账户与验证器重新登录。"})
	close(a.restart)
}

// Idempotent restart-time installation: old data is retained, never overwritten.
// Re-running after a crash continues any moves whose source still exists.
func applyRestore(dir string) error {
	marker := filepath.Join(dir, "restore-ready.json")
	b, e := os.ReadFile(marker)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return errors.New("invalid restore marker")
	}
	stage, previous := m["stage"], m["previous"]
	if !strings.HasPrefix(stage, "restore-") || filepath.Base(stage) != stage || !strings.HasPrefix(previous, "previous-") || filepath.Base(previous) != previous {
		return errors.New("invalid restore path")
	}
	old := filepath.Join(dir, previous)
	if e = os.MkdirAll(old, 0700); e != nil {
		return e
	}
	for _, name := range []string{"app.db-wal", "app.db-shm"} {
		src := filepath.Join(dir, name)
		dst := filepath.Join(old, name)
		if _, e = os.Stat(dst); errors.Is(e, os.ErrNotExist) {
			if e = os.Rename(src, dst); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
	}
	for _, name := range []string{"app.db", "master.key", "blobs"} {
		src := filepath.Join(dir, stage, name)
		if _, e = os.Stat(src); errors.Is(e, os.ErrNotExist) {
			continue
		}
		current := filepath.Join(dir, name)
		dst := filepath.Join(old, name)
		if _, e = os.Stat(dst); errors.Is(e, os.ErrNotExist) {
			if e = os.Rename(current, dst); e != nil && !errors.Is(e, os.ErrNotExist) {
				return e
			}
		}
		if e = os.Rename(src, current); e != nil {
			return e
		}
	}
	if e = os.Remove(marker); e != nil {
		return e
	}
	_ = os.Remove(filepath.Join(dir, stage))
	return nil
}

package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxUploadSize = 32 << 30      // 单文件上限 32 GB
	uploadTempDir = ".lumina/tmp" // 相对 root
)

var uploadChunkPool = sync.Pool{
	New: func() any {
		return make([]byte, 4<<20)
	},
}

type uploadRow struct {
	SID     string
	Owner   string
	Name    string
	Size    int64
	Chunk   int64
	Total   int
	Bits    string
	MTime   int64
	Folder  string
	Sub     string
	Secret  int
	Dur     float64
	Created int64
}

func (s *Server) ensureUploadTable() error {
	_, err := s.st.db.Exec(`
CREATE TABLE IF NOT EXISTS uploads(
  sid     TEXT PRIMARY KEY,
  owner   TEXT NOT NULL,
  name    TEXT NOT NULL,
  size    INTEGER NOT NULL,
  chunk   INTEGER NOT NULL,
  total   INTEGER NOT NULL,
  bits    TEXT NOT NULL DEFAULT '',
  mtime   INTEGER NOT NULL DEFAULT 0,
  folder  TEXT NOT NULL DEFAULT '',
  sub     TEXT NOT NULL DEFAULT '',
  secret  INTEGER NOT NULL DEFAULT 0,
  created INTEGER NOT NULL
);`)
	_, _ = s.st.db.Exec(`ALTER TABLE uploads ADD COLUMN mtime INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.st.db.Exec(`ALTER TABLE uploads ADD COLUMN dur REAL NOT NULL DEFAULT 0`)
	_, _ = s.st.db.Exec(`ALTER TABLE uploads ADD COLUMN folder TEXT NOT NULL DEFAULT ''`)
	_, _ = s.st.db.Exec(`ALTER TABLE uploads ADD COLUMN sub TEXT NOT NULL DEFAULT ''`)
	_, _ = s.st.db.Exec(`ALTER TABLE uploads ADD COLUMN secret INTEGER NOT NULL DEFAULT 0`)
	return err
}

// ---------- bitmap ----------

func bitLen(total int) int { return (total + 7) / 8 }

func newBits(total int) string {
	return hex.EncodeToString(make([]byte, bitLen(total)))
}

func hasBit(bitsHex string, idx int) bool {
	b, err := hex.DecodeString(bitsHex)
	if err != nil || idx/8 >= len(b) {
		return false
	}
	return b[idx/8]&(1<<(uint(idx)%8)) != 0
}

func setBit(bitsHex string, idx int, total int) (string, int) {
	b, err := hex.DecodeString(bitsHex)
	if err != nil || len(b) < bitLen(total) {
		b = make([]byte, bitLen(total))
	}
	b[idx/8] |= 1 << (uint(idx) % 8)
	count := 0
	for _, v := range b {
		for v != 0 {
			count += int(v & 1)
			v >>= 1
		}
	}
	return hex.EncodeToString(b), count
}

func countBits(bitsHex string) int {
	b, err := hex.DecodeString(bitsHex)
	if err != nil {
		return 0
	}
	n := 0
	for _, v := range b {
		for v != 0 {
			n += int(v & 1)
			v >>= 1
		}
	}
	return n
}

// ---------- handlers ----------

func (s *Server) hUploadInit(w http.ResponseWriter, r *http.Request) {
	memberUploadMu.Lock()
	defer memberUploadMu.Unlock()
	var req struct {
		Owner  string  `json:"owner"`
		Name   string  `json:"name"`
		Size   int64   `json:"size"`
		MTime  int64   `json:"mtime"`
		Folder string  `json:"folder"`
		Sub    string  `json:"sub"`
		Secret bool    `json:"secret"`
		Dur    float64 `json:"dur"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if !canSee(userOf(r), req.Owner) {
		http.Error(w, "forbidden", 403)
		return
	}
	if !validOwner(req.Owner) {
		http.Error(w, "bad owner", 400)
		return
	}
	if req.Size <= 0 || req.Size > maxUploadSize {
		http.Error(w, "bad size", 400)
		return
	}
	chunk := s.cfg.UploadChunk
	if chunk < 256<<10 {
		chunk = 256 << 10
	}
	if chunk > 64<<20 {
		chunk = 64 << 20
	}
	total := int((req.Size + chunk - 1) / chunk)
	name := sanitizeName(req.Name)

	// ---------- 断点续传：同一个文件（owner+name+size）24 小时内复用旧会话 ----------
	var oldSID, oldBits string
	if err := s.st.db.QueryRow(
		`SELECT sid, bits FROM uploads WHERE owner=? AND name=? AND size=? AND mtime=? AND folder=? AND sub=? AND secret=? AND created > ? ORDER BY created DESC LIMIT 1`,
		req.Owner, name, req.Size, req.MTime, req.Folder, req.Sub, boolInt(req.Secret), time.Now().Add(-24*time.Hour).Unix()).Scan(&oldSID, &oldBits); err == nil {
		if _, statErr := os.Stat(filepath.Join(s.cfg.Root, uploadTempDir, oldSID+".part")); statErr == nil {
			old, e := s.loadUpload(oldSID)
			if e != nil {
				http.Error(w, "session error", 500)
				return
			}
			chunk = old.Chunk
			total = old.Total
			var got []int
			for i := 0; i < total; i++ {
				if hasBit(oldBits, i) {
					got = append(got, i)
				}
			}
			writeJSON(w, map[string]any{
				"sid": oldSID, "chunk": chunk, "total": total, "received": got, "resumed": len(got) > 0,
			})
			return
		}
	}

	if !s.quotaAllows(req.Owner, req.Size) {
		http.Error(w, "空间配额不足", 409)
		return
	}
	sid := randomHex(16)
	dir := filepath.Join(s.cfg.Root, uploadTempDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "mkdir failed", 500)
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, sid+".part"), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		http.Error(w, "create failed", 500)
		return
	}
	_ = f.Close()

	row := &uploadRow{SID: sid, Owner: req.Owner, Name: name, Size: req.Size,
		Chunk: chunk, Total: total, Bits: newBits(total), MTime: req.MTime, Dur: req.Dur, Folder: req.Folder, Sub: req.Sub, Created: time.Now().Unix()}
	if req.Secret {
		row.Secret = 1
	}
	if _, err := s.st.db.Exec(`INSERT INTO uploads(sid,owner,name,size,chunk,total,bits,mtime,folder,sub,secret,dur,created)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, row.SID, row.Owner, row.Name, row.Size, row.Chunk, row.Total, row.Bits, row.MTime, row.Folder, row.Sub, row.Secret, row.Dur, row.Created); err != nil {
		http.Error(w, "db failed", 500)
		return
	}
	writeJSON(w, map[string]any{"sid": sid, "chunk": chunk, "total": total, "received": []int{}})
}

func (s *Server) hUploadChunk(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	unlock := uploadLock(sid)
	defer unlock()
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil {
		http.Error(w, "bad idx", 400)
		return
	}
	row, err := s.loadUpload(sid)
	if err != nil {
		http.Error(w, "no session", 404)
		return
	}
	if !canSee(userOf(r), row.Owner) {
		http.Error(w, "forbidden", 403)
		return
	}
	if idx < 0 || idx >= row.Total {
		http.Error(w, "idx out of range", 400)
		return
	}
	var buf []byte
	if row.Chunk == 4<<20 {
		buf = uploadChunkPool.Get().([]byte)
		defer uploadChunkPool.Put(buf)
	} else {
		buf = make([]byte, int(row.Chunk))
	}
	n, err := io.ReadFull(r.Body, buf)

	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		http.Error(w, "read failed", 400)
		return
	}
	expected := row.Chunk
	if left := row.Size - int64(idx)*row.Chunk; left < expected {
		expected = left
	}
	if int64(n) != expected {
		http.Error(w, "incorrect chunk length", 400)
		return
	}
	if n == 0 {
		http.Error(w, "empty chunk", 400)
		return
	}
	part := filepath.Join(s.cfg.Root, uploadTempDir, sid+".part")
	f, err := os.OpenFile(part, os.O_WRONLY, 0o644)
	if err != nil {
		http.Error(w, "open part failed", 500)
		return
	}
	_, werr := f.WriteAt(buf[:n], int64(idx)*row.Chunk)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		http.Error(w, "write failed", 500)
		return
	}
	bits, cnt := setBit(row.Bits, idx, row.Total)
	_, _ = s.st.db.Exec(`UPDATE uploads SET bits=? WHERE sid=?`, bits, sid)
	writeJSON(w, map[string]any{"ok": true, "received": cnt, "total": row.Total})
}

func (s *Server) hUploadStatus(w http.ResponseWriter, r *http.Request) {
	unlock := uploadLock(r.PathValue("sid"))
	defer unlock()
	row, err := s.loadUpload(r.PathValue("sid"))
	if err != nil {
		http.Error(w, "no session", 404)
		return
	}
	if !canSee(userOf(r), row.Owner) {
		http.Error(w, "forbidden", 403)
		return
	}
	got := []int{}
	for i := 0; i < row.Total; i++ {
		if hasBit(row.Bits, i) {
			got = append(got, i)
		}
	}
	writeJSON(w, map[string]any{"sid": row.SID, "name": row.Name, "owner": row.Owner,
		"size": row.Size, "chunk": row.Chunk, "total": row.Total, "received": got})
}

func (s *Server) hUploadAbort(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	unlock := uploadLock(sid)
	defer unlock()
	row, e := s.loadUpload(sid)
	if e != nil || !canSee(userOf(r), row.Owner) {
		http.Error(w, "forbidden", 403)
		return
	}
	_ = os.Remove(filepath.Join(s.cfg.Root, uploadTempDir, sid+".part"))
	_, _ = s.st.db.Exec(`DELETE FROM uploads WHERE sid=?`, sid)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) hUploadComplete(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	unlock := uploadLock(sid)
	defer unlock()
	row, err := s.loadUpload(sid)
	if err != nil {
		http.Error(w, "no session", 404)
		return
	}
	if !canSee(userOf(r), row.Owner) {
		http.Error(w, "forbidden", 403)
		return
	}
	// 磁盘上的实际大小才是权威：只要大小对得上就不必依赖位图，
	// 客户端漏报/丢包补传都能自愈。
	if sizeOf(filepath.Join(s.cfg.Root, uploadTempDir, sid+".part")) != row.Size ||
		countBits(row.Bits) < row.Total {
		http.Error(w, "incomplete", 409)
		return
	}
	part := filepath.Join(s.cfg.Root, uploadTempDir, sid+".part")
	fi, err := os.Stat(part)
	if err != nil || fi.Size() != row.Size {
		http.Error(w, fmt.Sprintf("size mismatch: have %d want %d", sizeOf(part), row.Size), 409)
		return
	}

	id, dedup, err := s.finalizeUpload(row)
	if err == nil && id > 0 {
		// 记下这次用的「文件名+大小」：内容相同但名字不同的文件也能对上账
		_, _ = s.st.db.Exec(`INSERT OR IGNORE INTO asset_names(asset_id,name,size) VALUES(?,?,?)`,
			id, row.Name, row.Size)
	}
	// 如果去重到的那份资产在回收站里，把它恢复出来：
	// 否则 alias 记录挂在一个非 active 的资产上，对账依然认为没备份
	_, _ = s.st.db.Exec(`UPDATE assets SET status='active', missing=0, missing_since=0 WHERE id=? AND status<>'active'`, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if dedup {
		writeJSON(w, map[string]any{"ok": true, "dedup": true, "asset": id})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "dedup": false, "asset": id})
}

// finalizeUpload 把临时文件落盘、入库、清理会话。
func (s *Server) finalizeUpload(row *uploadRow) (int64, bool, error) {
	sid := row.SID
	part := filepath.Join(s.cfg.Root, uploadTempDir, sid+".part")

	sum, err := hashFile(part)
	if err != nil {
		return 0, false, fmt.Errorf("hash failed: %w", err)
	}
	if id, err := s.findByHash(row.Owner, row.Name, sum, row.Size); err == nil && id > 0 {
		_ = os.Remove(part)
		_, _ = s.st.db.Exec(`DELETE FROM uploads WHERE sid=?`, sid)
		// 秒传也要把文件夹分区补上，否则重传已存在的文件夹，分区里永远是空的
		if row.Folder != "" {
			_, _ = s.st.db.Exec(`UPDATE assets SET folder=?, sub=? WHERE id=?`, row.Folder, row.Sub, id)
		}
		// 秒传也要应用"备份到秘密相册"：否则重传已存在的文件永远进不了秘密相册
		// 手机端报上来的视频时长比路由器现场探测可靠
		if row.Dur > 0 {
			_, _ = s.st.db.Exec(`UPDATE assets SET duration=? WHERE id=? AND (duration IS NULL OR duration<=0)`, row.Dur, id)
		}
		if row.Secret == 1 {
			_, _ = s.st.db.Exec(`UPDATE assets SET secret=1 WHERE id=?`, id)
		}
		// 秒传时顺手修正拍摄时间：老版本客户端没传时间，库里记的是上传时间。
		// 只要这次带了真实文件时间，就把它写回去（幂等，重复执行无副作用）。
		if row.MTime > 0 {
			ft := time.Unix(row.MTime, 0)
			var curTaken int64
			var relPath string
			_ = s.st.db.QueryRow(`SELECT taken_at, rel_path FROM assets WHERE id=?`, id).Scan(&curTaken, &relPath)
			// 只有差异超过 1 天才改，避免来回抖动
			if diff := curTaken - row.MTime; diff > 86400 || diff < -86400 {
				_, _ = s.st.db.Exec(`UPDATE assets SET taken_at=?, year_month=?, mtime=? WHERE id=?`,
					row.MTime, ft.Format("2006-01"), row.MTime, id)
				if relPath != "" {
					_ = os.Chtimes(filepath.Join(s.cfg.Root, relPath), ft, ft)
				}
				log.Printf("dedup: fixed taken_at for asset %d -> %s", id, ft.Format("2006-01-02"))
			}
		}
		return id, true, nil
	}
	// 先把临时文件的 mtime 改成客户端上报的文件时间：
	// OpenNamed 在没有 EXIF 时用 mtime 兜底，不改就会被"上传时间"污染。
	if row.MTime > 0 {
		ft := time.Unix(row.MTime, 0)
		_ = os.Chtimes(part, ft, ft)
	}
	pr := OpenNamed(part, row.Name)
	// 优先 EXIF 拍摄时间，其次客户端上报的文件时间，最后才是磁盘时间。
	// 绝不能默认用"上传时间"，否则老照片会全堆到今天。
	t := pr.TakenAt
	if t.IsZero() && row.MTime > 0 {
		t = time.Unix(row.MTime, 0)
	}
	if t.IsZero() {
		if fi, err := os.Stat(part); err == nil {
			t = fi.ModTime()
		} else {
			t = time.Now()
		}
	}
	rel, err := s.archiveFile(row.Owner, row.Name, part, t, row.Folder, row.Sub)
	if err != nil {
		return 0, false, fmt.Errorf("archive failed: %w", err)
	}
	// 落盘后把文件修改时间改成拍摄时间，保证后续扫描与缓存键一致
	if dst := filepath.Join(s.cfg.Root, rel); dst != "" {
		_ = os.Chtimes(dst, t, t)
	}
	id, err := s.insertUploadedSecret(rel, sum, pr, t, row.Folder, row.Sub, row.Secret)
	if err != nil {
		return 0, false, fmt.Errorf("index failed: %w", err)
	}
	_, _ = s.st.db.Exec(`DELETE FROM uploads WHERE sid=?`, sid)
	go func(id int64) {
		select {
		case s.queue <- id:
		case <-time.After(30 * time.Second):
		}
	}(id)
	return id, false, nil
}

// ---------- helpers ----------

func (s *Server) loadUpload(sid string) (*uploadRow, error) {
	var u uploadRow
	err := s.st.db.QueryRow(`SELECT sid,owner,name,size,chunk,total,bits,mtime,folder,sub,secret,dur,created
		FROM uploads WHERE sid=?`, sid).Scan(&u.SID, &u.Owner, &u.Name, &u.Size, &u.Chunk, &u.Total, &u.Bits, &u.MTime, &u.Folder, &u.Sub, &u.Secret, &u.Dur, &u.Created)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Server) findByHash(owner, name, sum string, size int64) (int64, error) {
	var id int64
	var rel string
	// 只有「同名 + 同大小 + 同内容」才算已存在。
	// 早先只比内容，结果手机上不同名字的重复图片被合并成一条记录，
	// 备份对账永远对不上，文件夹里的数量也少一截。
	err := s.st.db.QueryRow(`SELECT id,rel_path FROM assets
		WHERE owner=? AND name=? AND sha256=? AND size=? AND status='active' LIMIT 1`,
		owner, name, sum, size).Scan(&id, &rel)
	if err != nil {
		return 0, err
	}
	// 库里有记录但文件已删除（例如手动删盘）时不能秒传，走正常入库流程重建。
	if _, statErr := os.Stat(filepath.Join(s.cfg.Root, rel)); statErr != nil {
		return 0, nil
	}
	return id, nil
}

func (s *Server) insertUploaded(rel, sum string, pr *Probe, t time.Time, folder, sub string) (int64, error) {
	return s.insertUploadedSecret(rel, sum, pr, t, folder, sub, 0)
}

func (s *Server) insertUploadedSecret(rel, sum string, pr *Probe, t time.Time, folder, sub string, secret int) (int64, error) {
	t = saneTime(t)
	abs := filepath.Join(s.cfg.Root, rel)
	fi, err := os.Stat(abs)
	if err != nil {
		return 0, err
	}
	ext := strings.ToLower(filepath.Ext(rel))
	kind := "photo"
	if videoExt[ext] {
		kind = "video"
	}
	owner := ownerOf(rel)
	a := &Asset{
		Owner: owner, Kind: kind, RelPath: rel, Name: filepath.Base(rel),
		Size: fi.Size(), MTime: fi.ModTime().Unix(), Mime: mimeOf(ext, kind),
		Sha256: sum, W: pr.W, H: pr.H, Orient: pr.Orientation,
		Camera: joinNonEmpty(pr.Make, pr.Model), Lens: pr.Lens,
		Aperture: pr.Aperture, ISO: pr.ISO, Focal: pr.Focal,
		Duration: pr.Duration, Lat: pr.Lat, Lon: pr.Lon, HasGPS: pr.HasGPS,
		TakenAt: t.Unix(), YearMonth: t.Format("2006-01"),
		Folder: folder, Sub: sub,
	}
	id, err := s.st.Upsert(a)
	if err == nil && secret == 1 && id > 0 {
		_, _ = s.st.db.Exec(`UPDATE assets SET secret=1 WHERE id=?`, id)
	}
	return id, err
}

func validOwner(o string) bool {
	for _, d := range ownerDefs() {
		if d.Owner == o {
			return true
		}
	}
	return false
}

func ownerOf(rel string) string {
	rel = filepath.ToSlash(rel)
	for _, d := range ownerDefs() {
		if strings.HasPrefix(rel, d.Dir+"/") {
			return d.Owner
		}
	}
	return "family"
}
func baseDirFor(owner string) string {
	if owner == "family" {
		return "家庭共享"
	}
	return filepath.Join(owner, "手机照片与视频备份")
}

// archiveFile 按拍摄时间归档到 base/YYYY年/YYYY-MM月/，返回相对路径。
// archiveFile 落盘。带 folder（用户选的原始文件夹）时保留原始目录结构，
// 只有没带 folder 的才按 年份/月份 归档。
func (s *Server) archiveFile(owner, name, tmp string, t time.Time, folder, sub string) (string, error) {
	var dir string
	if strings.TrimSpace(folder) != "" {
		clean := sanitizeName(strings.TrimSpace(folder))
		full := filepath.Join(s.cfg.Root, baseDirFor(owner), clean)
		if sub != "" {
			parts := strings.Split(sub, "/")
			for _, seg := range parts {
				seg = strings.TrimSpace(seg)
				if seg == "" || seg == "." || seg == ".." {
					continue
				}
				full = filepath.Join(full, sanitizeName(seg))
			}
		}
		dir = full
	} else {
		// 不再按 年/月 归档，直接落到用户的根目录
		dir = filepath.Join(s.cfg.Root, baseDirFor(owner))
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := uniquePath(dir, sanitizeName(name))
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return filepath.Rel(s.cfg.Root, dst)
}

func uniquePath(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		p := filepath.Join(dir, cand)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, time.Now().UnixNano(), ext))
}

var badName = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func sanitizeName(n string) string {
	n = filepath.Base(strings.TrimSpace(n))
	n = badName.ReplaceAllString(n, "_")
	n = strings.Trim(n, " .")
	if n == "" || n == "." || n == ".." {
		n = "upload_" + time.Now().Format("20060102_150405")
	}
	if len(n) > 160 {
		ext := filepath.Ext(n)
		keep := 160 - len(ext)
		if keep < 8 {
			keep = 8
		}
		n = n[:keep] + ext
	}
	return n
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, 1<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sizeOf(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// 清理超过 24 小时未完成的上传会话
func (s *Server) gcUploads() {
	cutoff := time.Now().Add(-24 * time.Hour).Unix()
	rows, err := s.st.db.Query(`SELECT sid FROM uploads WHERE created < ?`, cutoff)
	if err != nil {
		return
	}
	var sids []string
	for rows.Next() {
		var sid string
		if rows.Scan(&sid) == nil {
			sids = append(sids, sid)
		}
	}
	rows.Close()
	for _, sid := range sids {
		_ = os.Remove(filepath.Join(s.cfg.Root, uploadTempDir, sid+".part"))
		_, _ = s.st.db.Exec(`DELETE FROM uploads WHERE sid=?`, sid)
	}
	if len(sids) > 0 {
		log.Printf("gc uploads: %d sessions removed", len(sids))
	}
}

// sweepUploads 把"分片已到齐、但客户端没来得及调 complete"的会话补完，
// 避免用户看到"传了但没出现"。
func (s *Server) sweepUploads() {
	rows, err := s.st.db.Query(`SELECT sid, size FROM uploads WHERE created < ?`, time.Now().Add(-90*time.Second).Unix())
	if err != nil {
		return
	}
	type cand struct {
		sid  string
		size int64
	}
	var list []cand
	for rows.Next() {
		var c cand
		if rows.Scan(&c.sid, &c.size) == nil {
			list = append(list, c)
		}
	}
	rows.Close()

	for _, c := range list {
		func() {
			unlock := uploadLock(c.sid)
			defer unlock()
			part := filepath.Join(s.cfg.Root, uploadTempDir, c.sid+".part")
			fi, err := os.Stat(part)
			if err != nil || fi.Size() != c.size || c.size == 0 {
				return
			}
			row, err := s.loadUpload(c.sid)
			if err != nil || countBits(row.Bits) < row.Total {
				return
			}
			if _, _, err := s.finalizeUpload(row); err == nil {
				log.Printf("sweep: finalized stuck upload %s (%d bytes)", c.sid[:8], c.size)
			}
		}()
	}
}

var uploadLocks [64]sync.Mutex

func uploadLock(sid string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sid))
	m := &uploadLocks[h.Sum32()%64]
	m.Lock()
	return m.Unlock
}

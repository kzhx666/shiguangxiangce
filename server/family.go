package main

import (
	"log"
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) ensureFamilyTables() error {
	// 历史数据里共享条目的 folder 是空的，导致"共享→文件夹"只显示空列表，这里补一次
	_, _ = s.st.db.Exec(`UPDATE assets SET folder='家庭共享'
		WHERE owner='family' AND (folder IS NULL OR folder='')`)
	_, err := s.st.db.Exec(`
CREATE TABLE IF NOT EXISTS shares(
  token   TEXT PRIMARY KEY,
  asset   INTEGER NOT NULL,
  created INTEGER NOT NULL,
  expires INTEGER NOT NULL
);`)
	if err != nil {
		return err
	}
	// 兼容老库
	_, _ = s.st.db.Exec(`ALTER TABLE assets ADD COLUMN deleted_at INTEGER NOT NULL DEFAULT 0`)
	_, _ = s.st.db.Exec(`ALTER TABLE assets ADD COLUMN trash_path TEXT NOT NULL DEFAULT ''`)
	return nil
}

func (s *Server) trashDir() string { return filepath.Join(s.cfg.Root, ".lumina_trash") }

func decodeIDs(r *http.Request) ([]int64, error) {
	var req struct {
		IDs   []int64 `json:"ids"`
		Value int     `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		return nil, err
	}
	return req.IDs, nil
}

// ---------- 回收站 ----------

func (s *Server) moveToTrash(id int64) error {
	a, err := s.st.Get(id)
	if err != nil || a.Status != "active" {
		return fmt.Errorf("not found")
	}
	src := filepath.Join(s.cfg.Root, a.RelPath)
	if _, err := os.Stat(src); err != nil {
		// 文件本来就不在了（比如用户从硬盘删了）：直接把它移进回收站状态，
		// 否则这条记录会永远删不掉。
		_, e := s.st.db.Exec(`UPDATE assets SET status='trash', deleted_at=?, trash_path='' WHERE id=?`,
			time.Now().Unix(), id)
		s.dropAssetCache(a)
		return e
	}
	if err := os.MkdirAll(s.trashDir(), 0o755); err != nil {
		return err
	}
	base := fmt.Sprintf("%d_%s", id, sanitizeName(a.Name))
	dst := filepath.Join(s.trashDir(), base)
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	_, err = s.st.db.Exec(`UPDATE assets SET status='trash', deleted_at=?, trash_path=? WHERE id=?`,
		time.Now().Unix(), base, id)
	if err == nil {
		// 照片进回收站就把缩略图/海报缓存一起删掉，恢复时按需重新生成
		s.dropAssetCache(a)
	}
	return err
}

// dropAssetCache 删除某个资产的缩略图与海报缓存。
func (s *Server) dropAssetCache(a *Asset) {
	if a == nil {
		return
	}
	for _, size := range []string{"m", "t", "p"} {
		_ = os.Remove(s.thumbKey(a.ID, a.Size, a.MTime, size))
	}
	if pp := s.posterPath(a); pp != "" {
		_ = os.Remove(pp)
	}
}

func (s *Server) restoreFromTrash(id int64) error {
	var relPath, trashPath string
	err := s.st.db.QueryRow(`SELECT rel_path, trash_path FROM assets WHERE id=? AND status='trash'`, id).
		Scan(&relPath, &trashPath)
	if err != nil {
		return fmt.Errorf("not in trash")
	}
	src := filepath.Join(s.trashDir(), trashPath)
	if _, err := os.Stat(src); err != nil {
		return err
	}
	dst := filepath.Join(s.cfg.Root, relPath)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	dst = uniquePath(filepath.Dir(dst), filepath.Base(dst))
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	newRel, _ := filepath.Rel(s.cfg.Root, dst)
	_, err = s.st.db.Exec(`UPDATE assets SET status='active', deleted_at=0, trash_path='', rel_path=?, name=? WHERE id=?`,
		newRel, filepath.Base(dst), id)
	return err
}

func (s *Server) purgeAsset(id int64) error {
	var trashPath string
	var asz, amt int64
	// 必须在删行之前读出来，否则算不出缓存路径
	_ = s.st.db.QueryRow(`SELECT trash_path, size, mtime FROM assets WHERE id=?`, id).
		Scan(&trashPath, &asz, &amt)
	// 彻底删除也留一手：先挪到"已删暂存区"，7 天后才真正清掉。
	// 这样误点"彻底删除"还能救回来。
	if trashPath != "" {
		src := filepath.Join(s.trashDir(), trashPath)
		if _, err := os.Stat(src); err == nil {
			keep := filepath.Join(s.trashDir(), ".purged")
			_ = os.MkdirAll(keep, 0o755)
			dst := filepath.Join(keep, fmt.Sprintf("%d_%s", time.Now().Unix(), filepath.Base(trashPath)))
			if os.Rename(src, dst) != nil {
				_ = os.Remove(src)
			}
		}
	}
	_, err := s.st.db.Exec(`DELETE FROM assets WHERE id=?`, id)
	for _, size := range []string{"m", "t", "p"} {
		_ = os.Remove(s.thumbKey(id, asz, amt, size))
	}
	sub := fmt.Sprintf("%02d", int(((id%100)+100)%100))
	_ = os.Remove(filepath.Join(s.cfg.CacheDir, "poster", sub,
		fmt.Sprintf("%d_%d_%d.jpg", id, asz, amt)))
	return err
}

func (s *Server) hTrashList(w http.ResponseWriter, r *http.Request) {
	clause, oargs := ownerIn(ownerScope(userOf(r)))
	rows, err := s.st.db.Query(`SELECT id,name,size,deleted_at,rel_path,kind FROM assets
		WHERE status='trash'`+clause+` ORDER BY deleted_at DESC`, oargs...)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, size, del int64
		var name, rel, kind string
		if rows.Scan(&id, &name, &size, &del, &rel, &kind) != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "name": name, "size": size,
			"deleted_at": del, "rel_path": rel, "kind": kind,
			"purge_at": del + 30*86400})
	}
	writeJSON(w, map[string]any{"items": out})
}

func (s *Server) hTrash(w http.ResponseWriter, r *http.Request) {
	ids, err := decodeIDs(r)
	if err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	n := 0
	for _, id := range ids {
		if s.moveToTrash(id) == nil {
			n++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "moved": n})
}

func (s *Server) hRestore(w http.ResponseWriter, r *http.Request) {
	ids, err := decodeIDs(r)
	if err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	n := 0
	for _, id := range ids {
		if s.restoreFromTrash(id) == nil {
			n++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "restored": n})
}

func (s *Server) hPurge(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	if err := s.purgeAsset(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) gcTrash() {
	cutoff := time.Now().Add(-30 * 24 * time.Hour).Unix()
	rows, err := s.st.db.Query(`SELECT id FROM assets WHERE status='trash' AND deleted_at < ? AND deleted_at > 0`, cutoff)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = s.purgeAsset(id)
	}
}

// ---------- 收藏 ----------

func (s *Server) hFav(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs   []int64 `json:"ids"`
		Value int     `json:"value"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	for _, id := range req.IDs {
		_, _ = s.st.db.Exec(`UPDATE assets SET fav=? WHERE id=?`, req.Value, id)
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 推送到家庭共享（硬链接，不占额外空间）----------

func (s *Server) hPush(w http.ResponseWriter, r *http.Request) {
	ids, err := decodeIDs(r)
	if err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	pushed, linked := 0, 0
	for _, id := range ids {
		fw := ""
		if aa, e := s.st.Get(id); e == nil { fw = aa.Folder }
		if s.pushOne(id, fw) {
			pushed++
			if true { linked++ }
		}
	}
	writeJSON(w, map[string]any{"ok": true, "pushed": pushed, "hardlinked": linked})
	return

	// 下面的老循环保留做参考（已被 pushOne 取代）
	for _, id := range []int64{} {
		a, err := s.st.Get(id)
		if err != nil || a.Owner == "family" || a.Status != "active" {
			continue
		}
		src := filepath.Join(s.cfg.Root, a.RelPath)
		t := time.Unix(a.TakenAt, 0)
		dir := filepath.Join(s.cfg.Root, "家庭共享", t.Format("2006年"), t.Format("2006-01月"))
		if os.MkdirAll(dir, 0o755) != nil {
			continue
		}
		dst := uniquePath(dir, sanitizeName(a.Name))
		if os.Link(src, dst) == nil {
			linked++
		} else if copyFile(src, dst) == nil {
			// 跨设备时退化为复制
		} else {
			continue
		}
		rel, _ := filepath.Rel(s.cfg.Root, dst)
		fi, _ := os.Stat(dst)
		b := *a
		b.ID = 0
		b.Owner = "family"
		b.RelPath = rel
		b.Name = filepath.Base(dst)
		b.Size = fi.Size()
		b.MTime = fi.ModTime().Unix()
		if nid, err := s.st.Upsert(&b); err == nil {
			select {
			case s.queue <- nid:
			default:
			}
			pushed++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "pushed": pushed, "hardlinked": linked})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// ---------- 流式 ZIP ----------

func (s *Server) hZip(w http.ResponseWriter, r *http.Request) {
	ids, err := decodeIDs(r)
	if err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if len(ids) == 0 {
		http.Error(w, "empty", 400)
		return
	}
	name := fmt.Sprintf("lumina-%s.zip", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		"attachment; filename=\""+name+"\"; filename*=UTF-8''"+url.PathEscape(name))

	zw := zip.NewWriter(w)
	defer zw.Close()
	seen := map[string]int{}
	for _, id := range ids {
		a, err := s.st.Get(id)
		if err != nil || !canSee(userOf(r), a.Owner) {
			continue
		}
		src, err := os.Open(filepath.Join(s.cfg.Root, a.RelPath))
		if err != nil {
			continue
		}
		entry := sanitizeName(a.Name)
		if n := seen[entry]; n > 0 {
			ext := filepath.Ext(entry)
			entry = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(entry, ext), n+1, ext)
		}
		seen[sanitizeName(a.Name)]++
		fi, _ := src.Stat()
		hdr := &zip.FileHeader{Name: entry, Method: zip.Store, Modified: fi.ModTime()}
		fw, err := zw.CreateHeader(hdr)
		if err == nil {
			_, _ = io.Copy(fw, src)
		}
		src.Close()
	}
}

// ---------- 分享链接 ----------

func (s *Server) hShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID    int64 `json:"id"`
		Hours int   `json:"hours"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if req.Hours <= 0 {
		req.Hours = 24 * 7
	}
	if _, err := s.st.Get(req.ID); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	tok := randomHex(12)
	now := time.Now()
	_, err := s.st.db.Exec(`INSERT INTO shares(token,asset,created,expires) VALUES(?,?,?,?)`,
		tok, req.ID, now.Unix(), now.Add(time.Duration(req.Hours)*time.Hour).Unix())
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "token": tok, "url": "/s/" + tok,
		"expires": now.Add(time.Duration(req.Hours) * time.Hour).Unix()})
}

func (s *Server) hShared(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	var id int64
	var exp int64
	if err := s.st.db.QueryRow(`SELECT asset,expires FROM shares WHERE token=?`, tok).Scan(&id, &exp); err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if time.Now().Unix() > exp {
		_, _ = s.st.db.Exec(`DELETE FROM shares WHERE token=?`, tok)
		http.Error(w, "link expired", 410)
		return
	}
	a, err := s.st.Get(id)
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	f, err := os.Open(filepath.Join(s.cfg.Root, a.RelPath))
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", a.Mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, a.Name, fi.ModTime(), f)
}

// ---------- 清理已丢失文件 ----------

func (s *Server) hPrune(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.db.Query(`SELECT id, rel_path FROM assets`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type miss struct {
		id  int64
		rel string
	}
	var gone []miss
	for rows.Next() {
		var id int64
		var rel string
		if rows.Scan(&id, &rel) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.cfg.Root, rel)); err != nil {
			gone = append(gone, miss{id, rel})
		}
	}
	rows.Close()
	for _, g := range gone {
		_, _ = s.st.db.Exec(`DELETE FROM faces WHERE asset=?`, g.id)
		_, _ = s.st.db.Exec(`DELETE FROM assets WHERE id=?`, g.id)
		for _, size := range []string{"m", "t", "p"} {
			_ = os.Remove(s.thumbPath(g.id, size))
		}
	}
	writeJSON(w, map[string]any{"ok": true, "removed": len(gone)})
}
// gcPurged 真正清掉"已删暂存区"里超过 7 天的文件。
func (s *Server) gcPurged(maxAge time.Duration) {
	dir := filepath.Join(s.trashDir(), ".purged")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cut := time.Now().Add(-maxAge)
	n := 0
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || info.IsDir() {
			continue
		}
		if info.ModTime().Before(cut) {
			if os.Remove(filepath.Join(dir, e.Name())) == nil {
				n++
			}
		}
	}
	if n > 0 {
		log.Printf("gc purged: %d files older than 7d removed", n)
	}
}

// pushOne 把单张照片/视频推送到 家庭共享/年/月 下，返回是否成功。
func (s *Server) pushOne(id int64, folderName string) bool {
	a, err := s.st.Get(id)
	if err != nil || a.Owner == "family" || a.Status != "active" {
		return false
	}
	src := filepath.Join(s.cfg.Root, a.RelPath)
	t := time.Unix(a.TakenAt, 0)
	// 保留原文件夹名：家庭共享/<原文件夹>/<文件名>
	if folderName == "" {
		folderName = t.Format("2006-01月")
	}
	dir := filepath.Join(s.cfg.Root, "家庭共享", sanitizeName(folderName))
	if os.MkdirAll(dir, 0o755) != nil {
		return false
	}
	dst := uniquePath(dir, sanitizeName(a.Name))
	if os.Link(src, dst) != nil {
		if copyFile(src, dst) != nil {
			return false
		}
	}
	rel, _ := filepath.Rel(s.cfg.Root, dst)
	fi, _ := os.Stat(dst)
	b := *a
	b.ID = 0
	b.Owner = "family"
	b.Folder = folderName   // 共享里按原文件夹归类
	b.RelPath = rel
	b.Name = filepath.Base(dst)
	b.Size = fi.Size()
	b.MTime = fi.ModTime().Unix()
	nid, err := s.st.Upsert(&b)
	if err != nil {
		return false
	}
	select {
	case s.queue <- nid:
	default:
	}
	return true
}

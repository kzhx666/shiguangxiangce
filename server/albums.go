package main

import (
	"bytes"
	"image"
	_ "image/jpeg"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) ensureAlbumTables() error {
	_, err := s.st.db.Exec(`
CREATE TABLE IF NOT EXISTS albums(
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  name    TEXT NOT NULL,
  owner   TEXT NOT NULL DEFAULT '',
  created INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS album_items(
  album  INTEGER NOT NULL,
  asset  INTEGER NOT NULL,
  added  INTEGER NOT NULL,
  PRIMARY KEY(album, asset)
);
CREATE INDEX IF NOT EXISTS idx_album_items ON album_items(album);`)
	return err
}

func (s *Server) hAlbums(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.db.Query(`
		SELECT a.id, a.name, a.created, COUNT(i.asset), COALESCE(MIN(i.asset),0)
		FROM albums a LEFT JOIN album_items i ON i.album=a.id
		GROUP BY a.id ORDER BY a.created DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, created, cnt, cover int64
		var name string
		if rows.Scan(&id, &name, &created, &cnt, &cover) != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "name": name, "count": cnt, "cover": cover})
	}
	writeJSON(w, map[string]any{"albums": out})
}

func (s *Server) hAlbumCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string  `json:"name"`
		IDs   []int64 `json:"ids"`
		Owner string  `json:"owner"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name required", 400)
		return
	}
	res, err := s.st.db.Exec(`INSERT INTO albums(name,owner,created) VALUES(?,?,?)`,
		name, req.Owner, time.Now().Unix())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	id, _ := res.LastInsertId()
	for _, a := range req.IDs {
		_, _ = s.st.db.Exec(`INSERT OR IGNORE INTO album_items(album,asset,added) VALUES(?,?,?)`,
			id, a, time.Now().Unix())
	}
	writeJSON(w, map[string]any{"ok": true, "id": id, "added": len(req.IDs)})
}

func (s *Server) hAlbumAdd(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	n := 0
	for _, a := range req.IDs {
		if _, err := s.st.db.Exec(`INSERT OR IGNORE INTO album_items(album,asset,added) VALUES(?,?,?)`,
			id, a, time.Now().Unix()); err == nil {
			n++
		}
	}
	writeJSON(w, map[string]any{"ok": true, "added": n})
}

func (s *Server) hAlbumRemove(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	for _, a := range req.IDs {
		_, _ = s.st.db.Exec(`DELETE FROM album_items WHERE album=? AND asset=?`, id, a)
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) hAlbumRename(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	var req struct {
		Name string `json:"name"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name required", 400)
		return
	}
	_, err := s.st.db.Exec(`UPDATE albums SET name=? WHERE id=?`, name, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) hAlbumDelete(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	_, _ = s.st.db.Exec(`DELETE FROM album_items WHERE album=?`, id)
	_, _ = s.st.db.Exec(`DELETE FROM albums WHERE id=?`, id)
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 视频预览帧 ----------

const frameCount = 8

func (s *Server) framePath(id int64, n int) string {
	sub := fmt.Sprintf("%02d", int(((id%100)+100)%100))
	return filepath.Join(s.cfg.CacheDir, "frame", sub, fmt.Sprintf("%d_%d.jpg", id, n))
}

func (s *Server) ensureFrame(id int64, n int) (string, error) {
	dst := s.framePath(id, n)
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}
	fl := &s.flocks[((id*8 + int64(n)) % 64)]
	fl.Lock()
	unlock := func() { fl.Unlock() }
	defer unlock()
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}

	a, err := s.st.Get(id)
	if err != nil || a.Kind != "video" || a.Duration <= 0 {
		return "", fmt.Errorf("not a video")
	}
	src := filepath.Join(s.cfg.Root, a.RelPath)
	at := a.Duration * float64(n) / float64(frameCount)
	if at > a.Duration-0.1 {
		at = a.Duration * 0.5
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	// 宽度限制在 320，进度条上够用且生成很快
	ctx, cancel := timeoutCtx(20 * time.Second)
	defer cancel()
	args := []string{"-nostdin", "-v", "error", "-y", "-ss", fmt.Sprintf("%.3f", at), "-i", src,
		"-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "6", "-f", "mjpeg", dst}
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("ffmpeg: %s", truncate(string(out), 120))
	}
	return dst, nil
}

func (s *Server) hVideoFrame(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSuffix(r.PathValue("id"), ".jpg"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(r.PathValue("n"), ".jpg"))
	if n < 0 || n >= frameCount {
		http.Error(w, "bad frame", 400)
		return
	}
	p, err := s.ensureFrame(id, n)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.Error(w, "open", 500)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}

// ---------- 视频海报（由客户端上传时截帧）----------

func (s *Server) posterPath(a *Asset) string {
	if a == nil {
		return ""
	}
	sub := fmt.Sprintf("%02d", int(((a.ID%100)+100)%100))
	sub = filepath.Join(s.cfg.CacheDir, "poster", sub)
	_ = os.MkdirAll(sub, 0o755)
	return filepath.Join(sub, fmt.Sprintf("%d_%d_%d.jpg", a.ID, a.Size, a.MTime))
}

// hPoster 接收客户端截取的一帧作为视频封面。
func (s *Server) hPoster(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	a, err := s.st.Get(id)
	if err != nil || !canSee(userOf(r), a.Owner) {
		http.Error(w, "not found", 404)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil || len(body) < 512 {
		http.Error(w, "empty body", 400)
		return
	}
	pp := s.posterPath(a)
	if pp == "" {
		http.Error(w, "bad asset", 400)
		return
	}
	if err := os.WriteFile(pp, body, 0o644); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// 清掉旧的缩略图，下次请求会用新海报重新生成
	for _, size := range []string{"m", "t", "p"} {
		_ = os.Remove(s.thumbKey(a.ID, a.Size, a.MTime, size))
	}
	// 重置 nothumb 标记，允许缩略图重新生成
	_, _ = s.st.db.Exec(`UPDATE assets SET nothumb=0 WHERE id=?`, a.ID)
	if a.W <= 0 || a.H <= 0 {
		if cfg, _, err := image.DecodeConfig(bytes.NewReader(body)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
			a.W, a.H = cfg.Width, cfg.Height
			_, _ = s.st.db.Exec(`UPDATE assets SET w=?, h=? WHERE id=?`, cfg.Width, cfg.Height, a.ID)
		}
	}
	writeJSON(w, map[string]any{"ok": true, "bytes": len(body)})
}
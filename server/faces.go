package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) ensureFaceTables() {
	stmts := []string{
		`ALTER TABLE assets ADD COLUMN face_done INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS faces(
		   id INTEGER PRIMARY KEY AUTOINCREMENT,
		   asset INTEGER NOT NULL,
		   x REAL NOT NULL DEFAULT 0, y REAL NOT NULL DEFAULT 0,
		   w REAL NOT NULL DEFAULT 0, h REAL NOT NULL DEFAULT 0,
		   emb BLOB NOT NULL,
		   person INTEGER NOT NULL DEFAULT 0,
		   created INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS people(
		   id INTEGER PRIMARY KEY AUTOINCREMENT,
		   name TEXT NOT NULL DEFAULT '',
		   cover INTEGER NOT NULL DEFAULT 0,
		   cnt INTEGER NOT NULL DEFAULT 0,
		   created INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_faces_asset ON faces(asset)`,
		`CREATE INDEX IF NOT EXISTS idx_faces_person ON faces(person)`,
	}
	for _, q := range stmts {
		_, _ = s.st.db.Exec(q)
	}
}

func vecToBytes(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func bytesToVec(b []byte) []float32 {
	if len(b)%4 != 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func normVec(v []float32) []float32 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	if sum == 0 {
		return v
	}
	n := float32(math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = f / n
	}
	return out
}

func dot(a, b []float32) float32 {
	if len(a) != len(b) {
		return -1
	}
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// ---------- 待处理队列 ----------

func (s *Server) hFacesPending(w http.ResponseWriter, r *http.Request) {
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	rows, err := s.st.db.Query(`SELECT id FROM assets
		WHERE status='active' AND kind='photo' AND face_done=0 ORDER BY taken_at DESC LIMIT ?`, limit)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	var remain int
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE status='active' AND kind='photo' AND face_done=0`).Scan(&remain)
	writeJSON(w, map[string]any{"ids": ids, "remaining": remain})
}

// ---------- 回传检测结果 ----------

func (s *Server) hFacesSubmit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	var req struct {
		Faces []struct {
			X   float64   `json:"x"`
			Y   float64   `json:"y"`
			W   float64   `json:"w"`
			H   float64   `json:"h"`
			Emb []float32 `json:"emb"`
		} `json:"faces"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	tx, err := s.st.db.Begin()
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	_, _ = tx.Exec(`DELETE FROM faces WHERE asset=?`, id)
	now := time.Now().Unix()
	n := 0
	for _, f := range req.Faces {
		if len(f.Emb) < 16 {
			continue
		}
		v := normVec(f.Emb)
		if _, err := tx.Exec(`INSERT INTO faces(asset,x,y,w,h,emb,created) VALUES(?,?,?,?,?,?,?)`,
			id, f.X, f.Y, f.W, f.H, vecToBytes(v), now); err != nil {
			continue
		}
		n++
	}
	_, _ = tx.Exec(`UPDATE assets SET face_done=1 WHERE id=?`, id)
	_ = tx.Commit()
	writeJSON(w, map[string]any{"ok": true, "faces": n})
}

// ---------- 聚类 ----------

const faceSimThreshold = 0.50 // 余弦相似度阈值

func (s *Server) rebuildPeople() int {
	rows, err := s.st.db.Query(`SELECT id, asset, emb FROM faces ORDER BY id`)
	if err != nil {
		return 0
	}
	type rec struct {
		id    int64
		asset int64
		v     []float32
	}
	var list []rec
	for rows.Next() {
		var id, asset int64
		var blob []byte
		if rows.Scan(&id, &asset, &blob) == nil {
			if v := bytesToVec(blob); len(v) > 0 {
				list = append(list, rec{id, asset, v})
			}
		}
	}
	rows.Close()

	type cluster struct {
		cent  []float32
		faces []int64
		cover int64
	}
	var cl []*cluster
	for _, f := range list {
		best := -1
		bestSim := float32(-1)
		for i, c := range cl {
			sim := dot(c.cent, f.v)
			if sim > bestSim {
				bestSim, best = sim, i
			}
		}
		if best >= 0 && bestSim >= faceSimThreshold {
			c := cl[best]
			for i := range c.cent {
				c.cent[i] = (c.cent[i]*float32(len(c.faces)) + f.v[i]) / float32(len(c.faces)+1)
			}
			c.faces = append(c.faces, f.id)
		} else {
			cl = append(cl, &cluster{cent: append([]float32(nil), f.v...), faces: []int64{f.id}, cover: f.id})
		}
	}

	tx, _ := s.st.db.Begin()
	_, _ = tx.Exec(`DELETE FROM people`)
	_, _ = tx.Exec(`UPDATE faces SET person=0`)
	kept := 0
	for _, c := range cl {
		if len(c.faces) < 2 {
			continue // 单张人脸的聚类意义不大，先不入库
		}
		kept++
		res, err := tx.Exec(`INSERT INTO people(cover,cnt,created) VALUES(?,?,?)`, c.cover, len(c.faces), time.Now().Unix())
		if err != nil {
			continue
		}
		pid, _ := res.LastInsertId()
		for _, fid := range c.faces {
			_, _ = tx.Exec(`UPDATE faces SET person=? WHERE id=?`, pid, fid)
		}
	}
	_ = tx.Commit()
	return kept
}

func (s *Server) hPeople(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.db.Query(`SELECT id, name, cover, cnt FROM people ORDER BY cnt DESC LIMIT 100`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, cover, cnt int64
		var name string
		if rows.Scan(&id, &name, &cover, &cnt) != nil {
			continue
		}
		out = append(out, map[string]any{"id": id, "name": name, "cover": cover, "count": cnt})
	}
	var total int
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE face_done=1`).Scan(&total)
	writeJSON(w, map[string]any{"people": out, "scanned": total})
}

func (s *Server) hPeopleRebuild(w http.ResponseWriter, r *http.Request) {
	n := s.rebuildPeople()
	writeJSON(w, map[string]any{"ok": true, "people": n})
}

func (s *Server) hPeopleName(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Name string `json:"name"`
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	_, err := s.st.db.Exec(`UPDATE people SET name=? WHERE id=?`, strings.TrimSpace(req.Name), id)
	if err != nil {
		http.Error(w, "db", 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ---------- 人脸裁剪图 ----------

func (s *Server) facePath(faceID int64) string {
	sub := fmt.Sprintf("%02d", int(((faceID%100)+100)%100))
	return filepath.Join(s.cfg.CacheDir, "face", sub, strconv.FormatInt(faceID, 10)+".jpg")
}

func (s *Server) ensureFaceImage(faceID int64) (string, error) {
	dst := s.facePath(faceID)
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}
	unlock := s.lockID(faceID + 1_000_000)
	defer unlock()
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}

	var assetID int64
	var x, y, w, h float64
	err := s.st.db.QueryRow(`SELECT asset,x,y,w,h FROM faces WHERE id=?`, faceID).Scan(&assetID, &x, &y, &w, &h)
	if err != nil {
		return "", err
	}
	a, err := s.st.Get(assetID)
	if err != nil {
		return "", err
	}
	base, err := s.ensureThumb(a, "p") // 1600px 预览，坐标按它归一
	if err != nil {
		return "", err
	}
	pw, ph := a.W, a.H
	if a.Orient >= 5 && a.Orient <= 8 {
		pw, ph = ph, pw
	}
	// 预览图长边被限制在 1600
	cw, ch := fitSize(pw, ph, thumbW["p"])
	if pw <= 0 || ph <= 0 {
		cw, ch = 1600, 1600
	}

	pad := 0.25
	nx := clamp01(x - w*pad)
	ny := clamp01(y - h*pad)
	nw := math.Min(1-nx, w*(1+2*pad))
	nh := math.Min(1-ny, h*(1+2*pad))

	px := int(nx * float64(cw))
	py := int(ny * float64(ch))
	pwid := int(nw * float64(cw))
	phei := int(nh * float64(ch))
	if pwid < 8 || phei < 8 {
		return "", fmt.Errorf("face too small")
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	vf := fmt.Sprintf("crop=%d:%d:%d:%d,scale=200:-2:flags=lanczos", pwid, phei, px, py)
	ctx, cancel := timeoutCtx(45 * time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-y", "-i", base,
		"-frames:v", "1", "-vf", vf, "-q:v", "4", "-f", "mjpeg", dst).CombinedOutput()
	if err != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("ffmpeg: %s", truncate(string(out), 160))
	}
	return dst, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func (s *Server) hFaceImage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSuffix(r.PathValue("id"), ".jpg"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	p, err := s.ensureFaceImage(id)
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

// ---------- 人物下的照片 ----------

func (s *Server) hPersonAssets(w http.ResponseWriter, r *http.Request) {
	pid, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rows, err := s.st.db.Query(`SELECT DISTINCT asset FROM faces WHERE person=? LIMIT 200`, pid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	writeJSON(w, map[string]any{"ids": ids})
}

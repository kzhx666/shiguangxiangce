package main

import (
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func (s *Server) ensureSmartColumns() {
	stmts := []string{
		`ALTER TABLE assets ADD COLUMN phash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE assets ADD COLUMN dup_group INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE assets ADD COLUMN event_id INTEGER NOT NULL DEFAULT 0`,
	}
	for _, q := range stmts {
		_, _ = s.st.db.Exec(q)
	}
	_, _ = s.st.db.Exec(`CREATE INDEX IF NOT EXISTS idx_phash ON assets(phash)`)
	_, _ = s.st.db.Exec(`CREATE INDEX IF NOT EXISTS idx_dup ON assets(dup_group)`)
}

// ---------- dHash ----------

// dHash：9×8 灰度网格，横向相邻比较，得到 64 位指纹。
// 用稀疏采样求平均代替缩放，省一次解码后的大图重采样。
func dHash(img image.Image) uint64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 9 || h < 8 {
		return 0
	}
	var cell [8][9]float64
	for cy := 0; cy < 8; cy++ {
		for cx := 0; cx < 9; cx++ {
			var sum float64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					x := b.Min.X + (cx*4+sx)*w/36 + w/72
					y := b.Min.Y + (cy*4+sy)*h/32 + h/64
					if x >= b.Max.X {
						x = b.Max.X - 1
					}
					if y >= b.Max.Y {
						y = b.Max.Y - 1
					}
					r, g, bb, _ := img.At(x, y).RGBA()
					sum += 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bb)
				}
			}
			cell[cy][cx] = sum / 16
		}
	}
	var hash uint64
	k := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if cell[y][x] > cell[y][x+1] {
				hash |= 1 << uint(k)
			}
			k++
		}
	}
	return hash
}

func hamming(a, b uint64) int {
	x := a ^ b
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}

var hashing int32

// hashPending 给还没有指纹的照片补算 dHash；一次一张，避免内存峰值。
func (s *Server) hashPending(limit int) int {
	if !atomic.CompareAndSwapInt32(&hashing, 0, 1) {
		return 0
	}
	defer atomic.StoreInt32(&hashing, 0)

	rows, err := s.st.db.Query(`SELECT id, rel_path FROM assets
		WHERE status='active' AND kind='photo' AND phash='' LIMIT ?`, limit)
	if err != nil {
		return 0
	}
	type item struct {
		id  int64
		rel string
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.rel) == nil {
			items = append(items, it)
		}
	}
	rows.Close()

	n := 0
	for _, it := range items {
		var img image.Image
		// 优先从已有的缩略图算 dHash（640px/96px 解码只需 1MB 内存，原图可能吃 100~200MB）
		for _, sz := range []string{"t", "m"} {
			p := s.thumbPath(it.id, sz)
			if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
				if tf, err := os.Open(p); err == nil {
					img, _, _ = image.Decode(tf)
					tf.Close()
					if img != nil {
						break
					}
				}
			}
		}
		if img == nil {
			f, err := os.Open(filepath.Join(s.cfg.Root, it.rel))
			if err != nil {
				continue
			}
			img, _, err = image.Decode(f)
			f.Close()
			if err != nil {
				// 解不了的（HEIC 等）标记为跳过，避免每轮重试
				_, _ = s.st.db.Exec(`UPDATE assets SET phash='-' WHERE id=?`, it.id)
				continue
			}
		}
		h := dHash(img)
		_, _ = s.st.db.Exec(`UPDATE assets SET phash=? WHERE id=?`, fmtHash(h), it.id)
		n++
	}
	return n
}

func fmtHash(h uint64) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = hexd[h&0xf]
		h >>= 4
	}
	return string(out)
}

func parseHash(s string) (uint64, bool) {
	if len(s) != 16 || s == "----------------" {
		return 0, false
	}
	var v uint64
	for i := 0; i < 16; i++ {
		c := s[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		default:
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

// ---------- 相似照片归组 ----------

const dupThreshold = 10 // 汉明距离阈值，越小越严格

func (s *Server) rebuildDupes() int {
	rows, err := s.st.db.Query(`SELECT id, phash FROM assets WHERE status='active' AND kind='photo' AND phash<>'' AND phash<>'-'`)
	if err != nil {
		return 0
	}
	type ph struct {
		id int64
		h  uint64
	}
	var list []ph
	for rows.Next() {
		var id int64
		var hs string
		if rows.Scan(&id, &hs) != nil {
			continue
		}
		if v, ok := parseHash(hs); ok {
			list = append(list, ph{id, v})
		}
	}
	rows.Close()

	type group struct {
		rep uint64
		ids []int64
	}
	var groups []*group
	for _, p := range list {
		var hit *group
		for _, g := range groups {
			if hamming(g.rep, p.h) <= dupThreshold {
				hit = g
				break
			}
		}
		if hit != nil {
			hit.ids = append(hit.ids, p.id)
		} else {
			groups = append(groups, &group{rep: p.h, ids: []int64{p.id}})
		}
	}

	tx, err := s.st.db.Begin()
	if err != nil {
		return 0
	}
	_, _ = tx.Exec(`UPDATE assets SET dup_group=0`)
	dupGroups := 0
	for _, g := range groups {
		if len(g.ids) < 2 {
			continue
		}
		dupGroups++
		for _, id := range g.ids {
			_, _ = tx.Exec(`UPDATE assets SET dup_group=? WHERE id=?`, dupGroups, id)
		}
	}
	_ = tx.Commit()
	log.Printf("dupes: %d groups from %d hashed photos", dupGroups, len(list))
	return dupGroups
}

func (s *Server) hDupes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.db.Query(`SELECT dup_group, id, name, size, taken_at FROM assets
		WHERE status='active' AND dup_group>0 ORDER BY dup_group, size DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type item struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		TakenAt  int64  `json:"taken_at"`
	}
	out := []map[string]any{}
	curG := 0
	var cur []item
	flush := func() {
		if len(cur) >= 2 {
			var wasted int64
			for i := 1; i < len(cur); i++ {
				wasted += cur[i].Size
			}
			out = append(out, map[string]any{"group": curG, "items": cur, "wasted": wasted})
		}
	}
	for rows.Next() {
		var g int
		var it item
		if rows.Scan(&g, &it.ID, &it.Name, &it.Size, &it.TakenAt) != nil {
			continue
		}
		if g != curG {
			flush()
			curG = g
			cur = nil
		}
		cur = append(cur, it)
	}
	flush()
	writeJSON(w, map[string]any{"groups": out})
}

// ---------- 事件聚合 ----------

func (s *Server) rebuildEvents() int {
	rows, err := s.st.db.Query(`SELECT id, taken_at, lat, lon, has_gps FROM assets
		WHERE status='active' ORDER BY taken_at ASC, id ASC`)
	if err != nil {
		return 0
	}
	type row struct {
		id    int64
		t     int64
		lat   float64
		lon   float64
		gps   bool
	}
	var list []row
	for rows.Next() {
		var v row
		if rows.Scan(&v.id, &v.t, &v.lat, &v.lon, &v.gps) == nil {
			list = append(list, v)
		}
	}
	rows.Close()

	if len(list) == 0 {
		return 0
	}
	tx, _ := s.st.db.Begin()
	_, _ = tx.Exec(`UPDATE assets SET event_id=0`)

	const gapLimit = 8 * 3600 // 8 小时
	eid := 0
	var cur []row
	flush := func() {
		if len(cur) == 0 {
			return
		}
		eid++
		for _, v := range cur {
			_, _ = tx.Exec(`UPDATE assets SET event_id=? WHERE id=?`, eid, v.id)
		}
		cur = nil
	}
	for i, v := range list {
		if i == 0 {
			cur = append(cur, v)
			continue
		}
		prev := cur[len(cur)-1]
		newEvent := v.t-prev.t > gapLimit
		if !newEvent && v.gps && prev.gps {
			if absf(v.lat-prev.lat) > 1.0 || absf(v.lon-prev.lon) > 1.0 {
				newEvent = true
			}
		}
		if newEvent {
			flush()
		}
		cur = append(cur, v)
	}
	flush()
	_ = tx.Commit()
	log.Printf("events: %d", eid)
	return eid
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func (s *Server) hEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.db.Query(`SELECT event_id, COUNT(*) c, MIN(taken_at), MAX(taken_at), MIN(id)
		FROM assets WHERE status='active' AND event_id>0 GROUP BY event_id ORDER BY MAX(taken_at) DESC LIMIT 60`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var eid, c, start, end, cover int64
		if rows.Scan(&eid, &c, &start, &end, &cover) != nil {
			continue
		}
		var lat, lon float64
		var hasGPS int
		_ = s.st.db.QueryRow(`SELECT lat,lon,has_gps FROM assets WHERE event_id=? AND has_gps=1 LIMIT 1`, eid).
			Scan(&lat, &lon, &hasGPS)
		place := ""
		if hasGPS == 1 {
			place = nearestCity(lat, lon)
		}
		title := time.Unix(start, 0).Format("2006 年 1 月 2 日")
		if place != "" {
			title = place + " · " + title
		}
		out = append(out, map[string]any{
			"id": eid, "count": c, "start": start, "end": end, "cover": cover,
			"title": title, "place": place,
		})
	}
	writeJSON(w, map[string]any{"events": out})
}

// ---------- 地点 ----------

func (s *Server) hPlaces(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.db.Query(`SELECT lat, lon, id, taken_at FROM assets
		WHERE status='active' AND has_gps=1 ORDER BY taken_at DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	type agg struct {
		City  string  `json:"city"`
		Count int     `json:"count"`
		Cover int64   `json:"cover"`
		Lat   float64 `json:"lat"`
		Lon   float64 `json:"lon"`
	}
	m := map[string]*agg{}
	for rows.Next() {
		var lat, lon float64
		var id, t int64
		if rows.Scan(&lat, &lon, &id, &t) != nil {
			continue
		}
		city := nearestCity(lat, lon)
		if city == "" {
			city = "未知地点"
		}
		a, ok := m[city]
		if !ok {
			a = &agg{City: city, Lat: lat, Lon: lon, Cover: id}
			m[city] = a
		}
		a.Count++
	}
	out := []*agg{}
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	writeJSON(w, map[string]any{"places": out})
}

// ---------- 编辑 ----------

func (s *Server) hEdit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      int64   `json:"id"`
		Bright  float64 `json:"brightness"` // -100..100
		Contrast float64 `json:"contrast"`
		Satur   float64 `json:"saturation"`
		Temp    float64 `json:"temperature"`
		Vignette float64 `json:"vignette"` // 0..100
		Rotate  int     `json:"rotate"`     // 0/90/180/270
	}
	if err := jsonDecode(r, &req); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	a, err := s.st.Get(req.ID)
	if err != nil || a.Kind != "photo" {
		http.Error(w, "not found", 404)
		return
	}
	src := filepath.Join(s.cfg.Root, a.RelPath)

	// 本机 ffmpeg 4.3.5 没有 eq 滤镜，用 lutrgb + hue + colorbalance 组合出等效调整。
	var filters []string
	if req.Bright != 0 || req.Contrast != 0 {
		c := 1 + req.Contrast/200
		b := req.Bright * 0.6
		e := "clip((val-128)*" + fnum(c) + "+128+" + fnum(b) + ",0,255)"
		filters = append(filters, "lutrgb=r='"+e+"':g='"+e+"':b='"+e+"'")
	}
	if req.Satur != 0 {
		filters = append(filters, "hue=s="+fnum(1+req.Satur/100))
	}
	if req.Temp != 0 {
		t := req.Temp / 400
		filters = append(filters, "colorbalance=rs="+fnum(t)+":bs="+fnum(-t))
	}
	if req.Vignette > 0 {
		filters = append(filters, "vignette=angle="+fnum(0.4+req.Vignette/140))
	}
	switch req.Rotate {
	case 90:
		filters = append(filters, "transpose=1")
	case 180:
		filters = append(filters, "transpose=1,transpose=1")
	case 270:
		filters = append(filters, "transpose=2")
	}
	if len(filters) == 0 {
		http.Error(w, "no operations", 400)
		return
	}

	ext := filepath.Ext(a.Name)
	stem := strings.TrimSuffix(a.Name, ext)
	outName := stem + "-edit-" + time.Now().Format("150405") + ".jpg"
	dst := uniquePath(filepath.Dir(src), outName)

	args := []string{"-v", "error", "-y", "-noautorotate", "-i", src,
		"-vf", strings.Join(filters, ","), "-q:v", "3", "-frames:v", "1", dst}
	ctx, cancel := timeoutCtx(90 * time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		_ = os.Remove(dst)
		http.Error(w, "ffmpeg: "+truncate(string(out), 200), 500)
		return
	}
	rel, _ := filepath.Rel(s.cfg.Root, dst)
	pr := Open(dst)
	t := time.Unix(a.TakenAt, 0)
	id, err := s.insertUploaded(rel, "", pr, t, a.Folder, a.Sub)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	select {
	case s.queue <- id:
	default:
	}
	writeJSON(w, map[string]any{"ok": true, "asset": id, "rel_path": rel})
}

func fnum(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		s = "0"
	}
	return s
}

func jsonDecode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}
// ---------- 智能重建 ----------

func (s *Server) hSmartRebuild(w http.ResponseWriter, r *http.Request) {
	n := s.hashPending(5000)
	d := s.rebuildDupes()
	e := s.rebuildEvents()
	writeJSON(w, map[string]any{"ok": true, "hashed": n, "dup_groups": d, "events": e})
}

func (s *Server) hSmartStats(w http.ResponseWriter, r *http.Request) {
	var hashed, withGps, dupGroups, events int
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE phash<>'' AND phash<>'-'`).Scan(&hashed)
	_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE has_gps=1`).Scan(&withGps)
	_ = s.st.db.QueryRow(`SELECT COUNT(DISTINCT dup_group) FROM assets WHERE dup_group>0`).Scan(&dupGroups)
	_ = s.st.db.QueryRow(`SELECT COUNT(DISTINCT event_id) FROM assets WHERE event_id>0`).Scan(&events)
	writeJSON(w, map[string]any{"hashed": hashed, "gps": withGps, "dup_groups": dupGroups, "events": events})
}


package main

import (
	"runtime/debug"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var photoExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".jpe": true, ".png": true,
	".heic": true, ".heif": true, ".webp": true, ".bmp": true,
	".tif": true, ".tiff": true, ".gif": true,
	".dng": true, ".raw": true, ".cr2": true, ".nef": true, ".arw": true,
}
var videoExt = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true, ".3gp": true,
	".mkv": true, ".webm": true, ".avi": true,
}

type ScanResult struct {
	Added   int64 `json:"added"`
	Updated int64 `json:"updated"`
	Skipped int64 `json:"skipped"`
	Errors  int64 `json:"errors"`
	Elapsed int64 `json:"elapsed_ms"`
}

// scanRunning 防止并发扫描。
var scanning int32

func (s *Server) Scan() ScanResult {
	if !atomic.CompareAndSwapInt32(&scanning, 0, 1) {
		return ScanResult{}
	}
	defer atomic.StoreInt32(&scanning, 0)

	start := time.Now()
	var res ScanResult

	for _, o := range owners {
		dir := filepath.Join(s.cfg.Root, o.Dir)
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				res.Errors++
				return nil
			}
			// 跳过隐藏目录与垃圾/缓存目录
			if d.IsDir() {
				name := d.Name()
				if strings.HasPrefix(name, ".") || name == "lost+found" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			kind := ""
			if photoExt[ext] {
				kind = "photo"
			} else if videoExt[ext] {
				kind = "video"
			} else {
				return nil
			}
			fi, err := d.Info()
			if err != nil || fi.Size() == 0 {
				return nil
			}
			rel, err := filepath.Rel(s.cfg.Root, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			id, size, mtime, err := s.st.Existing(rel)
			if err != nil {
				res.Errors++
				return nil
			}
			// 宽高缺失（早期解析失败）的记录强制重探，否则缩略图会被算成正方形
			needDims := false
			if id > 0 {
				var w, h int
				if s.st.db.QueryRow(`SELECT w,h FROM assets WHERE id=?`, id).Scan(&w, &h) == nil && (w == 0 || h == 0) {
					needDims = true
				}
			}
			if id > 0 && !s.forceScan && !needDims && size == fi.Size() && mtime == fi.ModTime().Unix() {
				res.Skipped++
				return nil
			}

			pr := Open(path)
			a := &Asset{
				Owner: o.Owner, Kind: kind, RelPath: rel, Name: fi.Name(),
				Size: fi.Size(), MTime: fi.ModTime().Unix(),
				Mime: mimeOf(ext, kind), W: pr.W, H: pr.H, Orient: pr.Orientation,
				Camera: joinNonEmpty(pr.Make, pr.Model), Lens: pr.Lens,
				Aperture: pr.Aperture, ISO: pr.ISO, Focal: pr.Focal,
				Duration: pr.Duration, Lat: pr.Lat, Lon: pr.Lon, HasGPS: pr.HasGPS,
			}
			t := pr.TakenAt
			if t.IsZero() {
				t = fi.ModTime()
			}
			a.TakenAt = t.Unix()
			a.YearMonth = t.Format("2006-01")

			newID, err := s.st.Upsert(a)
			if err != nil {
				res.Errors++
				log.Printf("upsert %s: %v", rel, err)
				return nil
			}
			a.ID = newID
			if id == 0 {
				res.Added++
			} else {
				res.Updated++
			}
			// 预生成缩略图：队列满就异步补发，绝不丢（丢了下游只能等用户浏览时才现做）
			go func(id int64) {
				select {
				case s.queue <- id:
				case <-time.After(30 * time.Second):
				}
			}(newID)
			return nil
		})
	}
	res.Elapsed = time.Since(start).Milliseconds()
	s.st.SetMeta("last_scan", time.Now().Format(time.RFC3339))
	s.st.SetMeta("last_scan_added", itoa(res.Added))
	s.st.SetMeta("last_scan_ms", itoa(res.Elapsed))
	log.Printf("scan done: +%d ~%d =%d !%d in %dms", res.Added, res.Updated, res.Skipped, res.Errors, res.Elapsed)

	// 智能分析放到后台，不阻塞扫描返回
	go func() {
		if s.hashPending(3000) > 0 {
			s.rebuildDupes()
		}
		s.rebuildEvents()
		// 扫描/哈希是突发性负载，跑完把堆还给系统，避免常驻内存虚高
		debug.FreeOSMemory()
	}()
	return res
}

func (s *Server) StartWorkers(n int) {
	for i := 0; i < n; i++ {
		go func() {
			for id := range s.queue {
				a, err := s.st.Get(id)
				if err != nil {
					continue
				}
				for _, size := range []string{"m", "t"} {
					if _, err := s.ensureThumb(a, size); err != nil {
						log.Printf("thumb %s %d/%s: %v", a.Name, id, size, err)
					}
				}
			}
		}()
	}
}

func mimeOf(ext, kind string) string {
	if kind == "video" {
		switch ext {
		case ".mp4", ".m4v":
			return "video/mp4"
		case ".mov":
			return "video/quicktime"
		case ".webm":
			return "video/webm"
		case ".mkv":
			return "video/x-matroska"
		case ".3gp":
			return "video/3gpp"
		}
		return "video/mp4"
	}
	switch ext {
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic", ".heif":
		return "image/heic"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".bmp":
		return "image/bmp"
	}
	return "image/jpeg"
}

func joinNonEmpty(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" {
		return b
	}
	if b == "" || strings.Contains(b, a) {
		return b
	}
	return a + " " + b
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

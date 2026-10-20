package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func timeoutCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

var thumbW = map[string]int{"m": 96, "t": 640, "p": 1600}

// thumbFfmpegSem 限制同时运行的 ffmpeg 数量。
var thumbFfmpegSem = make(chan struct{}, 2)

// thumbPath 用 id%100 分片，避免单目录堆积过多文件。
// thumbPath 的缓存名包含 size 与 mtime：
// 只按 id 命名的话，重建数据库后 id 会分配给别的文件，旧缓存就会被当成新文件的缩略图。
func (s *Server) thumbPath(id int64, size string) string {
	return s.thumbKey(id, 0, 0, size)
}

func (s *Server) thumbKey(id, fsize, mtime int64, size string) string {
	sub := fmt.Sprintf("%02d", int(((id % 100) + 100) % 100))
	name := fmt.Sprintf("%d_%d_%d.jpg", id, fsize, mtime)
	return filepath.Join(s.cfg.CacheDir, size, sub, name)
}

func orientFilter(o int) string {
	switch o {
	case 2:
		return "hflip,"
	case 3:
		return "hflip,vflip,"
	case 4:
		return "vflip,"
	case 5:
		return "transpose=0,"
	case 6:
		return "transpose=1,"
	case 7:
		return "transpose=3,"
	case 8:
		return "transpose=2,"
	}
	return ""
}

// ensureThumb 生成（或复用）指定档位缩略图。
func (s *Server) ensureThumb(a *Asset, size string) (string, error) {
	px, ok := thumbW[size]
	if !ok {
		return "", fmt.Errorf("bad size")
	}
	dst := s.thumbKey(a.ID, a.Size, a.MTime, size)
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}
	unlock := s.lockID(a.ID)
	defer unlock()
	if fi, err := os.Stat(dst); err == nil && fi.Size() > 0 {
		return dst, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	src := filepath.Join(s.cfg.Root, a.RelPath)
	fi, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	// 客户端传过小预览（封面）就直接拿它当源：几百 KB 的图解码几乎不占内存，
	// 这是超大图唯一可行的方案。
	posterAsSource := false
	if pp := s.posterPath(a); pp != "" {
		if pf, perr := os.Stat(pp); perr == nil && pf.Size() > 0 {
			src = pp
			fi = pf
			posterAsSource = true
		}
	}
	// 如果是 HEIC/HEIF 且还没有海报，优先从文件内部提取内嵌的 JPEG 预览
	if !posterAsSource {
		ext := strings.ToLower(filepath.Ext(a.RelPath))
		if ext == ".heic" || ext == ".heif" {
			pp := s.posterPath(a)
			if pp != "" && extractHEICPoster(src, pp) {
				if pf, perr := os.Stat(pp); perr == nil && pf.Size() > 0 {
					src = pp
					fi = pf
					posterAsSource = true
				}
			}
		}
	}

	// 超大图保护：路由器的 ffmpeg 解码一张 2000 万像素以上的图要上百 MB 内存，
	// 会把整台路由器拖垮（实测一张 76MB 的初修底片让 ffmpeg 吃到 224MB）。
	if a.Kind != "video" && !posterAsSource {
		const maxBytes = 25 << 20 // 25MB
		const maxPixels = 40000000
		if fi.Size() > maxBytes || (a.W > 0 && a.H > 0 && int64(a.W)*int64(a.H) > maxPixels) {
			return "", fmt.Errorf("too large for router-side thumbnail: %d bytes %dx%d", fi.Size(), a.W, a.H)
		}
	} else if a.Kind == "video" {
		// 视频只认客户端上传的封面（路由器没有任何视频解码器）
		pp := s.posterPath(a)
		if pp == "" {
			return "", fmt.Errorf("no poster for video")
		}
		if pf, perr := os.Stat(pp); perr != nil || pf.Size() == 0 {
			return "", fmt.Errorf("no poster for video")
		}
	}

	tmp := dst + ".tmp.jpg"

	// 缩略图统一走 ffmpeg 子进程：内存可控，不会把主进程撑爆

	var args []string
	if a.Kind == "video" {
		// 客户端上传时截好的海报优先（路由器解不了 H.264）
		input := src
		seek := []string{"-ss", "1"}
		if pp := s.posterPath(a); pp != "" {
			if fi, err := os.Stat(pp); err == nil && fi.Size() > 0 {
				input = pp
				seek = nil
			}
		}
		var vf string
		if a.W > 0 && a.H > 0 {
			tw, th := fitSize(a.W, a.H, px)
			vf = fmt.Sprintf("scale=%d:%d:flags=fast_bilinear", tw, th)
		} else {
			vf = fmt.Sprintf("scale=%d:-2:flags=fast_bilinear", px) // 宽高未知时按宽度缩放，保持比例
		}
		args = []string{"-v", "error", "-y"}
		args = append(args, seek...)
		args = append(args, "-i", input, "-frames:v", "1", "-vf", vf, "-q:v", "5", "-f", "mjpeg", tmp)
	} else {
		w, h := a.W, a.H
		orient := a.Orient
		if posterAsSource {
			orient = 1 // 海报已校正方向，无需重复旋转
			if w <= 0 || h <= 0 {
				if f, err := os.Open(src); err == nil {
					if cfg, _, err := image.DecodeConfig(f); err == nil && cfg.Width > 0 && cfg.Height > 0 {
						w, h = cfg.Width, cfg.Height
						a.W, a.H = w, h
						_, _ = s.st.db.Exec(`UPDATE assets SET w=?, h=? WHERE id=?`, w, h, a.ID)
					}
					f.Close()
				}
			}
		}
		if orient >= 5 && orient <= 8 {
			w, h = h, w
		}
		tw, th := fitSize(w, h, px)
		vf := orientFilter(orient) + fmt.Sprintf("scale=%d:%d:flags=fast_bilinear", tw, th)
		args = []string{"-v", "error", "-y", "-noautorotate", "-i", src,
			"-frames:v", "1", "-vf", vf, "-q:v", "5", "-f", "mjpeg", tmp}
	}
	ctx, cancel := timeoutCtx(45 * time.Second)
	// 全局最多 2 个 ffmpeg 同时跑：并发太多会把 CPU 和内存打满，
	// 每张反而更慢，还容易被内核 OOM 杀掉。
	// 机器被备份/其它任务压满时不要硬上：ffmpeg 会被 OOM 杀掉，
	// 白白产出失败记录。这时直接返回错误，前端用占位图顶着，等空闲了再补。
	if load1() > 16.0 {
		return "", fmt.Errorf("busy: system load too high")
	}
	thumbFfmpegSem <- struct{}{}
	defer func() { <-thumbFfmpegSem }()

	defer cancel()
	args = append([]string{"-threads", "1"}, args...)
	// 用最低优先级跑 ffmpeg：路由器的转发/网络不能被它抢 CPU
	cmd := exec.CommandContext(ctx, "nice", append([]string{"-n", "10", "ffmpeg"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("ffmpeg: %v: %s", err, truncate(string(out), 200))
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Size() == 0 {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("ffmpeg produced no output")
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	_, _ = s.st.db.Exec(`UPDATE assets SET nothumb=0 WHERE id=? AND nothumb=1`, a.ID)
	return dst, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// fitSize 把长边限制在 max 以内，并且绝不放大原图。
func fitSize(w, h, max int) (int, int) {
	if w <= 0 || h <= 0 {
		return max, max
	}
	long := w
	if h > long {
		long = h
	}
	if long <= max {
		return even(w), even(h)
	}
	r := float64(max) / float64(long)
	return even(int(float64(w)*r + 0.5)), even(int(float64(h)*r + 0.5))
}

func even(v int) int {
	if v < 2 {
		return 2
	}
	if v%2 == 1 {
		return v - 1
	}
	return v
}

// extractHEICPoster 扫描 HEIC/HEIF 内部内嵌的 JPEG 预览图并另存为 poster
func extractHEICPoster(heicPath, dstPath string) bool {
	f, err := os.Open(heicPath)
	if err != nil {
		return false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 || fi.Size() > 50<<20 {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(f, 50<<20))
	if err != nil || len(data) < 2048 {
		return false
	}
	var bestStart, bestEnd int
	var maxLen int
	pos := 0
	for pos+4 < len(data) {
		idx := bytes.Index(data[pos:], []byte{0xFF, 0xD8, 0xFF})
		if idx == -1 {
			break
		}
		start := pos + idx
		endIdx := bytes.Index(data[start+4:], []byte{0xFF, 0xD9})
		if endIdx == -1 {
			pos = start + 3
			continue
		}
		end := start + 4 + endIdx + 2
		l := end - start
		if l > 8192 && l > maxLen {
			maxLen = l
			bestStart = start
			bestEnd = end
		}
		pos = end
	}
	if maxLen > 8192 {
		_ = os.MkdirAll(filepath.Dir(dstPath), 0o755)
		if err := os.WriteFile(dstPath, data[bestStart:bestEnd], 0o644); err == nil {
			return true
		}
	}
	return false
}

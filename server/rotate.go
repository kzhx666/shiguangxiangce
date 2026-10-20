package main

// 图片旋转：把旋转后的像素直接写回原文件（不只是在数据库里记一个角度），
// 这样任何客户端、任何浏览器看到的都是转好的图，不会出现「缩略图转了、大图没转」。
//
// 代价：要重新编码一次 JPEG（路由器上一次约 0.5~2 秒），
// 但这是用户主动点的操作，可以接受。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// hRotate 顺时针/逆时针旋转 90°，直接改写原文件。
// 请求体：{"dir": 1} 顺时针；{"dir": -1} 逆时针
func (s *Server) hRotate(w http.ResponseWriter, r *http.Request) {
	id := atoi64(r.PathValue("id"))
	dir := 1
	var req struct {
		Dir int `json:"dir"`
	}
	_ = decodeJSON(r, &req)
	if req.Dir != 0 {
		dir = req.Dir
	}

	a, err := s.st.Get(id)
	if err != nil || !canSee(userOf(r), a.Owner) {
		http.Error(w, "not found", 404)
		return
	}
	if a.Kind != "photo" {
		http.Error(w, "only photos can be rotated", 400)
		return
	}
	src := filepath.Join(s.cfg.Root, a.RelPath)
	if _, statErr := os.Stat(src); statErr != nil {
		http.Error(w, "file missing", 404)
		return
	}

	// 先把现有的 EXIF 方向摆正，再按用户要求转 90°，最后统一记成 orient=1
	transpose := "transpose=1,"
	if dir < 0 {
		transpose = "transpose=2,"
	}
	vf := orientFilter(a.Orient) + transpose + "null"

	tmp := src + ".rot.jpg"
	args := []string{"-v", "error", "-y", "-noautorotate", "-i", src,
		"-vf", vf, "-q:v", "2", "-f", "mjpeg", tmp}
	ctx, cancel := timeoutCtx(120 * time.Second)
	defer cancel()
	out, runErr := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
	if runErr != nil {
		_ = os.Remove(tmp)
		http.Error(w, fmt.Sprintf("rotate failed: %v %s", runErr, truncate(string(out), 200)), 500)
		return
	}
	if fi, statErr := os.Stat(tmp); statErr != nil || fi.Size() == 0 {
		_ = os.Remove(tmp)
		http.Error(w, "rotate produced no output", 500)
		return
	}

	// 保留原文件的修改时间，避免打乱排序；但缩略图缓存键要换，所以用当前时间
	now := time.Now()
	if err := os.Rename(tmp, src); err != nil {
		_ = os.Remove(tmp)
		http.Error(w, err.Error(), 500)
		return
	}
	_ = os.Chtimes(src, now, now)
	newMtime := now.Unix()

	// 宽高互换、方向归一
	nw, nh := a.H, a.W
	_, _ = s.st.db.Exec(`UPDATE assets SET w=?, h=?, orient=1, mtime=?, size=?, sha256='' WHERE id=?`,
		nw, nh, newMtime, sizeOfFile(src), id)
	// 旧缩略图/封面作废
	for _, sz := range []string{"m", "t", "p"} {
		_ = os.Remove(s.thumbKey(a.ID, a.Size, a.MTime, sz))
	}
	if pp := s.posterPath(a); pp != "" {
		_ = os.Remove(pp)
	}

	writeJSON(w, map[string]any{"ok": true, "w": nw, "h": nh, "mtime": newMtime})
}

// sizeOfFile 取文件大小，失败返回 0。
func sizeOfFile(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

// decodeJSON 读一个小 JSON 请求体。
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}
package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// warmThumbs 在后台把整个库的缩略图提前生成好，用户滑动时直接读缓存文件。
//
// 策略：
//   - 只处理「缓存文件还不存在」的，已有的跳过；
//   - 一次最多处理 limit 个，每张之间歇一小会儿，避免把路由器 CPU 占满；
//   - 服务启动后跑一次，每次扫描完成后也跑一次。
var warmMu sync.Mutex

func (s *Server) warmThumbs(limit int) (made int) {
	// 同一时间只允许一个预热任务，否则会把路由器 CPU 打满
	if !warmMu.TryLock() {
		return 0
	}
	defer warmMu.Unlock()
	rows, err := s.st.db.Query(`SELECT id, owner, rel_path, size, mtime, kind, orient
		FROM assets WHERE status='active' AND missing=0 AND nothumb=0 ORDER BY taken_at DESC`)
	if err != nil {
		return 0
	}
	type job struct {
		id    int64
		owner string
		rel   string
		fsize int64
		mtime int64
		kind  string
		orient int
	}
	var list []job
	for rows.Next() {
		var j job
		if rows.Scan(&j.id, &j.owner, &j.rel, &j.fsize, &j.mtime, &j.kind, &j.orient) == nil {
			list = append(list, j)
		}
	}
	rows.Close()
	if len(list) == 0 {
		return 0
	}

	start := time.Now()
	failed := 0
	for _, j := range list {
		if made >= limit {
			break
		}
		// 缺哪个尺寸就补哪个
		need := []string{}
		for _, size := range []string{"m", "t"} {
			if _, err := os.Stat(s.thumbKey(j.id, j.fsize, j.mtime, size)); err != nil {
				need = append(need, size)
			}
		}
		if len(need) == 0 {
			continue
		}
		// 用独立子进程生成：个别文件会让磁盘读进入 D 状态（不可中断），
		// 子进程可以被 kill，主服务完全不受影响。
		okAny := false
		if exe, exErr := os.Executable(); exErr == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			cmd := exec.CommandContext(ctx, exe,
				"-root", s.cfg.Root, "-data", s.cfg.DataDir, "-cache", s.cfg.CacheDir,
				"-thumb-one", j.rel,
				"-thumb-id", itoa(j.id),
				"-thumb-fsize", itoa(j.fsize),
				"-thumb-fmtime", itoa(j.mtime),
				"-thumb-orient", itoa(int64(j.orient)))
			_, runErr := cmd.CombinedOutput()
			cancel()
			if runErr == nil {
				okAny = true
			} else {
				_, _ = s.st.db.Exec(`UPDATE assets SET nothumb=1 WHERE id=?`, j.id)
				for _, size := range need {
					_ = os.Remove(s.thumbKey(j.id, j.fsize, j.mtime, size) + ".tmp.jpg")
				}
			}
		}
		if okAny {
			made++
			// 固定让路时间：生成一张约 60~150ms，再歇 150ms，
			// 大约 30% 的 CPU 占用，不影响正常浏览。
			time.Sleep(150 * time.Millisecond)
		}
	}
	log.Printf("warm thumbs: 待补 %d 张，成功 %d，失败 %d，用时 %v",
		len(list), made, failed, time.Since(start).Round(time.Second))
	return made
}

// load1 读 /proc/loadavg 的 1 分钟负载，用来给预热任务限速。
func load1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}


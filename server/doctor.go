package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type checkResult struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fatal  bool   `json:"fatal"`
}

func dirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func toolVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "-version").CombinedOutput()
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	return truncate(strings.TrimSpace(line), 60), nil
}

func dirSize(path string) int64 {
	var sum int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			sum += info.Size()
		}
		return nil
	})
	return sum
}

func (s *Server) runChecks() []checkResult {
	var out []checkResult
	add := func(name string, ok bool, detail string, fatal bool) {
		out = append(out, checkResult{Name: name, OK: ok, Detail: detail, Fatal: fatal})
	}

	// 1. 媒体根目录
	if fi, err := os.Stat(s.cfg.Root); err != nil {
		add("媒体根目录", false, s.cfg.Root+" 不存在: "+err.Error(), true)
	} else if !fi.IsDir() {
		add("媒体根目录", false, s.cfg.Root+" 不是目录", true)
	} else {
		add("媒体根目录", true, s.cfg.Root, false)
	}

	// 2. 数据目录可写
	if err := dirWritable(s.cfg.DataDir); err != nil {
		add("数据目录", false, s.cfg.DataDir+" 不可写: "+err.Error(), true)
	} else {
		add("数据目录", true, s.cfg.DataDir, false)
	}

	// 3. 缩略图缓存可写
	if err := dirWritable(s.cfg.CacheDir); err != nil {
		add("缩略图缓存", false, s.cfg.CacheDir+" 不可写: "+err.Error(), true)
	} else {
		add("缩略图缓存", true, fmt.Sprintf("%s（%s）", s.cfg.CacheDir, humanSize(dirSize(s.cfg.CacheDir))), false)
	}

	// 4. ffmpeg / ffprobe
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if v, err := toolVersion(bin); err != nil {
			add(bin, false, "未找到或不可执行", true)
		} else {
			add(bin, true, v, false)
		}
	}

	// 5. 数据库完整性
	var integrity string
	if err := s.st.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		add("数据库", false, "integrity_check 失败: "+err.Error(), true)
	} else if integrity != "ok" {
		add("数据库", false, "integrity_check = "+integrity, true)
	} else {
		var assets, faces int
		_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&assets)
		_ = s.st.db.QueryRow(`SELECT COUNT(*) FROM faces`).Scan(&faces)
		add("数据库", true, fmt.Sprintf("ok · %d 条目 · %d 人脸 · %s",
			assets, faces, humanSize(fileSize(filepath.Join(s.cfg.DataDir, "lumina.db")))), false)
	}

	// 6. 磁盘空间
	free, total := diskFree(s.cfg.Root)
	if total == 0 {
		add("磁盘空间", false, "无法读取 statfs", false)
	} else if free < 1 {
		add("磁盘空间", false, fmt.Sprintf("仅剩 %.2f GB / %.1f GB", free, total), true)
	} else {
		add("磁盘空间", true, fmt.Sprintf("剩余 %.1f GB / %.1f GB", free, total), false)
	}

	// 7. 目录结构
	missing := []string{}
	for _, o := range ownerDefs() {
		p := filepath.Join(s.cfg.Root, o.Dir)
		if _, err := os.Stat(p); err != nil {
			missing = append(missing, o.Dir)
		}
	}
	if len(missing) > 0 {
		add("空间目录", false, "缺: "+strings.Join(missing, ", ")+"（首次扫描会自动创建）", false)
	} else {
		add("空间目录", true, "lwm / cr / 家庭共享 就绪", false)
	}

	// 8. 最近备份
	last := s.st.GetMeta("last_backup")
	if last == "" {
		add("数据库备份", false, "还没有备份过", false)
	} else {
		ts := atoi64(last)
		age := time.Since(time.Unix(ts, 0))
		add("数据库备份", age < 48*time.Hour,
			fmt.Sprintf("%s 前（%s）", roughAge(age), time.Unix(ts, 0).Format("2006-01-02 15:04")), false)
	}

	return out
}

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func roughAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "不到 1 分钟"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d 小时", int(d.Hours()))
	}
	return fmt.Sprintf("%d 天", int(d.Hours()/24))
}

func (s *Server) printDoctor() int {
	fmt.Println("LuminaGallery 自检")
	fmt.Println(strings.Repeat("─", 62))
	fail := 0
	for _, c := range s.runChecks() {
		mark := "✓"
		if !c.OK {
			mark = "✗"
			if c.Fatal {
				fail++
			}
		}
		fmt.Printf(" %s %-14s %s\n", mark, c.Name, c.Detail)
	}
	fmt.Println(strings.Repeat("─", 62))
	if fail > 0 {
		fmt.Printf("发现 %d 项致命问题，服务可能无法正常工作。\n", fail)
		return 1
	}
	fmt.Println("全部关键检查通过。")
	return 0
}

func (s *Server) hDoctor(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"checks": s.runChecks(), "version": "2.0.0"})
}

// ---------- 备份 ----------

func (s *Server) backupDir() string { return filepath.Join(s.cfg.DataDir, "backups") }

// backupDB 用 VACUUM INTO 做一致性热备（不需要停服），并只保留最近 7 份。
func (s *Server) backupDB() (string, error) {
	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "lumina-" + time.Now().Format("20060102-150405") + ".db"
	dst := filepath.Join(dir, name)
	if _, err := s.st.db.Exec(`VACUUM INTO ?`, dst); err != nil {
		return "", err
	}
	s.st.SetMeta("last_backup", fmt.Sprintf("%d", time.Now().Unix()))
	s.pruneBackups(7)
	return dst, nil
}

func (s *Server) pruneBackups(keep int) {
	ents, err := os.ReadDir(s.backupDir())
	if err != nil {
		return
	}
	var files []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".db") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for len(files) > keep {
		_ = os.Remove(filepath.Join(s.backupDir(), files[0]))
		files = files[1:]
	}
}

func (s *Server) hBackup(w http.ResponseWriter, r *http.Request) {
	p, err := s.backupDB()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": p, "size": fileSize(p)})
}

// 每天备份一次
func (s *Server) backupLoop() {
	for {
		if last := s.st.GetMeta("last_backup"); last != "" {
			if time.Since(time.Unix(atoi64(last), 0)) < 24*time.Hour {
				time.Sleep(time.Hour)
				continue
			}
		}
		if p, err := s.backupDB(); err == nil {
			log.Printf("backup: %s", p)
		} else {
			log.Printf("backup failed: %v", err)
		}
		time.Sleep(time.Hour)
	}
}

// gcThumbCache 删除长期没被访问过的缩略图/海报缓存，避免无限膨胀。
func (s *Server) gcThumbCache(maxAge time.Duration) (removed int, freed int64) {
	cutoff := time.Now().Add(-maxAge)
	for _, dir := range []string{"m", "t", "p", "poster"} {
		root := filepath.Join(s.cfg.CacheDir, dir)
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if info.ModTime().After(cutoff) {
				return nil
			}
			size := info.Size()
			if os.Remove(path) == nil {
				removed++
				freed += size
			}
			return nil
		})
	}
	if removed > 0 {
		log.Printf("cache gc: removed %d files, %.1f MB", removed, float64(freed)/1048576)
	}
	return
}

// checkpointWAL 把 WAL 合并回主库，防止 -wal 文件无限增长。
func (s *Server) checkpointWAL() {
	_, _ = s.st.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
}
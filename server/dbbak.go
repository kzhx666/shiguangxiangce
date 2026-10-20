package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 数据库每日备份：只用 Go 标准库，不依赖 sqlite3 命令行。
//
// 库里只有索引（4MB 左右），照片视频本体不在这里，所以备份非常轻：
// 压缩后约 1MB、耗时 1 秒内。默认保留 14 天。

const backupKeepDays = 14

// startDBBackup 启动后台任务：启动 5 分钟后先跑一次，之后每 24 小时一次。
func (s *Server) startDBBackup() {
	go func() {
		// 启动后稍微等一下，避开开机时的扫描高峰
		time.Sleep(5 * time.Minute)
		s.doBackup()
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for range t.C {
			s.doBackup()
		}
	}()
}

func (s *Server) dbBackupDir() string {
	return filepath.Join(s.cfg.DataDir, "backups")
}

// doBackup 生成一个压缩副本，超过保留期的自动删除。
func (s *Server) doBackup() {
	dir := s.dbBackupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("backup: mkdir %s: %v", dir, err)
		return
	}
	stamp := time.Now().Format("20060102-1504")
	out := filepath.Join(dir, fmt.Sprintf("lumina-%s.db.gz", stamp))

	// VACUUM INTO 是 SQLite 官方的在线备份方式，不会读到写了一半的页
	tmpDB := filepath.Join(dir, ".snapshot.db")
	os.Remove(tmpDB)
	if _, err := s.st.db.Exec("VACUUM INTO ?", tmpDB); err != nil {
		// 老版本 SQLite 不支持 VACUUM INTO 时退回文件拷贝
		if _, e2 := os.Stat(filepath.Join(s.cfg.DataDir, "lumina.db")); e2 == nil {
			if err := copyPlain(filepath.Join(s.cfg.DataDir, "lumina.db"), tmpDB); err != nil {
				log.Printf("backup: snapshot: %v / %v", err, e2)
				return
			}
		} else {
			log.Printf("backup: vacuum into: %v", err)
			return
		}
	}

	src, err := os.Open(tmpDB)
	if err != nil {
		log.Printf("backup: open snapshot: %v", err)
		return
	}
	defer src.Close()

	f, err := os.Create(out)
	if err != nil {
		log.Printf("backup: create %s: %v", out, err)
		return
	}
	gw := gzip.NewWriter(f)
	n, cerr := io.Copy(gw, src)
	if cerr == nil {
		cerr = gw.Close()
	}
	f.Close()
	os.Remove(tmpDB)
	if cerr != nil {
		log.Printf("backup: compress: %v", cerr)
		os.Remove(out)
		return
	}
	log.Printf("backup: wrote %s (%.1f MB raw -> %.2f MB gz)",
		filepath.Base(out), float64(n)/(1<<20), float64(gzSize(out))/(1<<20))

	pruneBackups(dir)
}

func gzSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return 0
}

func copyPlain(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, e := io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}

// pruneBackups 只保留最近 backupKeepDays 天。
func pruneBackups(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type item struct {
		name string
		mod  time.Time
	}
	var list []item
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "lumina-") || !strings.HasSuffix(e.Name(), ".db.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, item{e.Name(), info.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })
	cutoff := time.Now().AddDate(0, 0, -backupKeepDays)
	for _, it := range list {
		if it.mod.Before(cutoff) {
			os.Remove(filepath.Join(dir, it.name))
		}
	}
}

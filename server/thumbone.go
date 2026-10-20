package main

// 缩略图独立进程模式：
//   lumina -thumb-one <rel> -thumb-id <id> -thumb-size <bytes> -thumb-mtime <sec> -thumb-orient <o>
//
// 为什么不在主进程里直接生成：USB 硬盘上个别文件会让 read 进入 D 状态
// （不可中断），Go 里没有任何办法打断它，会把整个批次永久卡住。
// 拆成子进程后，父进程用 context 超时直接 kill，主服务完全不受影响。

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// runThumbOne 在子进程里生成一张（两个尺寸）缩略图，生成完就退出。
func runThumbOne(c Config, id int64, rel string, fsize, mtime int64, orient int) int {
	s := &Server{cfg: c}
	src := filepath.Join(c.Root, rel)
	if _, err := os.Stat(src); err != nil {
		fmt.Println("no file:", err)
		return 1
	}
	ok := 0
	for _, size := range []string{"m", "t"} {
		px, known := thumbW[size]
		if !known {
			continue
		}
		dst := s.thumbKey(id, fsize, mtime, size)
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		tmp := dst + ".tmp.jpg"
		if err := genThumbGo(src, tmp, px, orient); err != nil {
			_ = os.Remove(tmp)
			return 2
		}
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Remove(tmp)
			return 3
		}
		ok++
	}
	if ok == 0 {
		return 4
	}
	return 0
}

// thumbSem 全局信号量：同一时刻只允许一个子进程在解码大图，
// 否则并发解码几张照片就能把路由器内存吃光。
var thumbSem = make(chan struct{}, 1)

// thumbViaChild 调自己（-thumb-one）生成缩略图，超时就 kill。
// 子进程退出后内存立刻释放，主进程不会被大图撑爆。
func (s *Server) thumbViaChild(a *Asset, size, src string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	px, ok := thumbW[size]
	if !ok {
		return fmt.Errorf("bad size")
	}
	_ = px
	dst := s.thumbKey(a.ID, a.Size, a.MTime, size)
	_ = os.MkdirAll(filepath.Dir(dst), 0o755)
	if fi, statErr := os.Stat(dst); statErr == nil && fi.Size() > 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe,
		"-root", s.cfg.Root, "-data", s.cfg.DataDir, "-cache", s.cfg.CacheDir,
		"-thumb-one", a.RelPath,
		"-thumb-id", strconv.FormatInt(a.ID, 10),
		"-thumb-fsize", strconv.FormatInt(a.Size, 10),
		"-thumb-fmtime", strconv.FormatInt(a.MTime, 10),
		"-thumb-orient", strconv.Itoa(a.Orient))
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return fmt.Errorf("thumb child: %v %s", runErr, string(out))
	}
	if fi, statErr := os.Stat(dst); statErr != nil || fi.Size() == 0 {
		return fmt.Errorf("child produced nothing")
	}
	return nil
}

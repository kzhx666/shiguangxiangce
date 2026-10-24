package main

import "syscall"

func diskFree(path string) (freeGB, totalGB float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bs := float64(st.Bsize)
	freeGB = float64(st.Bavail) * bs / (1 << 30)
	totalGB = float64(st.Blocks) * bs / (1 << 30)
	return
}

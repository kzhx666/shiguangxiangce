package main

import "golang.org/x/sys/windows"

func diskFree(path string) (float64, float64) {
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, 0
	}
	var free, total, available uint64
	if windows.GetDiskFreeSpaceEx(p, &available, &total, &free) != nil {
		return 0, 0
	}
	return float64(available) / (1 << 30), float64(total) / (1 << 30)
}

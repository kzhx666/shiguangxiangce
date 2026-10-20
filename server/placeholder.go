package main

// 缩略图占位图：路由器解不了 HEVC/H.264 的视频、以及个别损坏的图片，
// 以前会返回 500，前端就显示成破图，而且每次滚到都要重新尝试（每次几十毫秒）。
// 这里生成一张深色卡片 + 播放三角，直接返回 200，界面干净且不再反复重试。

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
)

var placeholderCache sync.Map // size -> []byte

// placeholderJPEG 按缩略图档位生成占位图（内存里只算一次）。
func placeholderJPEG(size string) []byte {
	if v, ok := placeholderCache.Load(size); ok {
		return v.([]byte)
	}
	px, ok := thumbW[size]
	if !ok || px <= 0 {
		px = 640
	}
	w := px
	h := px * 9 / 16
	if h < 60 {
		h = 60
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{R: 16, G: 19, B: 26, A: 255} // #10131A
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = bg.R
		img.Pix[i+1] = bg.G
		img.Pix[i+2] = bg.B
		img.Pix[i+3] = 255
	}
	// 中间画一个青色播放三角
	accent := color.RGBA{R: 94, G: 234, B: 212, A: 255}
	cx, cy := w/2, h/2
	half := px / 10
	if half < 8 {
		half = 8
	}
	for y := -half; y <= half; y++ {
		width := (half - y) / 2
		if y > 0 {
			width = (half + y) / 2
		}
		width += half / 2
		for x := half / 2; x < half/2+width; x++ {
			px2, py2 := cx+x, cy+y
			if px2 < 0 || py2 < 0 || px2 >= w || py2 >= h {
				continue
			}
			o := py2*img.Stride + px2*4
			img.Pix[o] = accent.R
			img.Pix[o+1] = accent.G
			img.Pix[o+2] = accent.B
			img.Pix[o+3] = 255
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		return nil
	}
	b := buf.Bytes()
	placeholderCache.Store(size, b)
	return b
}

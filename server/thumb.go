package main

import (
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"os"
)

// genThumbGo 纯 Go 生成缩略图：解码 → 盒式缩放 → 旋转 → 编码 JPEG。
//
// 为什么不走 ffmpeg：这台路由器每张缩略图都要 fork 一个 ffmpeg 进程，
// 单张约 0.5 秒、吃掉 24% CPU，几百张就把机器压满。Go 内做同样的活只要几十毫秒。
func genThumbGo(srcPath, dstPath string, px int, orient int) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return err
	}
	sb := img.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return fmt.Errorf("empty image")
	}

	// 方向 5–8 表示宽高互换
	w, h := sw, sh
	if orient >= 5 && orient <= 8 {
		w, h = h, w
	}
	tw, th := fitSize(w, h, px)
	if tw <= 0 || th <= 0 {
		return fmt.Errorf("bad target size")
	}

	// 缩放（旋转前，行列按未旋转的尺寸算）
	rw, rh := tw, th
	if orient >= 5 && orient <= 8 {
		rw, rh = th, tw
	}
	small := resizeBox(img, rw, rh)

	if orient >= 2 && orient <= 8 {
		small = orientImage(small, orient)
	}

	out, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return jpeg.Encode(out, small, &jpeg.Options{Quality: 82})
}

// resizeBox 盒式平均缩放：缩小时质量足够，且只遍历源像素一次。
func resizeBox(src image.Image, tw, th int) *image.RGBA {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()

	// 不整幅转 RGBA（一张 1200 万像素的照片会多吃 48MB 内存，路由器扛不住），
	// 直接从源图按块采样。缩小时每个输出像素取约 6×6 个源像素，CPU 也就几十毫秒。
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	xr := float64(sw) / float64(tw)
	yr := float64(sh) / float64(th)
	if xr < 1 {
		xr = 1
	}
	if yr < 1 {
		yr = 1
	}

	for y := 0; y < th; y++ {
		y0 := int(float64(y) * yr)
		y1 := int(float64(y+1) * yr)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > sh {
			y1 = sh
		}
		for x := 0; x < tw; x++ {
			x0 := int(float64(x) * xr)
			x1 := int(float64(x+1) * xr)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > sw {
				x1 = sw
			}
			var r, g, b, n uint32
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, _ := src.At(sb.Min.X+xx, sb.Min.Y+yy).RGBA()
					r += cr >> 8
					g += cg >> 8
					b += cb >> 8
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0] = uint8(r / n)
			o[1] = uint8(g / n)
			o[2] = uint8(b / n)
			o[3] = 255
		}
	}
	return dst
}

// orientImage 按 EXIF orientation 把图片摆正。
func orientImage(src *image.RGBA, orient int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	var dw, dh int
	if orient >= 5 {
		dw, dh = h, w
	} else {
		dw, dh = w, h
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch orient {
			case 2: // 水平镜像
				nx, ny = w-1-x, y
			case 3: // 180°
				nx, ny = w-1-x, h-1-y
			case 4: // 垂直镜像
				nx, ny = x, h-1-y
			case 5: // 转置
				nx, ny = y, x
			case 6: // 顺时针 90°
				nx, ny = h-1-y, x
			case 7: // 反转置
				nx, ny = h-1-y, w-1-x
			case 8: // 逆时针 90°
				nx, ny = y, w-1-x
			default:
				nx, ny = x, y
			}
			si := y*src.Stride + x*4
			di := ny*dst.Stride + nx*4
			if di+3 < len(dst.Pix) && si+3 < len(src.Pix) {
				copy(dst.Pix[di:di+4], src.Pix[si:si+4])
			}
		}
	}
	return dst
}

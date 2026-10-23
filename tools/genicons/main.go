//go:build ignore

// genicons สร้างไอคอน Share Group เป็นไฟล์ PNG
//
//	go run tools/genicons/main.go
//
// สัญลักษณ์คือไฟล์กลางวงกลุ่ม (โลโก้แบบ C): หน้ากระดาษมุมพับ
// มีจุดสามจุดรอบๆ ต่อเข้าหน้ากระดาษ สีหลักตรงกับ --accent / --accent2
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var (
	colAccent  = [3]float64{0x5b, 0x7c, 0xff}
	colAccent2 = [3]float64{0x8f, 0x6b, 0xff}
)

type style int

const (
	styleRounded style = iota
	styleFull
	styleTemplate
)

type spec struct {
	file  string
	dir   string
	size  int
	style style
	glyph float64
}

var specs = []spec{
	{"icon-192.png", "web/icons", 192, styleRounded, 0.74},
	{"icon-512.png", "web/icons", 512, styleRounded, 0.74},
	{"icon-maskable-512.png", "web/icons", 512, styleFull, 0.58},
	{"apple-touch-icon.png", "web/icons", 180, styleFull, 0.66},
	{"MenuTemplate.png", "desktop/LanShareMenu/Resources", 18, styleTemplate, 0.92},
	{"MenuTemplate@2x.png", "desktop/LanShareMenu/Resources", 36, styleTemplate, 0.92},
	{"icon_16x16.png", "desktop/LanShareMenu/icon.iconset", 16, styleRounded, 0.78},
	{"icon_16x16@2x.png", "desktop/LanShareMenu/icon.iconset", 32, styleRounded, 0.78},
	{"icon_32x32.png", "desktop/LanShareMenu/icon.iconset", 32, styleRounded, 0.76},
	{"icon_32x32@2x.png", "desktop/LanShareMenu/icon.iconset", 64, styleRounded, 0.76},
	{"icon_128x128.png", "desktop/LanShareMenu/icon.iconset", 128, styleRounded, 0.74},
	{"icon_128x128@2x.png", "desktop/LanShareMenu/icon.iconset", 256, styleRounded, 0.74},
	{"icon_256x256.png", "desktop/LanShareMenu/icon.iconset", 256, styleRounded, 0.74},
	{"icon_256x256@2x.png", "desktop/LanShareMenu/icon.iconset", 512, styleRounded, 0.74},
	{"icon_512x512.png", "desktop/LanShareMenu/icon.iconset", 512, styleRounded, 0.74},
	{"icon_512x512@2x.png", "desktop/LanShareMenu/icon.iconset", 1024, styleRounded, 0.74},
	{"AppIcon.png", "mobile/ios/ShareGroupHost/Assets.xcassets/AppIcon.appiconset", 1024, styleFull, 0.66},
}

// พิกัด 0..1 ในกล่องสัญลักษณ์: หน้ากระดาษตรงกลาง จุดสามจุดรอบนอก
var (
	docX0, docY0 = 0.355, 0.265
	docX1, docY1 = 0.645, 0.735
	docR         = 0.04
	fold         = 0.125
	nodes        = [3][2]float64{{0.50, 0.105}, {0.12, 0.84}, {0.88, 0.84}}
	anchors      = [3][2]float64{{0.50, 0.255}, {0.34, 0.745}, {0.66, 0.745}}
	nodeR        = 0.078
	lineHW       = 0.024
)

func main() {
	for _, s := range specs {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "สร้างโฟลเดอร์ไม่สำเร็จ:", err)
			os.Exit(1)
		}
		path := filepath.Join(s.dir, s.file)
		if err := write(path, render(s)); err != nil {
			fmt.Fprintln(os.Stderr, "เขียนไฟล์ไม่สำเร็จ:", err)
			os.Exit(1)
		}
		fmt.Printf("%s %dx%d\n", path, s.size, s.size)
	}
}

func write(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

const ss = 4

func render(s spec) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, s.size, s.size))
	n := float64(s.size)
	for y := 0; y < s.size; y++ {
		for x := 0; x < s.size; x++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					u := (float64(x) + (float64(sx)+0.5)/ss) / n
					v := (float64(y) + (float64(sy)+0.5)/ss) / n
					pr, pg, pb, pa := sample(s, u, v)
					r += pr * pa
					g += pg * pa
					b += pb * pa
					a += pa
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(math.Round(r / a)),
				G: uint8(math.Round(g / a)),
				B: uint8(math.Round(b / a)),
				A: uint8(math.Round(255 * a / (ss * ss))),
			})
		}
	}
	return img
}

func sample(s spec, u, v float64) (r, g, b, a float64) {
	on := inGlyph(u, v, s.glyph)
	if s.style == styleTemplate {
		if !on {
			return 0, 0, 0, 0
		}
		return 0, 0, 0, 1
	}
	if s.style == styleRounded && !inRoundedSquare(u, v, 0.22) {
		return 0, 0, 0, 0
	}
	t := (u + v) / 2
	r = colAccent[0] + (colAccent2[0]-colAccent[0])*t
	g = colAccent[1] + (colAccent2[1]-colAccent[1])*t
	b = colAccent[2] + (colAccent2[2]-colAccent[2])*t
	if on {
		r, g, b = 255, 255, 255
	}
	return r, g, b, 1
}

func inRoundedSquare(u, v, radius float64) bool {
	cx := math.Max(math.Abs(u-0.5)-(0.5-radius), 0)
	cy := math.Max(math.Abs(v-0.5)-(0.5-radius), 0)
	return cx*cx+cy*cy <= radius*radius
}

func inGlyph(u, v, scale float64) bool {
	gx := (u-0.5)/scale + 0.5
	gy := (v-0.5)/scale + 0.5
	for _, n := range nodes {
		if math.Hypot(gx-n[0], gy-n[1]) <= nodeR {
			return true
		}
	}
	for i, n := range nodes {
		a := anchors[i]
		if distSegment(gx, gy, n[0], n[1], a[0], a[1]) <= lineHW {
			return true
		}
	}
	return inPage(gx, gy)
}

func inPage(gx, gy float64) bool {
	if inTriangle(gx, gy, docX1-fold, docY0, docX1, docY0, docX1, docY0+fold) {
		return false
	}
	return inRoundedRect(gx, gy, docX0, docY0, docX1, docY1, docR)
}

func inRoundedRect(px, py, x0, y0, x1, y1, r float64) bool {
	hw := (x1 - x0) / 2
	hh := (y1 - y0) / 2
	cx := math.Max(math.Abs(px-(x0+x1)/2)-(hw-r), 0)
	cy := math.Max(math.Abs(py-(y0+y1)/2)-(hh-r), 0)
	return cx*cx+cy*cy <= r*r
}

func inTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	v0x, v0y := cx-ax, cy-ay
	v1x, v1y := bx-ax, by-ay
	v2x, v2y := px-ax, py-ay
	dot00 := v0x*v0x + v0y*v0y
	dot01 := v0x*v1x + v0y*v1y
	dot02 := v0x*v2x + v0y*v2y
	dot11 := v1x*v1x + v1y*v1y
	dot12 := v1x*v2x + v1y*v2y
	den := dot00*dot11 - dot01*dot01
	if den == 0 {
		return false
	}
	inv := 1 / den
	u := (dot11*dot02 - dot01*dot12) * inv
	v := (dot00*dot12 - dot01*dot02) * inv
	return u >= 0 && v >= 0 && u+v <= 1
}

func distSegment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	den := dx*dx + dy*dy
	if den == 0 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / den
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

//go:build ignore

// genicons สร้างไอคอน PWA ของ LAN Share เป็นไฟล์ PNG ใน web/icons/
//
// รันจากรากของโปรเจกต์ครั้งเดียวแล้ว commit ไฟล์ PNG ที่ได้:
//
//	go run tools/genicons/main.go
//
// ไม่มี dependency ภายนอก ใช้เฉพาะ image/png ภาพวาดด้วย signed distance
// field พร้อม supersampling เพื่อให้ขอบเรียบ ผลลัพธ์เหมือนเดิมทุกครั้งที่รัน
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

// สีหลักต้องตรงกับตัวแปร --bg, --accent และ --accent2 ใน web/style.css
var (
	colAccent  = [3]float64{0x5b, 0x7c, 0xff}
	colAccent2 = [3]float64{0x8f, 0x6b, 0xff}
)

type style int

const (
	// styleRounded: สี่เหลี่ยมมุมมน มุมโปร่งใส สำหรับ purpose "any"
	styleRounded style = iota
	// styleFull: พื้นหลังเต็มภาพไม่มีความโปร่งใส สำหรับ maskable และ apple-touch
	// (ระบบปฏิบัติการจะตัดมุมเอง ตัวสัญลักษณ์ต้องอยู่ใน safe zone ตรงกลาง)
	styleFull
)

type spec struct {
	file  string
	size  int
	style style
	// glyph คือสัดส่วนของสัญลักษณ์เทียบกับภาพ
	glyph float64
}

var specs = []spec{
	{"icon-192.png", 192, styleRounded, 0.72},
	{"icon-512.png", 512, styleRounded, 0.72},
	{"icon-maskable-512.png", 512, styleFull, 0.58},
	{"apple-touch-icon.png", 180, styleFull, 0.66},
}

// สัญลักษณ์ "แชร์": จุดสามจุดต่อกันด้วยเส้น พิกัด 0..1 ภายในกล่องสัญลักษณ์
var (
	nodes  = [3][2]float64{{0.80, 0.20}, {0.80, 0.80}, {0.20, 0.50}}
	nodeR  = 0.14
	lineHW = 0.035
)

func main() {
	dir := filepath.Join("web", "icons")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "สร้างโฟลเดอร์ไม่สำเร็จ:", err)
		os.Exit(1)
	}
	for _, s := range specs {
		path := filepath.Join(dir, s.file)
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

const ss = 4 // supersampling ต่อแกน

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

// sample คืนสีและความทึบของจุด (u, v) ในช่วง 0..1
func sample(s spec, u, v float64) (r, g, b, a float64) {
	if s.style == styleRounded && !inRoundedSquare(u, v, 0.22) {
		return 0, 0, 0, 0
	}
	t := (u + v) / 2
	r = colAccent[0] + (colAccent2[0]-colAccent[0])*t
	g = colAccent[1] + (colAccent2[1]-colAccent[1])*t
	b = colAccent[2] + (colAccent2[2]-colAccent[2])*t
	if inGlyph(u, v, s.glyph) {
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
	// แปลงพิกัดภาพเป็นพิกัดในกล่องสัญลักษณ์
	gx := (u-0.5)/scale + 0.5
	gy := (v-0.5)/scale + 0.5
	for _, n := range nodes {
		if math.Hypot(gx-n[0], gy-n[1]) <= nodeR {
			return true
		}
	}
	c := nodes[2]
	for _, n := range nodes[:2] {
		if distSegment(gx, gy, c[0], c[1], n[0], n[1]) <= lineHW {
			return true
		}
	}
	return false
}

func distSegment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := ((px-ax)*dx + (py-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

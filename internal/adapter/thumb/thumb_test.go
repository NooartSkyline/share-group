package thumb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"lanshare/internal/domain"
)

func gradientJPEG(t *testing.T, w, h int, exif []byte) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// red on the left half, blue on the right half
			if x < w/2 {
				img.Set(x, y, color.RGBA{R: 255, A: 255})
			} else {
				img.Set(x, y, color.RGBA{B: 255, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if exif == nil {
		return data
	}
	// insert an APP1 segment right after SOI
	seg := append([]byte{0xFF, 0xE1}, 0, 0)
	binary.BigEndian.PutUint16(seg[2:], uint16(2+len(exif)))
	seg = append(seg, exif...)
	return append(append(append([]byte{}, data[:2]...), seg...), data[2:]...)
}

// exifWithOrientation builds a minimal big-endian EXIF block with one Orientation tag.
func exifWithOrientation(o uint16) []byte {
	b := []byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08")
	b = append(b, 0, 1) // one IFD entry
	entry := make([]byte, 12)
	binary.BigEndian.PutUint16(entry[0:], 0x0112)
	binary.BigEndian.PutUint16(entry[2:], 3) // SHORT
	binary.BigEndian.PutUint32(entry[4:], 1)
	binary.BigEndian.PutUint16(entry[8:], o)
	b = append(b, entry...)
	return append(b, 0, 0, 0, 0)
}

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("output is not a JPEG: %v", err)
	}
	return img
}

func TestScalesDownKeepingAspect(t *testing.T) {
	out, err := New().Render(bytes.NewReader(gradientJPEG(t, 2000, 1000, nil)), 360)
	if err != nil {
		t.Fatal(err)
	}
	b := decode(t, out).Bounds()
	if b.Dx() != 360 || b.Dy() != 180 {
		t.Fatalf("thumbnail is %dx%d, want 360x180", b.Dx(), b.Dy())
	}
}

func TestDoesNotUpscale(t *testing.T) {
	out, err := New().Render(bytes.NewReader(gradientJPEG(t, 100, 50, nil)), 360)
	if err != nil {
		t.Fatal(err)
	}
	if b := decode(t, out).Bounds(); b.Dx() != 100 || b.Dy() != 50 {
		t.Fatalf("small image became %dx%d", b.Dx(), b.Dy())
	}
}

func TestPortraitPhotoWithExifRotation(t *testing.T) {
	// A landscape sensor image (400x200) tagged "rotate 90 CW" is a portrait photo.
	src := gradientJPEG(t, 400, 200, exifWithOrientation(6))
	out, err := New().Render(bytes.NewReader(src), 100)
	if err != nil {
		t.Fatal(err)
	}
	img := decode(t, out)
	if b := img.Bounds(); b.Dx() != 50 || b.Dy() != 100 {
		t.Fatalf("rotated thumbnail is %dx%d, want 50x100", b.Dx(), b.Dy())
	}
	// Left half was red; after rotating 90 degrees clockwise it is the top half.
	top := color.RGBAModel.Convert(img.At(25, 10)).(color.RGBA)
	bottom := color.RGBAModel.Convert(img.At(25, 90)).(color.RGBA)
	if top.R < 200 || top.B > 60 || bottom.B < 200 || bottom.R > 60 {
		t.Fatalf("wrong orientation: top=%v bottom=%v", top, bottom)
	}
}

func TestOrientationMappings(t *testing.T) {
	// 2x1 image: left pixel red, right pixel blue.
	src := image.NewRGBA(image.Rect(0, 0, 2, 1))
	red, blue := color.RGBA{R: 255, A: 255}, color.RGBA{B: 255, A: 255}
	src.SetRGBA(0, 0, red)
	src.SetRGBA(1, 0, blue)
	cases := []struct {
		o          int
		w, h       int
		at00, atXY color.RGBA
		x, y       int
	}{
		{1, 2, 1, red, blue, 1, 0},
		{2, 2, 1, blue, red, 1, 0},  // mirror
		{3, 2, 1, blue, red, 1, 0},  // 180
		{6, 1, 2, red, blue, 0, 1},  // 90 CW: left becomes top
		{8, 1, 2, blue, red, 0, 1},  // 90 CCW: left becomes bottom
		{5, 1, 2, red, blue, 0, 1},  // transpose
		{7, 1, 2, blue, red, 0, 1},  // transverse
		{4, 2, 1, red, blue, 1, 0},  // vertical flip of a 1px high image
		{99, 2, 1, red, blue, 1, 0}, // unknown: unchanged
	}
	for _, c := range cases {
		got := orient(src, c.o)
		if got.Bounds().Dx() != c.w || got.Bounds().Dy() != c.h {
			t.Errorf("orientation %d: size %v, want %dx%d", c.o, got.Bounds(), c.w, c.h)
			continue
		}
		if got.RGBAAt(0, 0) != c.at00 || got.RGBAAt(c.x, c.y) != c.atXY {
			t.Errorf("orientation %d: pixels (0,0)=%v (%d,%d)=%v", c.o, got.RGBAAt(0, 0), c.x, c.y, got.RGBAAt(c.x, c.y))
		}
	}
}

func TestPNGWithTransparencyBecomesWhiteNotBlack(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10)) // fully transparent
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	out, err := New().Render(&buf, 360)
	if err != nil {
		t.Fatal(err)
	}
	c := color.RGBAModel.Convert(decode(t, out).At(5, 5)).(color.RGBA)
	if c.R < 240 || c.G < 240 || c.B < 240 {
		t.Fatalf("transparent pixel became %v", c)
	}
}

func TestRejectsGarbageAndBombs(t *testing.T) {
	r := New()
	if _, err := r.Render(strings.NewReader("this is not an image"), 360); !errors.Is(err, domain.ErrUnsupported) {
		t.Errorf("garbage: %v", err)
	}
	// A valid PNG header declaring 60000x60000 pixels must be refused before decoding.
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	b := buf.Bytes()
	binary.BigEndian.PutUint32(b[16:], 60000) // IHDR width
	binary.BigEndian.PutUint32(b[20:], 60000) // IHDR height
	if _, err := r.Render(bytes.NewReader(b), 360); !errors.Is(err, domain.ErrUnsupported) {
		t.Errorf("decompression bomb: %v", err)
	}
}

func TestExifParserIgnoresMalformedData(t *testing.T) {
	for _, data := range [][]byte{
		nil, {0xFF}, {0xFF, 0xD8}, {0xFF, 0xD8, 0xFF, 0xE1, 0xFF, 0xFF},
		{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x08, 'E', 'x', 'i', 'f', 0, 0},
	} {
		if got := exifOrientation(data); got != 1 {
			t.Errorf("exifOrientation(%v) = %d, want 1", data, got)
		}
	}
}

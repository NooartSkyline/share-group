package heicconv

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"lanshare/internal/domain"
)

type outWriter struct{}

func (outWriter) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != sipsPath {
		return nil, domain.ErrUnsupported
	}
	out := ""
	for i, a := range args {
		if a == "--out" && i+1 < len(args) {
			out = args[i+1]
			break
		}
	}
	if out == "" {
		return nil, domain.ErrUnsupported
	}
	return nil, os.WriteFile(out, []byte("fake-jpeg-bytes"), 0o600)
}

func TestConvertWritesJPEGAndCleansTemp(t *testing.T) {
	c := New(outWriter{})
	name, body, size, err := c.Convert(context.Background(), "IMG.HEIC", strings.NewReader("heic"))
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if name != "IMG.jpg" || size != 15 {
		t.Fatalf("name=%q size=%d", name, size)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fake-jpeg-bytes" {
		t.Fatalf("jpeg = %q", got)
	}
}

func TestConvertRejectsNonHEIC(t *testing.T) {
	c := New(outWriter{})
	_, _, _, err := c.Convert(context.Background(), "a.jpg", bytes.NewReader([]byte("x")))
	if err == nil {
		t.Fatal("expected error")
	}
}

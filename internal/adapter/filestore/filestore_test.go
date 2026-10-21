package filestore_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lanshare/internal/adapter/filestore"
	"lanshare/internal/domain"
)

type brokenReader struct{ sent bool }

func (b *brokenReader) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, "partial data"), nil
	}
	return 0, errors.New("connection reset")
}

func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func TestPutAndOpen(t *testing.T) {
	dir := t.TempDir()
	s := filestore.New(dir)
	ctx := context.Background()

	n, err := s.Put(ctx, "main", "abc123", strings.NewReader("hello world"))
	if err != nil || n != 11 {
		t.Fatalf("Put = %d, %v", n, err)
	}
	f, err := s.Open(ctx, "main", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if b, _ := io.ReadAll(f); string(b) != "hello world" {
		t.Fatalf("content = %q", b)
	}
	if _, err := f.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(f); string(b) != "world" {
		t.Fatalf("after seek = %q", b)
	}
}

func TestFailedPutLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	s := filestore.New(dir)
	if _, err := s.Put(context.Background(), "main", "abc123", &brokenReader{}); err == nil {
		t.Fatal("expected error")
	}
	if left := listFiles(t, dir); len(left) != 0 {
		t.Fatalf("partial upload left files behind: %v", left)
	}
	if _, err := s.Open(context.Background(), "main", "abc123"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Open after failed Put: %v", err)
	}
}

func TestRejectsPathTraversalIDs(t *testing.T) {
	s := filestore.New(t.TempDir())
	ctx := context.Background()
	for _, id := range []string{"../x", "a/b", "", "..", "a b"} {
		if _, err := s.Put(ctx, "main", domain.FileID(id), strings.NewReader("x")); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("file id %q accepted: %v", id, err)
		}
	}
	if _, err := s.Put(ctx, "../evil", "abc", strings.NewReader("x")); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("room id traversal accepted: %v", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	s := filestore.New(t.TempDir())
	ctx := context.Background()
	_, _ = s.Put(ctx, "main", "abc", strings.NewReader("x"))
	if err := s.Delete(ctx, "main", "abc"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "main", "abc"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

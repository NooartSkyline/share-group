// Package heicconv turns HEIC/HEIF pictures into JPEG with macOS sips.
package heicconv

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

const (
	sipsPath  = "/usr/bin/sips"
	maxInput  = 512 << 20
	maxOutput = 64 << 20
	timeout   = 45 * time.Second
)

var safeExt = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// Converter implements port.HEICConverter.
type Converter struct {
	run port.CommandRunner
}

func New(run port.CommandRunner) *Converter { return &Converter{run: run} }

func Available() bool {
	_, err := os.Stat(sipsPath)
	return err == nil
}

func (c *Converter) Convert(ctx context.Context, name string, src io.Reader) (string, io.ReadCloser, int64, error) {
	if c == nil || c.run == nil || !domain.IsHEIC(name) {
		return "", nil, 0, domain.ErrUnsupported
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if !safeExt.MatchString(ext) {
		return "", nil, 0, domain.ErrUnsupported
	}
	dir, err := os.MkdirTemp("", "lanshare-heic-")
	if err != nil {
		return "", nil, 0, fmt.Errorf("heic temp: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()

	in := filepath.Join(dir, "in."+ext)
	if err := copyCapped(in, src, maxInput); err != nil {
		return "", nil, 0, err
	}
	out := filepath.Join(dir, "out.jpg")
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := c.run.Run(cctx, sipsPath, "-s", "format", "jpeg", "-s", "formatOptions", "80", in, "--out", out); err != nil {
		return "", nil, 0, fmt.Errorf("%w: sips: %v", domain.ErrUnsupported, err)
	}
	f, err := os.Open(out)
	if err != nil {
		return "", nil, 0, fmt.Errorf("%w: jpeg missing", domain.ErrUnsupported)
	}
	st, err := f.Stat()
	if err != nil || st.Size() == 0 || st.Size() > maxOutput {
		_ = f.Close()
		return "", nil, 0, fmt.Errorf("%w: jpeg unusable", domain.ErrUnsupported)
	}
	cleanup = false
	return domain.JPEGNameFromHEIC(path.Base(name)), &removeOnClose{File: f, dir: dir}, st.Size(), nil
}

type removeOnClose struct {
	*os.File
	dir string
}

func (r *removeOnClose) Close() error {
	err := r.File.Close()
	_ = os.RemoveAll(r.dir)
	return err
}

func copyCapped(dst string, src io.Reader, max int64) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(src, max+1))
	cerr := f.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if n > max {
		return fmt.Errorf("%w: heic too large", domain.ErrUnsupported)
	}
	return nil
}

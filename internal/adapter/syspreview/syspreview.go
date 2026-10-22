// Package syspreview draws preview pictures with the tools macOS ships:
// sips converts pictures (HEIC, camera RAW, TIFF, PDF ...) and Quick Look
// (qlmanage) draws the first page of documents, text, fonts and the like.
//
// Invariants: the commands are started from a fixed argv with absolute paths;
// the uploaded file is copied to a private temporary folder under a name the
// server chose ("in" plus a validated extension), so its own name never
// reaches a command line; every run is bounded by a timeout and the temporary
// folder is always removed. Anything that goes wrong, including a tool that
// is missing (not macOS), is reported as domain.ErrUnsupported so the caller
// falls back to an icon.
package syspreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lanshare/internal/domain"
	"lanshare/internal/usecase/port"
)

const (
	sipsPath = "/usr/bin/sips"
	qlPath   = "/usr/bin/qlmanage"

	// maxInput bounds the copy made for the tools; larger files get an icon.
	maxInput = 512 << 20
	// maxOutput bounds the picture read back.
	maxOutput = 32 << 20

	imageTimeout    = 20 * time.Second
	documentTimeout = 12 * time.Second
)

var safeExt = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// Previewer implements port.SystemPreviewer.
type Previewer struct {
	run port.CommandRunner
}

var _ port.SystemPreviewer = (*Previewer)(nil)

// New returns a Previewer that starts its commands through run.
func New(run port.CommandRunner) *Previewer { return &Previewer{run: run} }

// Available reports whether the macOS tools are installed.
func Available() bool {
	for _, p := range []string{sipsPath, qlPath} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// Preview implements port.SystemPreviewer.
func (p *Previewer) Preview(ctx context.Context, name string, kind domain.PreviewKind, src io.Reader, maxDim int) ([]byte, error) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if !safeExt.MatchString(ext) || maxDim < 16 {
		return nil, unsupported("bad name")
	}
	if kind != domain.PreviewImage && kind != domain.PreviewDocument {
		return nil, unsupported("kind " + string(kind))
	}

	dir, err := os.MkdirTemp("", "lanshare-preview-")
	if err != nil {
		return nil, fmt.Errorf("create preview folder: %w", err)
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "in."+ext)
	if err := copyLimited(in, src); err != nil {
		return nil, err
	}

	var out string
	switch kind {
	case domain.PreviewImage:
		out = filepath.Join(dir, "out.jpg")
		err = p.runTool(ctx, imageTimeout, sipsPath,
			"-s", "format", "jpeg", "-s", "formatOptions", "80", "-Z", strconv.Itoa(maxDim), in, "--out", out)
	default:
		// qlmanage names its result after the input file: in.<ext>.png
		out = in + ".png"
		err = p.runTool(ctx, documentTimeout, qlPath, "-t", "-s", strconv.Itoa(maxDim), "-o", dir, in)
	}
	if err != nil {
		return nil, err
	}
	return readLimited(out)
}

// runTool runs one command with its own timeout. A parent context that ended
// is returned as is; any other failure means "no preview".
func (p *Previewer) runTool(ctx context.Context, timeout time.Duration, tool string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := p.run.Run(cctx, tool, args...); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("preview: %w", ctx.Err())
		}
		return unsupported(filepath.Base(tool) + " failed")
	}
	return nil
}

func copyLimited(dst string, src io.Reader) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create preview input: %w", err)
	}
	n, err := io.Copy(f, io.LimitReader(src, maxInput+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("copy preview input: %w", err)
	}
	if n > maxInput {
		return unsupported("file larger than the preview limit")
	}
	return nil
}

func readLimited(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, unsupported("no picture produced")
		}
		return nil, fmt.Errorf("open preview output: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxOutput+1))
	if err != nil {
		return nil, fmt.Errorf("read preview output: %w", err)
	}
	if len(b) == 0 || len(b) > maxOutput {
		return nil, unsupported("unusable picture")
	}
	return b, nil
}

func unsupported(why string) error {
	return fmt.Errorf("%w: preview: %s", domain.ErrUnsupported, why)
}

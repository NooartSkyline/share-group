package domain

import (
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// FileID identifies a stored file inside a room.
type FileID string

// File is the metadata of a file shared in a room.
type File struct {
	ID     FileID
	RoomID RoomID
	// Dir is the sanitized folder the file was sent in ("" or "photos/2026"),
	// always '/' separated, so folders survive upload and zip download.
	Dir          string
	Name         string
	Size         int64
	UploaderID   DeviceID
	UploaderName string
	CreatedAt    time.Time
}

// Path is the display path of the file: Dir and Name joined.
func (f File) Path() string {
	if f.Dir == "" {
		return f.Name
	}
	return f.Dir + "/" + f.Name
}

const (
	maxNameRunes     = 200
	maxDirDepth      = 12
	fallbackFileName = "file"
)

// cleanSegment makes one path segment safe: no control characters, no
// leading/trailing dots or spaces, bounded length. It returns "" when nothing
// usable is left (for example "..").
func cleanSegment(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(s, " .")
	if utf8.RuneCountInString(s) > maxNameRunes {
		s = string([]rune(s)[:maxNameRunes])
	}
	return s
}

// SanitizeFileName returns a safe display name: no directories, no control
// characters, no leading/trailing dots or spaces, bounded length.
func SanitizeFileName(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name = cleanSegment(name); name == "" {
		return fallbackFileName
	}
	return name
}

// SanitizeDir normalizes a client supplied folder path. Empty, "." and ".."
// segments are dropped, so the result can never climb out of its folder.
func SanitizeDir(dir string) string {
	dir = strings.ReplaceAll(dir, `\`, "/")
	var parts []string
	for _, seg := range strings.Split(dir, "/") {
		if seg = cleanSegment(seg); seg == "" {
			continue
		}
		parts = append(parts, seg)
		if len(parts) == maxDirDepth {
			break
		}
	}
	return strings.Join(parts, "/")
}

// UniqueName returns name, or "name (2).ext", "name (3).ext"... for the first
// variant that taken reports as free. Comparison is case-insensitive.
func UniqueName(name string, taken func(candidate string) bool) string {
	if !taken(name) {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if base == "" { // a dotfile such as ".env"
		base, ext = name, ""
	}
	for i := 2; ; i++ {
		cand := base + " (" + strconv.Itoa(i) + ")" + ext
		if !taken(cand) {
			return cand
		}
	}
}

// FileKind groups files for display.
type FileKind string

const (
	KindImage    FileKind = "image"
	KindVideo    FileKind = "video"
	KindAudio    FileKind = "audio"
	KindDocument FileKind = "document"
	KindArchive  FileKind = "archive"
	KindOther    FileKind = "other"
)

var kindByExt = map[string]FileKind{}

func init() {
	add := func(k FileKind, exts ...string) {
		for _, e := range exts {
			kindByExt[e] = k
		}
	}
	add(KindImage, "jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "bmp", "tif", "tiff", "svg")
	add(KindVideo, "mp4", "mov", "m4v", "webm", "mkv", "avi", "3gp")
	add(KindAudio, "mp3", "wav", "m4a", "flac", "aac", "ogg", "opus")
	add(KindDocument, "pdf", "doc", "docx", "txt", "md", "rtf", "xls", "xlsx", "csv", "ppt", "pptx", "key", "pages", "numbers")
	add(KindArchive, "zip", "rar", "7z", "tar", "gz", "tgz", "dmg")
}

// KindOf classifies a file by its extension.
func KindOf(name string) FileKind {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if k, ok := kindByExt[ext]; ok {
		return k
	}
	return KindOther
}

// CanThumbnail reports whether the server can render a thumbnail for name.
// Only formats decoded by memory-safe Go decoders are supported.
func CanThumbnail(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) {
	case "jpg", "jpeg", "png", "gif", "webp":
		return true
	}
	return false
}

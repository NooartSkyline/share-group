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
	// SHA256 is the lowercase hex digest of the content, or "" when it has not
	// been computed (files stored before checksums existed).
	SHA256 string
	// Integrity is how SHA256 was checked; the zero value means unknown.
	Integrity Integrity
}

// IntegrityOrUnknown returns Integrity, treating the zero value and any
// unrecognized value as IntegrityUnknown so callers never expose a blank or
// made-up status.
func (f File) IntegrityOrUnknown() Integrity {
	switch f.Integrity {
	case IntegrityVerified, IntegrityUnverified:
		return f.Integrity
	}
	return IntegrityUnknown
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

// rawExts are camera RAW formats. They are images even though browsers cannot show them.
var rawExts = []string{
	"cr2", "cr3", "crw", "nef", "nrw", "arw", "srf", "sr2", "dng", "orf", "rw2", "raf", "pef", "srw",
	"raw", "3fr", "erf", "kdc", "mrw", "mef", "rwl", "iiq",
}

// codeExts are source and config files; Quick Look draws them as text pages.
var codeExts = []string{
	"css", "js", "ts", "py", "go", "c", "h", "cpp", "java", "swift", "sh", "rb", "sql", "php", "rs", "kt", "toml", "ini",
}

func init() {
	add := func(k FileKind, exts ...string) {
		for _, e := range exts {
			kindByExt[e] = k
		}
	}
	add(KindImage, "jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "avif", "jxl", "bmp", "tif", "tiff", "svg",
		"ico", "icns", "psd", "jp2", "tga", "dds")
	add(KindImage, rawExts...)
	add(KindVideo, "mp4", "mov", "m4v", "webm", "mkv", "avi", "3gp", "wmv", "flv", "mpg", "mpeg", "mts", "m2ts")
	add(KindAudio, "mp3", "wav", "m4a", "flac", "aac", "ogg", "opus", "aif", "aiff", "wma", "caf", "mid", "midi")
	add(KindDocument, "pdf", "doc", "docx", "txt", "md", "rtf", "xls", "xlsx", "csv", "tsv", "ppt", "pptx", "key", "pages", "numbers",
		"odt", "ods", "odp", "epub", "json", "xml", "yaml", "yml", "log")
	add(KindDocument, codeExts...)
	add(KindArchive, "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "zst", "dmg", "iso", "pkg")
}

// KindOf classifies a file by its extension.
func KindOf(name string) FileKind {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if k, ok := kindByExt[ext]; ok {
		return k
	}
	return KindOther
}

// CanThumbnail reports whether name is decoded by the memory-safe Go decoders
// alone. PreviewOf also covers formats that need the host operating system.
func CanThumbnail(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) {
	case "jpg", "jpeg", "png", "gif", "webp":
		return true
	}
	return false
}

// PreviewKind says how the server can turn a file into a preview image.
type PreviewKind string

const (
	// PreviewNone: no preview image; the client shows an icon.
	PreviewNone PreviewKind = ""
	// PreviewNative: JPEG, PNG, GIF and WebP, decoded in-process.
	PreviewNative PreviewKind = "native"
	// PreviewImage: other pictures (HEIC, camera RAW, TIFF, PDF ...) converted by the host OS.
	PreviewImage PreviewKind = "image"
	// PreviewDocument: files the host OS draws a first page of (Office, text, code, fonts, SVG ...).
	PreviewDocument PreviewKind = "document"
)

var previewByExt = map[string]PreviewKind{}

func init() {
	add := func(k PreviewKind, exts ...string) {
		for _, e := range exts {
			previewByExt[e] = k
		}
	}
	add(PreviewNative, "jpg", "jpeg", "png", "gif", "webp")
	add(PreviewImage, "heic", "heif", "avif", "jxl", "bmp", "tif", "tiff", "ico", "icns", "psd", "jp2", "tga", "dds", "pdf")
	add(PreviewImage, rawExts...)
	add(PreviewDocument, "doc", "docx", "xls", "xlsx", "ppt", "pptx", "pages", "numbers", "key", "rtf", "txt", "md", "csv", "tsv",
		"json", "xml", "yaml", "yml", "log", "svg", "ttf", "otf", "eps", "ai", "epub")
	add(PreviewDocument, codeExts...)
}

// PreviewOf says how a preview image of name can be made. Videos are not
// listed: browsers draw their first frame themselves.
func PreviewOf(name string) PreviewKind {
	return previewByExt[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

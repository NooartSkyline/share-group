package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Files of the PWA are served by dedicated handlers instead of the generic
// file server so that their Content-Type never depends on the host's mime
// database and so that sw.js can carry a version derived from the app shell.
// They are public static files, like the rest of the web UI: no token is
// needed and nothing here reads room data.

const (
	shellVersionPlaceholder = "__SHELL_VERSION__"
	shellVersionLen         = 12
)

var (
	allowListRE = regexp.MustCompile(`(?s)\bALLOW\s*=\s*\[(.*?)\]`)
	quotedRE    = regexp.MustCompile(`'([^']*)'|"([^"]*)"`)
)

// registerPWA wires /manifest.webmanifest, /sw.js and /icons/{name} onto mux.
// Every response is Cache-Control: no-cache, so a reachable server is always
// asked to revalidate and never serves a stale shell.
func registerPWA(mux *http.ServeMux, web fs.FS) {
	if web == nil {
		return
	}
	mux.HandleFunc("GET /manifest.webmanifest", serveEmbedded(web, "manifest.webmanifest", "application/manifest+json"))
	mux.HandleFunc("GET /icons/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if !strings.HasSuffix(name, ".png") {
			http.NotFound(w, r)
			return
		}
		serveEmbedded(web, "icons/"+name, "image/png")(w, r)
	})
	if body, ok := versionedServiceWorker(web); ok {
		mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
			writeStatic(w, r, "sw.js", "text/javascript", body)
		})
	}
}

func serveEmbedded(web fs.FS, name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		body, err := fs.ReadFile(web, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeStatic(w, r, name, contentType, body)
	}
}

func writeStatic(w http.ResponseWriter, r *http.Request, name, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
}

// versionedServiceWorker reads sw.js and replaces the placeholder with the
// shell version. It runs once, when the router is built. ok is false when
// sw.js is not in web, in which case the route is left to the file server.
func versionedServiceWorker(web fs.FS) (body []byte, ok bool) {
	src, err := fs.ReadFile(web, "sw.js")
	if err != nil {
		return nil, false
	}
	version := shellVersion(web, allowListOf(src))
	return bytes.ReplaceAll(src, []byte(shellVersionPlaceholder), []byte(version)), true
}

// allowListOf extracts the URL paths of the ALLOW array declared in sw.js, so
// the version follows the very list the worker caches from.
func allowListOf(sw []byte) []string {
	m := allowListRE.FindSubmatch(sw)
	if m == nil {
		return nil
	}
	var paths []string
	for _, q := range quotedRE.FindAllSubmatch(m[1], -1) {
		if len(q[1]) > 0 {
			paths = append(paths, string(q[1]))
		} else if len(q[2]) > 0 {
			paths = append(paths, string(q[2]))
		}
	}
	return paths
}

// shellVersion is the first 12 hex digits of the SHA-256 over the bytes of the
// allow-listed files, concatenated in file-name order. "/" is index.html and
// listed paths missing from web contribute nothing. The result is
// deterministic and changes whenever any contributing file changes.
func shellVersion(web fs.FS, allow []string) string {
	seen := make(map[string]bool, len(allow))
	var names []string
	for _, p := range allow {
		name := strings.TrimPrefix(path.Clean("/"+p), "/")
		if name == "" {
			name = "index.html"
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		if !fs.ValidPath(name) {
			continue
		}
		if b, err := fs.ReadFile(web, name); err == nil {
			sum.Write(b)
		}
	}
	return hex.EncodeToString(sum.Sum(nil))[:shellVersionLen]
}

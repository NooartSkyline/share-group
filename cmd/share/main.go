// Command share talks to a LAN Share server on this Mac.
//
//	share send photo.jpg
//	share send -port 9000 ./folder/report.pdf
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return nil
	}
	switch args[0] {
	case "send":
		return send(args[1:])
	case "-h", "-help", "help":
		fmt.Fprint(os.Stderr, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

const usage = `share — ส่งไฟล์เข้า LAN Share จากเครื่องนี้

คำสั่ง:
  share send [-port 8080] <ไฟล์...>

ตัวอย่าง:
  share send photo.jpg
  share send -port 9000 ./report.pdf ./notes.txt
`

func send(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	port := fs.Int("port", 8080, "port of the LAN Share server on this Mac")
	if err := fs.Parse(args); err != nil {
		return err
	}
	files := fs.Args()
	if len(files) == 0 {
		return fmt.Errorf("send: missing file\n%s", usage)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	c := &http.Client{Jar: jar}
	base := fmt.Sprintf("http://127.0.0.1:%d", *port)
	if err := ownerSession(c, base); err != nil {
		return err
	}
	for _, path := range files {
		if err := uploadFile(c, base, path); err != nil {
			return err
		}
		fmt.Println("sent", path)
	}
	return nil
}

func ownerSession(c *http.Client, base string) error {
	req, err := http.NewRequest(http.MethodPost, base+"/api/owner/session", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-LanShare-Client", "1")
	res, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach LAN Share at %s (is the server running?): %w", base, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("owner session: HTTP %d", res.StatusCode)
	}
	return nil
}

func uploadFile(c *http.Client, base, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a folder; send files one by one", path)
	}
	u := base + "/api/files?name=" + url.QueryEscape(filepath.Base(path))
	req, err := http.NewRequest(http.MethodPost, u, f)
	if err != nil {
		return err
	}
	req.Header.Set("X-LanShare-Client", "1")
	req.ContentLength = st.Size()
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		var msg struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &msg)
		if msg.Error == "" {
			msg.Error = string(bytes.TrimSpace(body))
		}
		return fmt.Errorf("upload %s: HTTP %d %s", path, res.StatusCode, msg.Error)
	}
	return nil
}

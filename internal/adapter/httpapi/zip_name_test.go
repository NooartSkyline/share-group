package httpapi

import "testing"

func TestZipEntryName(t *testing.T) {
	used := map[string]int{}
	if got := zipEntryName("Instagram/a.jpg", used); got != "Instagram/a.jpg" {
		t.Fatalf("nested = %q", got)
	}
	if got := zipEntryName("Instagram/a.jpg", used); got != "Instagram/a (2).jpg" {
		t.Fatalf("dup nested = %q", got)
	}
	if got := zipEntryName("a.jpg", used); got != "a.jpg" {
		t.Fatalf("root = %q", got)
	}
	if got := zipEntryName("a.jpg", used); got != "a (2).jpg" {
		t.Fatalf("dup root = %q", got)
	}
	if got := zipEntryName("../x", used); got != "x" {
		t.Fatalf("dotdot = %q", got)
	}
	if got := zipEntryName("", used); got != "file" {
		t.Fatalf("empty = %q", got)
	}
}

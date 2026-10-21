//go:build darwin || linux

package diskspace

import "syscall"

// freeBytes returns Bavail*Bsize for the filesystem containing path. The field
// types differ between systems (Bsize is uint32 on darwin, int64 on linux), so
// both are converted to uint64 before multiplying.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

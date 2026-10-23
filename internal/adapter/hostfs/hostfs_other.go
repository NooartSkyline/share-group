//go:build !android

package hostfs

func detectVolumes() []volume { return nil }

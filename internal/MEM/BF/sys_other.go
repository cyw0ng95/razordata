//go:build !linux

package bf

// madviseDontNeed is a no-op on non-Linux platforms.
func madviseDontNeed(buf []byte) {}

// madviseHugePage is a no-op on non-Linux platforms. REQ000302.
func madviseHugePage(buf []byte) {}

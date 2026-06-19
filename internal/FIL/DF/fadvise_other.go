//go:build !linux

package df

// fadviseSequential is a no-op on non-Linux platforms (REQ000553).
func fadviseSequential(fd int, offset int64, length int64) error {
	return nil
}

// fadviseWillNeed is a no-op on non-Linux platforms (REQ000553).
func fadviseWillNeed(fd int, offset int64, length int64) error {
	return nil
}
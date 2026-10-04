package core

import "testing"

// setHome points the user's home at dir for one test, on every OS: os.UserHomeDir reads HOME on Unix and USERPROFILE
// on Windows.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

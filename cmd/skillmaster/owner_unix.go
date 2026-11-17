//go:build unix

package main

import (
	"os"
	"syscall"
)

// ownedByMe says whether this process's user owns the file.
//
// **`$TMPDIR` is shared on Linux** (`/tmp`), and a cache keyed by a public digest is a name anybody
// can pre-create. A directory another user made is not this machine's cache; it is their content
// wearing this machine's name.
func ownedByMe(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}

// sharedWritable reports whether a directory's write bits let someone else write into it.
func sharedWritable(mode os.FileMode) bool {
	return mode.Perm()&0o022 != 0
}

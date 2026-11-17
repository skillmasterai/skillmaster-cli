//go:build windows

package main

import "os"

// ownedByMe has no portable answer on Windows, and `%TEMP%` is the user's own directory there —
// the shared-directory problem this guards against does not arise.
func ownedByMe(os.FileInfo) bool { return true }

// sharedWritable is always false on Windows.
//
// **The mode bits there are not permissions.** Go synthesizes `0666|0111` for every writable
// directory, so a bit test would refuse every cache root; the ACL is what decides, and the tool
// does not read one. Ownership is the guard on this platform (v82 audit).
func sharedWritable(os.FileMode) bool { return false }

//go:build unix

package sessions

import (
	"os"
	"syscall"
)

// openFlags open a transcript without waiting: a named pipe put in its place opens at once,
// and the fstat that follows refuses it (HT-382). A regular file reads as usual.
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK

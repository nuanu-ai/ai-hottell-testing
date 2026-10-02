//go:build !unix

package sessions

import "os"

// openFlags open a transcript; outside unix there are no named pipes in the file tree.
const openFlags = os.O_RDONLY

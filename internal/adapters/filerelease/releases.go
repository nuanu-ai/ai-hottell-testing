// Package filerelease serves the client artifacts bundled in the self-hosted image.
package filerelease

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
)

type Releases struct{ files fs.FS }

// New reads an operator-owned, read-only directory; it needs no release-service token.
func New(dir string) *Releases { return &Releases{files: os.DirFS(dir)} }

func (r *Releases) Open(ctx context.Context, name string) (io.ReadCloser, int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if !fs.ValidPath(name) || path.Base(name) != name || name == "." {
		return nil, 0, fmt.Errorf("invalid release file name")
	}
	f, err := r.files.Open(name)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, 0, fmt.Errorf("release file is not readable or regular")
	}
	return f, info.Size(), nil
}

// Package readutil holds the one shared document-read helper for the
// compat/loader format adapters. It existed as three identical private
// copies (markdown/html/pdf) before this consolidation.
package readutil

import (
	"context"
	"fmt"
	"io"
)

// MaxDocumentBytes caps the size of a single loaded document (32 MiB).
const MaxDocumentBytes = 32 << 20

// ReadAllLimited reads at most limit bytes from r, polling ctx between reads
// so a cancelled context aborts promptly without leaking a goroutine.
func ReadAllLimited(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var buf []byte
	tmp := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if int64(len(buf)) > limit {
				return nil, fmt.Errorf("document exceeds %d byte limit", limit)
			}
		}
		if err != nil {
			if err == io.EOF {
				return buf, nil
			}
			return nil, err
		}
	}
}

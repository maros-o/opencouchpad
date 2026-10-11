package main

import (
	"io"
	"os"
	"path/filepath"
)

// openLog appends to the log file at p, rotating it once it passes 5 MB.
func openLog(p string) io.Writer {
	os.MkdirAll(filepath.Dir(p), 0o755)
	if fi, err := os.Stat(p); err == nil && fi.Size() > 5<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return io.Discard
	}
	return f
}

package backend

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// rotatedLog bounds local operational logs to three 5 MiB files.
type rotatedLog struct {
	mu   sync.Mutex
	path string
	file *os.File
	size int64
}

func openLog(dir string) (*rotatedLog, error) {
	w := &rotatedLog{path: filepath.Join(dir, "logs", "server.log")}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}
func (w *rotatedLog) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}
func (w *rotatedLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.size+int64(len(data)) > 5<<20 {
		w.file.Close()
		os.Remove(w.path + ".2")
		if _, err := os.Stat(w.path + ".1"); err == nil {
			if err = os.Rename(w.path+".1", w.path+".2"); err != nil {
				return 0, err
			}
		}
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return 0, err
		}
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(data)
	w.size += int64(n)
	return n, err
}
func (w *rotatedLog) Close() error { w.mu.Lock(); defer w.mu.Unlock(); return w.file.Close() }

type requestLogKey struct{}
type requestLog struct {
	Logger *log.Logger
	ID     string
}

func safeErrorClass(err error) string {
	// Error strings can contain SQL values or private filenames. Operational
	// logs keep request IDs and error codes without copying request content.
	type coder interface{ Code() int }
	if c, ok := err.(coder); ok {
		return fmt.Sprintf("%T code=%d", err, c.Code())
	}
	if e, ok := err.(*APIError); ok {
		return e.Code
	}
	return fmt.Sprintf("%T", err)
}

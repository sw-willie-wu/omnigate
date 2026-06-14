package app

import (
	"os"
	"sync"
)

// RotatingFile is a size-capped log sink. Writes append to the active file;
// once a write would push the file past maxBytes, the active file is renamed
// to "<path>.1" (overwriting any previous backup) and a fresh active file is
// started. Total on-disk log footprint is therefore bounded at roughly
// 2×maxBytes. It is safe for concurrent use.
type RotatingFile struct {
	path     string
	maxBytes int64

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenRotatingFile opens (creating if needed) the log file at path in append
// mode. maxBytes <= 0 disables rotation. The caller owns Close.
func OpenRotatingFile(path string, maxBytes int64) (*RotatingFile, error) {
	rf := &RotatingFile{path: path, maxBytes: maxBytes}
	if err := rf.open(); err != nil {
		return nil, err
	}
	return rf, nil
}

func (rf *RotatingFile) open() error {
	f, err := os.OpenFile(rf.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	rf.f = f
	rf.size = info.Size()
	return nil
}

func (rf *RotatingFile) Write(p []byte) (int, error) {
	rf.mu.Lock()
	defer rf.mu.Unlock()

	if rf.maxBytes > 0 && rf.size > 0 && rf.size+int64(len(p)) > rf.maxBytes {
		if err := rf.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := rf.f.Write(p)
	rf.size += int64(n)
	return n, err
}

// rotate closes the active file, moves it to "<path>.1" (replacing any prior
// backup), and opens a fresh active file. Caller holds rf.mu.
func (rf *RotatingFile) rotate() error {
	if err := rf.f.Close(); err != nil {
		return err
	}
	// os.Rename replaces the destination on Windows and Unix alike.
	if err := os.Rename(rf.path, rf.path+".1"); err != nil {
		// Rotation failed; reopen the original so logging continues.
		_ = rf.open()
		return err
	}
	return rf.open()
}

// Close closes the active log file.
func (rf *RotatingFile) Close() error {
	rf.mu.Lock()
	defer rf.mu.Unlock()
	if rf.f == nil {
		return nil
	}
	err := rf.f.Close()
	rf.f = nil
	return err
}

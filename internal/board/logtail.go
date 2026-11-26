package board

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DefaultLogChunkBytes is the max bytes returned per /api/log or SSE append.
const DefaultLogChunkBytes = 64 * 1024

// LogChunk is one offset-based read of a session log file (not stored in SQLite).
type LogChunk struct {
	OK         bool   `json:"ok"`
	Path       string `json:"path,omitempty"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"next_offset"`
	EOF        bool   `json:"eof"`
	Chunk      string `json:"chunk,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	Missing    bool   `json:"missing,omitempty"` // agent has no log_path
	Error      string `json:"error,omitempty"`
}

// LogFunc resolves an agent session log and returns a chunk from offset.
// fromTail is honored only when offset is 0 (initial window near EOF).
type LogFunc func(projectID, agent string, offset int64, fromTail bool) (LogChunk, error)

// ValidateLogPath requires an absolute path that resolves under $HOME
// without escaping via symlinks.
func ValidateLogPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("empty path")
	}
	if !filepath.IsAbs(raw) {
		return "", errors.New("path must be absolute")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	homeReal, err := filepath.EvalSymlinks(homeAbs)
	if err != nil {
		// Home itself may not need EvalSymlinks on all platforms; keep Abs.
		homeReal = homeAbs
	}

	cleaned := filepath.Clean(raw)
	// Resolve existing prefix for symlink escape checks.
	resolved, err := resolveUnder(cleaned)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(homeReal, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path must be under $HOME")
	}
	return resolved, nil
}

// resolveUnder EvalSymlinks the longest existing prefix, then joins the rest.
func resolveUnder(path string) (string, error) {
	cur := path
	var missing []string
	for {
		real, err := filepath.EvalSymlinks(cur)
		if err == nil {
			if len(missing) == 0 {
				return real, nil
			}
			parts := append([]string{real}, reverse(missing)...)
			return filepath.Join(parts...), nil
		}
		if !os.IsNotExist(err) {
			// EvalSymlinks fails on non-exist; also try parent for other errors on leaf.
			if _, statErr := os.Lstat(cur); statErr == nil {
				// Exists but EvalSymlinks failed — reject rather than guess.
				return "", fmt.Errorf("resolve path: %w", err)
			}
		}
		dir, base := filepath.Split(cur)
		if dir == "" || dir == cur {
			return "", fmt.Errorf("resolve path: %w", err)
		}
		missing = append(missing, strings.TrimSuffix(base, string(os.PathSeparator)))
		cur = strings.TrimSuffix(dir, string(os.PathSeparator))
		if cur == "" {
			cur = string(os.PathSeparator)
		}
	}
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[len(s)-1-i]
	}
	return out
}

// ReadLogFile reads up to maxBytes from offset. Offset beyond EOF clamps to size.
// When fromTail is true and offset is 0, starts at max(0, size-maxBytes) so the
// board can show the recent window of a growing session log.
func ReadLogFile(absPath string, offset int64, maxBytes int, fromTail bool) (LogChunk, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultLogChunkBytes
	}
	absPath, err := ValidateLogPath(absPath)
	if err != nil {
		return LogChunk{OK: false, Error: err.Error()}, err
	}
	f, err := os.Open(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return LogChunk{OK: true, Path: absPath, Offset: offset, NextOffset: offset, EOF: true, Error: "file not found"}, nil
		}
		return LogChunk{OK: false, Path: absPath, Error: err.Error()}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return LogChunk{OK: false, Path: absPath, Error: err.Error()}, err
	}
	size := info.Size()
	if offset < 0 {
		offset = 0
	}
	if fromTail && offset == 0 && size > int64(maxBytes) {
		offset = size - int64(maxBytes)
	}
	if offset > size {
		offset = size
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return LogChunk{OK: false, Path: absPath, Offset: offset, Error: err.Error()}, err
	}
	// Read one extra byte to detect truncation without a second Stat race.
	buf := make([]byte, maxBytes+1)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return LogChunk{OK: false, Path: absPath, Offset: offset, Error: err.Error()}, err
	}
	truncated := n > maxBytes
	if truncated {
		n = maxBytes
	}
	next := offset + int64(n)
	return LogChunk{
		OK:         true,
		Path:       absPath,
		Offset:     offset,
		NextOffset: next,
		EOF:        next >= size && !truncated,
		Chunk:      string(buf[:n]),
		Truncated:  truncated,
	}, nil
}

// AgentMetaLogPath extracts meta.log_path if present.
func AgentMetaLogPath(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	v, ok := meta["log_path"]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

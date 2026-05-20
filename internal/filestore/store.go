package filestore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type Store struct {
	base string
}

func New(base string) *Store {
	return &Store{base: base}
}

func (s *Store) Write(client, filename string, content io.Reader) (string, error) {
	full, err := s.safePath(client, filename)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.Create(full)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", full, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, content); err != nil {
		return "", fmt.Errorf("write %s: %w", full, err)
	}

	return full, nil
}

func (s *Store) Read(client, filename string) (io.ReadCloser, error) {
	full, err := s.safePath(client, filename)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

func (s *Store) safePath(client, filename string) (string, error) {
	if err := validateClient(client); err != nil {
		return "", err
	}
	if err := validateFilename(filename); err != nil {
		return "", err
	}

	full := filepath.Join(s.base, client, filename)
	cleanBase := filepath.Clean(s.base)
	cleanFull := filepath.Clean(full)

	rel, err := filepath.Rel(cleanBase, cleanFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("resolved path %q escapes base %q", cleanFull, cleanBase)
	}
	return cleanFull, nil
}

// validateClient accepts forward-slash separated segments (e.g.
// "Lifleg/Admin/2026/05/Cliente 01"), validating each segment individually.
// Backslashes, null bytes, drive letters and "."/".." segments are rejected.
func validateClient(s string) error {
	if s == "" {
		return fmt.Errorf("client is required")
	}
	if strings.ContainsAny(s, `\`+"\x00") {
		return fmt.Errorf("invalid client: contains backslash or null byte: %q", s)
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" {
			return fmt.Errorf("invalid client: empty segment in %q", s)
		}
		if seg == "." || seg == ".." {
			return fmt.Errorf("invalid client: %q segment in %q", seg, s)
		}
		if len(seg) >= 2 && seg[1] == ':' {
			return fmt.Errorf("invalid client: drive-letter-like segment %q in %q", seg, s)
		}
	}
	return nil
}

// validateFilename remains strict — single name, no separators of any kind.
func validateFilename(s string) error {
	if s == "" {
		return fmt.Errorf("filename is required")
	}
	if s == "." || s == ".." {
		return fmt.Errorf("invalid filename: %q", s)
	}
	if strings.ContainsAny(s, `/\`+"\x00") {
		return fmt.Errorf("invalid filename: contains separator or null byte: %q", s)
	}
	if len(s) >= 2 && s[1] == ':' {
		return fmt.Errorf("invalid filename: looks like a drive letter: %q", s)
	}
	return nil
}

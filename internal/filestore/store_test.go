package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWrite_HappyPath_SingleSegment(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	full, err := s.Write("Cliente A", "teste.txt", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	want := filepath.Join(dir, "Cliente A", "teste.txt")
	if full != want {
		t.Fatalf("path = %q, want %q", full, want)
	}
}

func TestWrite_HappyPath_NestedClient(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	client := "Lifleg/Admin/2026/05/Cliente 01"
	full, err := s.Write(client, "balanco.pdf", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	want := filepath.Join(dir, "Lifleg", "Admin", "2026", "05", "Cliente 01", "balanco.pdf")
	if full != want {
		t.Fatalf("path = %q, want %q", full, want)
	}

	// File and full directory chain must actually exist
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("file should exist: %v", err)
	}
}

func TestWrite_RejectsPathTraversal(t *testing.T) {
	cases := []struct {
		name     string
		client   string
		filename string
	}{
		{"client just dotdot", "..", "x.txt"},
		{"client dotdot in middle", "ok/../etc", "x.txt"},
		{"client dotdot at end", "ok/..", "x.txt"},
		{"client dotdot at start", "../etc", "x.txt"},
		{"client backslash dotdot", `..\Windows`, "x.txt"},
		{"client absolute unix", "/etc", "x.txt"},
		{"client absolute windows", `C:\Windows`, "x.txt"},
		{"client drive letter segment", "C:/something", "x.txt"},
		{"client with backslash", `a\b`, "x.txt"},
		{"client with null byte", "a\x00b", "x.txt"},
		{"client trailing slash", "Lifleg/Admin/", "x.txt"},
		{"client double slash", "Lifleg//Admin", "x.txt"},
		{"client just dot", ".", "x.txt"},
		{"empty client", "", "x.txt"},

		{"filename dotdot", "Cliente A", ".."},
		{"filename dotdot path", "Cliente A", "../../passwd"},
		{"filename with separator", "Cliente A", "a/b.txt"},
		{"filename with backslash", "Cliente A", `a\b.txt`},
		{"filename absolute", "Cliente A", "/etc/passwd"},
		{"filename windows absolute", "Cliente A", `C:\Windows\evil.dll`},
		{"filename with null byte", "Cliente A", "a\x00b.txt"},
		{"empty filename", "Cliente A", ""},
		{"filename just dot", "Cliente A", "."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := New(dir)

			full, err := s.Write(tc.client, tc.filename, strings.NewReader("payload"))
			if err == nil {
				t.Fatalf("expected error, got path %q", full)
			}
		})
	}
}

func TestRead_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	_, err := s.Read("..", "any.txt")
	if err == nil {
		t.Fatal("expected error on traversal client")
	}
	_, err = s.Read("Cliente A", "../../etc/passwd")
	if err == nil {
		t.Fatal("expected error on traversal filename")
	}
	_, err = s.Read(`a\b`, "any.txt")
	if err == nil {
		t.Fatal("expected error on backslash in client")
	}
}

func TestRead_AcceptsNestedClient(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	client := "Lifleg/Cliente/2026/05/Cliente 01"
	if _, err := s.Write(client, "x.txt", strings.NewReader("data")); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	rc, err := s.Read(client, "x.txt")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer rc.Close()
}

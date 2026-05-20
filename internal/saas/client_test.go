package saas

import (
	"strings"
	"testing"
)

func TestResolveURL_Relative(t *testing.T) {
	got, err := resolveURL("https://saas.example.com", "/files/123")
	if err != nil {
		t.Fatalf("resolveURL: %v", err)
	}
	want := "https://saas.example.com/files/123"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveURL_AbsoluteSameHostAllowed(t *testing.T) {
	got, err := resolveURL("https://saas.example.com", "https://saas.example.com/files/123")
	if err != nil {
		t.Fatalf("resolveURL: %v", err)
	}
	if got != "https://saas.example.com/files/123" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveURL_RejectsForeignHost(t *testing.T) {
	_, err := resolveURL("https://saas.example.com", "https://attacker.example/payload.exe")
	if err == nil {
		t.Fatal("expected error for foreign host")
	}
	if !strings.Contains(err.Error(), "host") {
		t.Fatalf("error %q does not mention host", err.Error())
	}
}

func TestResolveURL_RejectsForeignScheme(t *testing.T) {
	_, err := resolveURL("https://saas.example.com", "http://saas.example.com/files/123")
	if err == nil {
		t.Fatal("expected error for downgraded scheme")
	}
}

func TestResolveURL_RejectsMalformedRef(t *testing.T) {
	_, err := resolveURL("https://saas.example.com", "ht!tp://broken")
	if err == nil {
		t.Fatal("expected error for malformed ref")
	}
}

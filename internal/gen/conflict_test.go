package gen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGoConflict(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "legacy.go"), "package handlers\n\nfunc RespondJSON() {}\n\ntype User struct{}\n\nfunc (u *User) Save() {}\n")
	writeFile(t, filepath.Join(dir, "legacy_test.go"), "package handlers_test\n\nfunc helper() {}\n")
	writeFile(t, filepath.Join(dir, "tool.go"), "//go:build ignore\n\npackage main\n\nfunc main() {}\n")

	cases := []struct {
		name, file, src, want string
	}{
		{"no clash", "new.go", "package handlers\n\nfunc Other() {}\n", ""},
		{"func clash", "new.go", "package handlers\n\nfunc RespondJSON() {}\n", "RespondJSON"},
		{"type clash", "new.go", "package handlers\n\ntype User int\n", "User"},
		{"method clash", "new.go", "package handlers\n\nfunc (User) Save() {}\n", "User.Save"},
		{"same method name, other type", "new.go", "package handlers\n\ntype Order struct{}\n\nfunc (o Order) Save() {}\n", ""},
		{"package mismatch", "new.go", "package api\n", "package handlers"},
		{"external test pkg only vs itself", "x_test.go", "package handlers_test\n\nfunc helper() {}\n", "helper"},
		{"internal test vs external test", "y_test.go", "package handlers\n\nfunc helper() {}\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := goConflict(filepath.Join(dir, c.file), []byte(c.src))
			if c.want == "" && got != "" {
				t.Fatalf("unexpected conflict: %s", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("conflict = %q, want it to mention %q", got, c.want)
			}
		})
	}
}

func TestWriterKeepsFilesItDidNotWrite(t *testing.T) {
	root := t.TempDir()
	own := "package app\n\n// mine\nfunc X() {}\n"
	writeFile(t, filepath.Join(root, "internal/app/modules_gen.go"), own)
	writeFile(t, filepath.Join(root, "README.md"), "# mine\n")

	w, err := NewWriter(root, false, false, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	gen := generatedHeader + "\n\npackage app\n"
	if err := w.Write("internal/app/modules_gen.go", []byte(gen), Generated); err != nil {
		t.Fatal(err)
	}
	if err := w.Write("README.md", []byte("# goforge\n"), Scaffold); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{"internal/app/modules_gen.go": own, "README.md": "# mine\n"} {
		b, _ := os.ReadFile(filepath.Join(root, rel))
		if string(b) != want {
			t.Errorf("%s was modified:\n%s", rel, b)
		}
	}
	if len(w.Kept) != 2 {
		t.Errorf("Kept = %v, want both files", w.Kept)
	}

	// A Generated file that carries goforge's header is regenerated even without a manifest entry.
	writeFile(t, filepath.Join(root, "internal/app/middleware_gen.go"), generatedHeader+"\n\npackage app\n\n// old\n")
	if err := w.Write("internal/app/middleware_gen.go", []byte(gen), Generated); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "internal/app/middleware_gen.go")); strings.Contains(string(b), "old") {
		t.Errorf("goforge-generated file was not regenerated")
	}
}

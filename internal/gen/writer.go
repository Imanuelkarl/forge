// Package gen renders templates into a project directory.
package gen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Imanuelkarl/forge/internal/assets"
)

// Kind decides what happens when a target file already exists.
type Kind int

const (
	// Scaffold files are written once and then belong to the developer: they are
	// only overwritten while they still match what goforge last wrote (or with --force).
	Scaffold Kind = iota
	// Generated files ("Code generated ... DO NOT EDIT.") are always overwritten.
	Generated
)

const manifestPath = ".forge/manifest.json"

// ModuleState remembers what goforge generated for a module.
type ModuleState struct {
	Generated bool   `json:"generated"`
	Migration string `json:"migration,omitempty"`
	UpHash    string `json:"up_hash,omitempty"`
}

// Manifest records a hash of every file goforge wrote, so it can tell later whether a
// developer edited it.
type Manifest struct {
	Files   map[string]string      `json:"files"`
	Modules map[string]ModuleState `json:"modules"`
}

// LoadManifest reads .forge/manifest.json (an absent file yields an empty manifest).
func LoadManifest(root string) (*Manifest, error) {
	m := &Manifest{Files: map[string]string{}, Modules: map[string]ModuleState{}}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(manifestPath)))
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestPath, err)
	}
	if m.Files == nil {
		m.Files = map[string]string{}
	}
	if m.Modules == nil {
		m.Modules = map[string]ModuleState{}
	}
	return m, nil
}

// Save writes the manifest to disk.
func (m *Manifest) Save(root string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(root, filepath.FromSlash(manifestPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Writer writes files, formats Go code and keeps the manifest up to date.
type Writer struct {
	Root     string
	DryRun   bool
	Force    bool
	Manifest *Manifest
	Out      io.Writer

	Written      int
	Skipped      []string
	FormatErrors []string
}

// NewWriter creates a Writer for the project at root.
func NewWriter(root string, dryRun, force bool, out io.Writer) (*Writer, error) {
	mf, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	return &Writer{Root: root, DryRun: dryRun, Force: force, Manifest: mf, Out: out}, nil
}

func (w *Writer) logf(format string, args ...any) {
	fmt.Fprintf(w.Out, format+"\n", args...)
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Write stores content at rel (slash-separated, relative to the project root).
func (w *Writer) Write(rel string, content []byte, kind Kind) error {
	rel = filepath.ToSlash(rel)

	if strings.HasSuffix(rel, ".go") {
		formatted, err := format.Source(content)
		if err != nil {
			w.FormatErrors = append(w.FormatErrors, fmt.Sprintf("%s: %v", rel, err))
		} else {
			content = formatted
		}
	}
	if len(content) == 0 || content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}

	abs := filepath.Join(w.Root, filepath.FromSlash(rel))
	sum := hashBytes(content)

	existing, err := os.ReadFile(abs)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if exists {
		cur := hashBytes(existing)
		if cur == sum {
			w.Manifest.Files[rel] = sum
			return nil
		}
		if kind == Scaffold && !w.Force {
			prev, tracked := w.Manifest.Files[rel]
			if !tracked || prev != cur {
				w.Skipped = append(w.Skipped, rel)
				w.logf("  skip     %s (edited by you)", rel)
				return nil
			}
		}
	}

	verb := "create"
	if exists {
		verb = "update"
	}
	if w.DryRun {
		w.logf("  would %s %s", verb, rel)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		return err
	}
	w.Manifest.Files[rel] = sum
	w.Written++
	w.logf("  %s   %s", verb, rel)
	return nil
}

// Finish saves the manifest and prints a summary. It returns an error if any Go file failed to format.
func (w *Writer) Finish() error {
	if !w.DryRun {
		if err := w.Manifest.Save(w.Root); err != nil {
			return err
		}
	}

	if len(w.Skipped) > 0 {
		w.logf("\n%d file(s) were edited by you and left untouched (use --force to overwrite):", len(w.Skipped))
		for _, s := range w.Skipped {
			w.logf("  - %s", s)
		}
		w.logf("If the schema changed, review them by hand so they match the regenerated code.")
	}
	if len(w.FormatErrors) > 0 {
		w.logf("\ngenerated code that does not parse (this is a goforge bug, please report it):")
		for _, e := range w.FormatErrors {
			w.logf("  - %s", e)
		}
		return fmt.Errorf("%d generated file(s) failed gofmt", len(w.FormatErrors))
	}
	return nil
}

// render executes the embedded template at path with data.
func render(path string, data any) ([]byte, error) {
	b, err := assets.FS.ReadFile("templates/" + path)
	if err != nil {
		return nil, err
	}
	t, err := template.New(path).Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render %s: %w", path, err)
	}
	return buf.Bytes(), nil
}

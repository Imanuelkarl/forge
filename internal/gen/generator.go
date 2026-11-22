package gen

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Imanuelkarl/forge/internal/assets"
	"github.com/Imanuelkarl/forge/internal/dialect"
	"github.com/Imanuelkarl/forge/internal/naming"
	"github.com/Imanuelkarl/forge/internal/schema"
)

// Generator turns a schema into files.
type Generator struct {
	Root      string
	Schema    *schema.Schema
	Dialect   dialect.Dialect
	W         *Writer
	Out       io.Writer
	NoSwagger bool

	project *ProjectData
}

// New creates a Generator for the project at root.
func New(root string, s *schema.Schema, w *Writer, out io.Writer) (*Generator, error) {
	d, err := dialect.Get(s.Project.Database)
	if err != nil {
		return nil, err
	}
	return &Generator{Root: root, Schema: s, Dialect: d, W: w, Out: out}, nil
}

func (g *Generator) swagger() bool { return g.Schema.Project.Swagger && !g.NoSwagger }

func (g *Generator) logf(format string, args ...any) {
	fmt.Fprintf(g.Out, format+"\n", args...)
}

// ── Modules ──────────────────────────────────────────────────────────────

// All generates every module in schema order, refreshes the router files, then the wiring.
func (g *Generator) All() error {
	for i := range g.Schema.Modules {
		if err := g.Module(g.Schema.Modules[i].Name); err != nil {
			return err
		}
	}
	if err := g.RefreshProjectFiles(); err != nil {
		return err
	}
	return g.Wiring()
}

// refreshedProjectFiles are project skeleton files that depend on how the generated wiring
// looks. `goforge sync` re-renders them so older projects pick up wiring changes; like every
// scaffold file they are only overwritten while you have not edited them.
var refreshedProjectFiles = []string{
	"cmd/server/main.go",
	"internal/handlers/router.go",
	"internal/handlers/health_handler_test.go",
	"internal/services/helpers.go",
}

// RefreshProjectFiles re-renders refreshedProjectFiles.
func (g *Generator) RefreshProjectFiles() error {
	data := g.projectData()
	for _, rel := range refreshedProjectFiles {
		content, err := render("project/"+rel+".tmpl", data)
		if err != nil {
			return err
		}
		if err := g.W.Write(rel, content, Scaffold); err != nil {
			return err
		}
	}
	return nil
}

// Module generates the code, tests and (once) the initial migration for one module.
// It does not rewrite the wiring; call Wiring afterwards.
func (g *Generator) Module(name string) error {
	m, idx := g.Schema.Module(name)
	if m == nil {
		return fmt.Errorf("module %q not found in %s", name, schema.FileName)
	}
	g.logf("module %s:", m.Name)

	mv := buildView(m, g.Schema.Project, g.Dialect, g.swagger())
	p := mv.FilePrefix

	files := []struct{ tmpl, out string }{
		{"module/domain.go.tmpl", "internal/domain/" + p + ".go"},
		{"module/service.go.tmpl", "internal/services/" + p + "_service.go"},
		{"module/handler.go.tmpl", "internal/handlers/" + p + "_handler.go"},
		{"module/repository.go.tmpl", "internal/repositories/" + p + "_repository.go"},
		{"module/memory_repository.go.tmpl", "internal/repositories/" + p + "_memory_repository.go"},
		{"module/service_test.go.tmpl", "internal/services/" + p + "_service_test.go"},
	}
	if mv.HandlerTests {
		files = append(files, struct{ tmpl, out string }{"module/handler_test.go.tmpl", "internal/handlers/" + p + "_handler_test.go"})
	}

	for _, f := range files {
		content, err := render(f.tmpl, mv)
		if err != nil {
			return err
		}
		if err := g.W.Write(f.out, content, Scaffold); err != nil {
			return err
		}
	}

	if err := g.migration(m, idx); err != nil {
		return err
	}

	if !g.W.DryRun {
		key := naming.Snake(m.Name)
		st := g.W.Manifest.Modules[key]
		st.Generated = true
		g.W.Manifest.Modules[key] = st
	}
	return nil
}

var versionRe = regexp.MustCompile(`^(\d+)_`)

// nextVersion returns a UTC timestamp version that is greater than every existing one.
func nextVersion(dir string) (string, error) {
	var highest uint64
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, e := range entries {
		if m := versionRe.FindStringSubmatch(e.Name()); m != nil {
			if v, err := strconv.ParseUint(m[1], 10, 64); err == nil && v > highest {
				highest = v
			}
		}
	}
	now, err := strconv.ParseUint(time.Now().UTC().Format("20060102150405"), 10, 64)
	if err != nil {
		return "", err
	}
	if now <= highest {
		now = highest + 1
	}
	return strconv.FormatUint(now, 10), nil
}

// migration writes the initial migration for a module exactly once.
func (g *Generator) migration(m *schema.Module, idx int) error {
	up, down := migrationSQL(g.Schema.Modules, idx, g.Dialect)
	hash := hashBytes([]byte(up))
	key := naming.Snake(m.Name)

	state := g.W.Manifest.Modules[key]
	// Deleting a module's (not yet applied) migration file and re-running sync regenerates it.
	if state.Migration != "" {
		if _, err := os.Stat(filepath.Join(g.Root, filepath.FromSlash(state.Migration+".up.sql"))); errors.Is(err, os.ErrNotExist) {
			g.logf("  note     %s.up.sql was deleted; writing a fresh migration for %q", state.Migration, m.Name)
			if !g.W.DryRun {
				_ = os.Remove(filepath.Join(g.Root, filepath.FromSlash(state.Migration+".down.sql")))
			}
			state.Migration, state.UpHash = "", ""
		}
	}
	if state.Migration != "" {
		if state.UpHash != hash {
			g.logf("  note     the schema of %q changed after its migration was generated;", m.Name)
			g.logf("           write the change with `goforge migrate create <name>` (goforge never edits applied migrations)")
			g.logf("           or, if %s.up.sql was never applied, delete it and re-run `goforge sync`", state.Migration)
		}
		return nil
	}

	version, err := nextVersion(filepath.Join(g.Root, "migrations"))
	if err != nil {
		return err
	}
	base := fmt.Sprintf("migrations/%s_create_%s", version, m.Table)
	if err := g.W.Write(base+".up.sql", []byte(up), Scaffold); err != nil {
		return err
	}
	if err := g.W.Write(base+".down.sql", []byte(down), Scaffold); err != nil {
		return err
	}
	if !g.W.DryRun {
		g.W.Manifest.Modules[key] = ModuleState{Generated: state.Generated, Migration: base, UpHash: hash}
	}
	return nil
}

// ── Wiring ───────────────────────────────────────────────────────────────

// Wiring regenerates the *_gen.go files: internal/app/{modules,middleware}_gen.go,
// internal/handlers/handler_gen.go and internal/services/ids_gen.go.
// Only modules that goforge has already generated are wired, so a half-written
// forge.yaml can never break the build.
func (g *Generator) Wiring() error {
	var views []*ModuleView
	needUUID := false
	for i := range g.Schema.Modules {
		m := &g.Schema.Modules[i]
		if g.W.Manifest.Modules[naming.Snake(m.Name)].Generated {
			mv := buildView(m, g.Schema.Project, g.Dialect, g.swagger())
			views = append(views, mv)
			needUUID = needUUID || !mv.ID.Auto
		}
	}

	var mw []string
	for _, n := range g.Schema.Middleware {
		mw = append(mw, naming.Pascal(n))
	}

	data := struct {
		Module     string
		Modules    []*ModuleView
		Middleware []string
		NeedUUID   bool
	}{g.Schema.Project.Module, views, mw, needUUID}

	for _, f := range []struct{ tmpl, out string }{
		{"wiring/modules_gen.go.tmpl", "internal/app/modules_gen.go"},
		{"wiring/middleware_gen.go.tmpl", "internal/app/middleware_gen.go"},
		{"wiring/handler_gen.go.tmpl", "internal/handlers/handler_gen.go"},
		{"wiring/ids_gen.go.tmpl", "internal/services/ids_gen.go"},
	} {
		content, err := render(f.tmpl, data)
		if err != nil {
			return err
		}
		if err := g.W.Write(f.out, content, Generated); err != nil {
			return err
		}
	}
	return nil
}

// ── Project ──────────────────────────────────────────────────────────────

// ProjectData is the data passed to the project templates.
type ProjectData struct {
	Name          string
	Module        string
	Dialect       string
	Swagger       bool
	GoVersion     string
	DriverImport  string
	DriverName    string
	MigrateImport string
	DefaultURL    string
}

// DefaultGoVersion is the Go version written to a new project's go.mod.
const DefaultGoVersion = "1.22"

func (g *Generator) projectData() *ProjectData {
	p := g.Schema.Project
	goVersion := GoVersion(g.Root)
	if goVersion == "" {
		goVersion = DefaultGoVersion
	}
	return &ProjectData{
		Name:          p.Name,
		Module:        p.Module,
		Dialect:       g.Dialect.Name(),
		Swagger:       p.Swagger,
		GoVersion:     goVersion,
		DriverImport:  g.Dialect.DriverImport(),
		DriverName:    g.Dialect.DriverName(),
		MigrateImport: g.Dialect.MigrateImport(),
		DefaultURL:    g.Dialect.DefaultURL(naming.Snake(p.Name)),
	}
}

// Project writes the full project skeleton, forge.yaml and .env.
func (g *Generator) Project() error {
	p := g.Schema.Project
	g.project = g.projectData()
	g.project.GoVersion = DefaultGoVersion

	const prefix = "templates/project/"
	err := fs.WalkDir(assets.FS, "templates/project", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".tmpl") {
			return nil
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, prefix), ".tmpl")
		if rel == "docs/docs.go" && !g.project.Swagger {
			return nil
		}

		content, err := render(strings.TrimPrefix(p, "templates/"), g.project)
		if err != nil {
			return err
		}
		if err := g.W.Write(rel, content, Scaffold); err != nil {
			return err
		}
		if rel == ".env.example" {
			return g.W.Write(".env", content, Scaffold)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := g.W.Write(schema.FileName, []byte(schema.RenderInitial(p)), Scaffold); err != nil {
		return err
	}
	return g.Wiring()
}

// UpdateGoVersion re-renders go.mod-dependent files (the Dockerfile) after `go mod tidy`
// raised the go directive.
func (g *Generator) UpdateGoVersion(version string) error {
	if g.project == nil || version == "" || version == g.project.GoVersion {
		return nil
	}
	g.project.GoVersion = version
	content, err := render("project/Dockerfile.tmpl", g.project)
	if err != nil {
		return err
	}
	return g.W.Write("Dockerfile", content, Scaffold)
}

// ── Middleware ───────────────────────────────────────────────────────────

// MiddlewarePresets lists the available middleware templates.
var MiddlewarePresets = []string{"blank", "security-headers", "rate-limit"}

// Middleware scaffolds internal/middleware/<name>.go and optionally registers it globally.
func (g *Generator) Middleware(name, preset string, global bool) error {
	known := false
	for _, p := range MiddlewarePresets {
		if p == preset {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("unknown preset %q (available: %s)", preset, strings.Join(MiddlewarePresets, ", "))
	}

	fn := naming.Pascal(name)
	if fn == "" {
		return errors.New("middleware name must contain letters or digits")
	}

	// Refuse to declare a function that already exists in the package.
	declRe := regexp.MustCompile(`(?m)^func\s+` + regexp.QuoteMeta(fn) + `\s*\(`)
	entries, _ := os.ReadDir(filepath.Join(g.Root, "internal", "middleware"))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(g.Root, "internal", "middleware", e.Name()))
		if err == nil && declRe.Match(b) && e.Name() != naming.Snake(name)+".go" {
			return fmt.Errorf("internal/middleware/%s already declares %s; pick another name", e.Name(), fn)
		}
	}

	data := struct{ Func, Name string }{fn, naming.Kebab(name)}
	content, err := render(path.Join("middleware", preset+".go.tmpl"), data)
	if err != nil {
		return err
	}
	if err := g.W.Write("internal/middleware/"+naming.Snake(name)+".go", content, Scaffold); err != nil {
		return err
	}

	if !global {
		return nil
	}
	if g.W.DryRun {
		g.logf("  would register %s in forge.yaml and regenerate internal/app/middleware_gen.go", data.Name)
		return nil
	}
	if err := schema.AddMiddleware(filepath.Join(g.Root, schema.FileName), data.Name); err != nil {
		g.logf("  could not edit forge.yaml automatically (%v); add `- %s` under `middleware:` and run `goforge sync`", err, data.Name)
		return nil
	}
	if !containsStr(g.Schema.Middleware, data.Name) {
		g.Schema.Middleware = append(g.Schema.Middleware, data.Name)
	}
	return g.Wiring()
}

// ── External tools ───────────────────────────────────────────────────────

func (g *Generator) run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = g.Root
	cmd.Stdout = g.Out
	cmd.Stderr = g.Out
	return cmd.Run()
}

// Tidy runs `go mod tidy` in the project.
func (g *Generator) Tidy() error {
	g.logf("\nrunning go mod tidy ...")
	return g.run("go", "mod", "tidy")
}

// Swag regenerates the Swagger docs when swagger is enabled and the swag CLI is installed.
func (g *Generator) Swag() error {
	if !g.swagger() || g.W.DryRun {
		return nil
	}
	if _, err := exec.LookPath("swag"); err != nil {
		g.logf("\nswagger: `swag` is not installed, so docs were not regenerated.")
		g.logf("         install it with: go install github.com/swaggo/swag/cmd/swag@latest   then run: make swagger")
		return nil
	}
	g.logf("\nrunning swag init ...")
	if err := g.run("swag", "init", "-g", "cmd/server/main.go", "-o", "docs", "--parseInternal"); err != nil {
		return fmt.Errorf("swag init: %w", err)
	}
	// docs.go uses fields of swag.Spec that only exist in the library version matching the
	// CLI (e.g. LeftDelim), while `go mod tidy` alone may resolve a much older library.
	if v := swagVersion(); v != "" {
		if err := g.run("go", "get", "github.com/swaggo/swag@"+v); err != nil {
			g.logf("could not pin github.com/swaggo/swag@%s (%v); run `go get github.com/swaggo/swag@%s` yourself", v, err, v)
		}
	}
	return g.run("go", "mod", "tidy")
}

var semverRe = regexp.MustCompile(`v\d+\.\d+\.\d+`)

// swagVersion returns the installed swag CLI's version (e.g. v1.16.4), or "".
func swagVersion() string {
	out, err := exec.Command("swag", "--version").Output()
	if err != nil {
		return ""
	}
	return semverRe.FindString(string(out))
}

// GoVersion reads the go directive (major.minor) from the project's go.mod.
func GoVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "go" {
			parts := strings.Split(fields[1], ".")
			if len(parts) >= 2 {
				return parts[0] + "." + parts[1]
			}
			return fields[1]
		}
	}
	return ""
}

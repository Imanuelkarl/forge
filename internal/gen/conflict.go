package gen

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// goConflict reports why adding a new Go file (abs, with content) to its directory would
// break the package already there: a different package name, or a top-level identifier
// that the directory already declares. It returns "" when the file can be added safely.
//
// This is what lets goforge scaffold into an existing project (`goforge init`) without
// breaking code it did not write.
func goConflict(abs string, content []byte) string {
	fset := token.NewFileSet()
	nf, err := parser.ParseFile(fset, abs, content, parser.SkipObjectResolution)
	if err != nil {
		return "" // unparsable output is reported as a format error by the Writer
	}
	dir, base := filepath.Split(abs)
	newPkg := nf.Name.Name
	newTest := strings.HasSuffix(base, "_test.go")
	newExternal := newTest && strings.HasSuffix(newPkg, "_test")

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	ours := topLevelNames(nf)
	var clashes []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == base || !strings.HasSuffix(name, ".go") {
			continue
		}
		// Skip files excluded by build constraints (//go:build ignore, _windows.go, ...).
		if ok, err := build.Default.MatchFile(dir, name); err != nil || !ok {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		pkg := f.Name.Name
		external := strings.HasSuffix(name, "_test.go") && strings.HasSuffix(pkg, "_test")

		switch {
		case external || newExternal:
			// Black-box test packages live beside the real one; only compare like with like.
			if pkg != newPkg {
				continue
			}
		case pkg != newPkg:
			return fmt.Sprintf("%s is package %s, goforge's file is package %s", filepath.ToSlash(name), pkg, newPkg)
		}

		theirs := topLevelNames(f)
		for id := range ours {
			if theirs[id] {
				clashes = append(clashes, fmt.Sprintf("%s (in %s)", id, name))
			}
		}
	}
	if len(clashes) == 0 {
		return ""
	}
	sort.Strings(clashes)
	return "already declared: " + strings.Join(clashes, ", ")
}

// topLevelNames returns the package-level identifiers a file declares. Methods are
// keyed as Type.Method so they only clash with a method of the same type.
func topLevelNames(f *ast.File) map[string]bool {
	names := map[string]bool{}
	add := func(n string) {
		if n != "_" && n != "init" {
			names[n] = true
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv != nil && len(d.Recv.List) > 0 {
				add(recvName(d.Recv.List[0].Type) + "." + d.Name.Name)
			} else {
				add(d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					add(s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						add(n.Name)
					}
				}
			}
		}
	}
	return names
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

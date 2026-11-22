package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, yaml string) (*Schema, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

const header = `
version: 1
project:
  name: app
  module: example.com/app
  database: postgres
`

func TestIDTypeDefaultsAndOverrides(t *testing.T) {
	s, err := load(t, header+`
modules:
  user:
    fields:
      name: string
  product:
    id: uuid
    fields:
      title: string
`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Project.IDType != "int" {
		t.Errorf("project id_type: want int, got %q", s.Project.IDType)
	}
	if got := s.Modules[0].ID; got != "int" {
		t.Errorf("user id: want int (project default), got %q", got)
	}
	if got := s.Modules[1].ID; got != "uuid" {
		t.Errorf("product id: want uuid (override), got %q", got)
	}
}

func TestProjectIDTypeApplies(t *testing.T) {
	s, err := load(t, strings.Replace(header, "database: postgres", "database: postgres\n  id_type: bigint", 1)+`
modules:
  user:
    fields:
      name: string
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Modules[0].ID; got != "bigint" {
		t.Errorf("want bigint, got %q", got)
	}
}

func TestInvalidIDType(t *testing.T) {
	_, err := load(t, header+`
modules:
  user:
    id: string
    fields:
      name: string
`)
	if err == nil || !strings.Contains(err.Error(), "id must be one of") {
		t.Fatalf("want id type error, got %v", err)
	}
}

func TestUnknownKeySuggestsFix(t *testing.T) {
	_, err := load(t, header+`
modules:
  user:
    fields:
      uuid: {type: uuid, required: true, unnique: true}
`)
	if err == nil || !strings.Contains(err.Error(), `did you mean "unique"`) {
		t.Fatalf("want a did-you-mean error, got %v", err)
	}
}

func TestRefTypeMustMatchTargetID(t *testing.T) {
	_, err := load(t, header+`
modules:
  user:
    fields:
      name: string
  post:
    fields:
      author_id: {type: uuid, required: true, ref: users.id}
`)
	if err == nil || !strings.Contains(err.Error(), "must be type int") {
		t.Fatalf("want ref type mismatch error, got %v", err)
	}
}

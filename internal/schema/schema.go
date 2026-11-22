// Package schema defines forge.yaml: the single source of truth for a project.
package schema

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Imanuelkarl/forge/internal/naming"
)

// FileName is the schema file looked up in the project root.
const FileName = "forge.yaml"

// ErrNotEditable is returned when goforge cannot safely edit forge.yaml in place.
var ErrNotEditable = errors.New("forge.yaml cannot be edited automatically")

var (
	// TypeNames lists the supported field types.
	TypeNames = []string{"string", "text", "int", "bigint", "float", "decimal", "bool", "uuid", "timestamp", "enum"}

	// IDTypes lists the supported primary key types. int and bigint are auto-incrementing.
	IDTypes = []string{"int", "bigint", "uuid"}

	// DefaultIDType is the primary key type used when forge.yaml does not set one.
	DefaultIDType = "int"

	// CRUDActions lists the built-in endpoints.
	CRUDActions = []string{"list", "get", "create", "update", "delete"}

	reservedFields = map[string]bool{"id": true, "created_at": true, "updated_at": true}
	uniqueTypes    = map[string]bool{"string": true, "uuid": true, "enum": true, "int": true, "bigint": true, "decimal": true}
	rangeTypes     = map[string]bool{"string": true, "text": true, "int": true, "bigint": true, "float": true}
	httpMethods    = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

	identRe     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	moduleRe    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	refRe       = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)
	pathParamRe = regexp.MustCompile(`\{[^}]*\}`)
	modulesKey  = regexp.MustCompile(`(?m)^modules:[ \t]*(#.*)?$`)
)

// UniqueAllowed reports whether a field of the given type may be marked unique.
func UniqueAllowed(t string) bool { return uniqueTypes[t] }

// Schema is the parsed forge.yaml.
type Schema struct {
	Version    int      `yaml:"version"`
	Project    Project  `yaml:"project"`
	Middleware []string `yaml:"middleware"`
	Modules    Modules  `yaml:"modules"`
}

// Project holds project-wide settings.
type Project struct {
	Name     string `yaml:"name"`
	Module   string `yaml:"module"`
	Layout   string `yaml:"layout"`
	Database string `yaml:"database"`
	Swagger  bool   `yaml:"swagger"`
	// IDType is the default primary key type for every module: int | bigint | uuid.
	IDType string `yaml:"id_type"`
}

// Module describes one entity and its endpoints.
type Module struct {
	Name   string `yaml:"-"`
	Table  string `yaml:"table"`
	Plural string `yaml:"plural"`
	// ID overrides project.id_type for this module: int | bigint | uuid.
	ID        string    `yaml:"id"`
	Fields    FieldList `yaml:"fields"`
	DTO       DTO       `yaml:"dto"`
	Endpoints Endpoints `yaml:"endpoints"`
	Security  Security  `yaml:"security"`
	Options   Options   `yaml:"options"`
}

// Field describes one column / struct field.
type Field struct {
	Name        string   `yaml:"-"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required"`
	Nullable    bool     `yaml:"nullable"`
	Unique      bool     `yaml:"unique"`
	Index       bool     `yaml:"index"`
	Default     string   `yaml:"default"`
	Min         *int     `yaml:"min"`
	Max         *int     `yaml:"max"`
	Format      string   `yaml:"format"`
	Values      []string `yaml:"values"`
	Validate    string   `yaml:"validate"`
	Ref         string   `yaml:"ref"`
	Description string   `yaml:"description"`
}

// DTO selects which fields appear in the create and update request bodies.
// A nil list means "every field".
type DTO struct {
	Create []string `yaml:"create"`
	Update []string `yaml:"update"`
}

// Endpoints selects the built-in CRUD endpoints and declares custom ones.
type Endpoints struct {
	CRUD   []string         `yaml:"crud"`
	Custom []CustomEndpoint `yaml:"custom"`
}

// CustomEndpoint is a hand-written endpoint scaffolded as a handler + service stub.
type CustomEndpoint struct {
	Name       string `yaml:"name"`
	Method     string `yaml:"method"`
	Path       string `yaml:"path"`
	Summary    string `yaml:"summary"`
	Permission string `yaml:"permission"`
}

// Security records access rules. They are validated and carried along now and
// enforced once the rbac pack exists.
type Security struct {
	Permissions map[string]string `yaml:"permissions"`
	OwnerScoped string            `yaml:"owner_scoped"`
}

// Options holds module-level switches.
type Options struct {
	SoftDelete bool `yaml:"soft_delete"`
}

// FieldList preserves the order fields were declared in.
type FieldList []Field

// Modules preserves the order modules were declared in (it decides migration order).
type Modules []Module

// UnmarshalYAML keeps declaration order, which a plain map would lose.
func (m *Modules) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: modules must be a mapping of name -> definition", node.Line)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		var mod Module
		if err := checkKeys(val, Module{}, fmt.Sprintf("module %q", key.Value)); err != nil {
			return err
		}
		if err := val.Decode(&mod); err != nil {
			return fmt.Errorf("module %q: %w", key.Value, err)
		}
		mod.Name = key.Value
		*m = append(*m, mod)
	}
	return nil
}

// UnmarshalYAML keeps declaration order and accepts the shorthand `name: string`.
func (l *FieldList) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!null" {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: fields must be a mapping of name -> definition", node.Line)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		var f Field
		if err := val.Decode(&f); err != nil {
			return fmt.Errorf("field %q: %w", key.Value, err)
		}
		f.Name = key.Value
		*l = append(*l, f)
	}
	return nil
}

// UnmarshalYAML accepts either `type` shorthand (a scalar) or a full mapping.
func (f *Field) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		f.Type = node.Value
		return nil
	}
	if err := checkKeys(node, Field{}, "field"); err != nil {
		return err
	}
	type plain Field
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	*f = Field(p)
	return nil
}

// UnmarshalYAML rejects unknown keys under `project:`.
func (p *Project) UnmarshalYAML(node *yaml.Node) error {
	if err := checkKeys(node, Project{}, "project"); err != nil {
		return err
	}
	type plain Project
	var v plain
	if err := node.Decode(&v); err != nil {
		return err
	}
	*p = Project(v)
	return nil
}

// checkKeys reports keys in a mapping node that the struct v has no yaml tag for, so a
// typo such as `unnique: true` fails loudly instead of being silently ignored.
func checkKeys(node *yaml.Node, v any, ctx string) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	var known []string
	t := reflect.TypeOf(v)
	for i := 0; i < t.NumField(); i++ {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if tag != "" && tag != "-" {
			known = append(known, tag)
		}
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		if containsString(known, k.Value) {
			continue
		}
		msg := fmt.Sprintf("line %d: %s: unknown key %q", k.Line, ctx, k.Value)
		if s := closest(k.Value, known); s != "" {
			msg += fmt.Sprintf(" (did you mean %q?)", s)
		} else {
			msg += " (valid: " + strings.Join(known, ", ") + ")"
		}
		return errors.New(msg)
	}
	return nil
}

// closest returns the candidate within edit distance 2 of s, if any.
func closest(s string, candidates []string) string {
	best, bestDist := "", 3
	for _, c := range candidates {
		if d := editDistance(s, c); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// ── Loading ──────────────────────────────────────────────────────────────

// FindRoot walks up from start until it finds forge.yaml.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s not found: run this inside a goforge project (or `goforge new` first)", FileName)
		}
		dir = parent
	}
}

// Load reads, normalizes and validates a schema file.
func Load(path string) (*Schema, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Schema
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	s.Normalize()
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("%s:\n%w", filepath.Base(path), err)
	}
	return &s, nil
}

// Normalize fills defaults. It is idempotent.
func (s *Schema) Normalize() {
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Project.Layout == "" {
		s.Project.Layout = "layered"
	}
	if s.Project.Database == "" {
		s.Project.Database = "postgres"
	}
	if s.Project.IDType == "" {
		s.Project.IDType = DefaultIDType
	}
	for i := range s.Modules {
		s.Modules[i].Normalize()
		if s.Modules[i].ID == "" {
			s.Modules[i].ID = s.Project.IDType
		}
	}
}

// Normalize fills module defaults. It is idempotent.
func (m *Module) Normalize() {
	if m.Plural == "" {
		m.Plural = naming.Plural(naming.Snake(m.Name))
	} else {
		m.Plural = naming.Snake(m.Plural)
	}
	if m.Table == "" {
		m.Table = m.Plural
	}
	if m.DTO.Create == nil {
		m.DTO.Create = m.fieldNames()
	}
	if m.DTO.Update == nil {
		m.DTO.Update = m.fieldNames()
	}
	if len(m.Endpoints.CRUD) == 0 {
		m.Endpoints.CRUD = append([]string(nil), CRUDActions...)
	}
	for i := range m.Endpoints.Custom {
		c := &m.Endpoints.Custom[i]
		c.Method = strings.ToUpper(c.Method)
		c.Name = naming.Snake(c.Name)
	}
}

func (m *Module) fieldNames() []string {
	names := make([]string, 0, len(m.Fields))
	for _, f := range m.Fields {
		names = append(names, f.Name)
	}
	return names
}

// HasCRUD reports whether a built-in endpoint is enabled.
func (m Module) HasCRUD(action string) bool {
	for _, a := range m.Endpoints.CRUD {
		if a == action {
			return true
		}
	}
	return false
}

// Field returns the field with the given name.
func (m Module) Field(name string) (Field, bool) {
	for _, f := range m.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Module returns the module with the given name (case/style-insensitive).
func (s *Schema) Module(name string) (*Module, int) {
	want := naming.Snake(name)
	for i := range s.Modules {
		if naming.Snake(s.Modules[i].Name) == want {
			return &s.Modules[i], i
		}
	}
	return nil, -1
}

// ── Validation ───────────────────────────────────────────────────────────

// Validate reports every problem it finds, not just the first.
func (s *Schema) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf("  - "+format, args...)) }

	if s.Project.Name == "" {
		add("project.name is required")
	}
	if s.Project.Module == "" {
		add("project.module is required")
	}
	if s.Project.Database != "postgres" && s.Project.Database != "mysql" {
		add("project.database must be postgres or mysql (got %q)", s.Project.Database)
	}
	if s.Project.Layout != "layered" {
		add("project.layout %q is not supported yet (only \"layered\")", s.Project.Layout)
	}
	if !containsString(IDTypes, s.Project.IDType) {
		add("project.id_type must be one of %s (got %q)", strings.Join(IDTypes, ", "), s.Project.IDType)
	}

	seenNames := map[string]bool{}
	seenTables := map[string]string{}
	for i := range s.Modules {
		m := &s.Modules[i]
		if !moduleRe.MatchString(m.Name) {
			add("module %q: name must start with a letter and contain only letters, digits, - or _", m.Name)
			continue
		}
		key := naming.Snake(m.Name)
		if seenNames[key] {
			add("module %q is declared twice", m.Name)
			continue
		}
		seenNames[key] = true
		if other, ok := seenTables[m.Table]; ok {
			add("module %q: table %q is already used by module %q", m.Name, m.Table, other)
		}
		seenTables[m.Table] = m.Name
		validateModule(m, add)
	}
	s.validateRefs(add)
	return errors.Join(errs...)
}

// validateRefs checks that a field referencing another module's id has the same type
// as that module's primary key (otherwise the foreign key cannot be created).
func (s *Schema) validateRefs(add func(string, ...any)) {
	for _, m := range s.Modules {
		for _, f := range m.Fields {
			refTable, refCol, ok := strings.Cut(f.Ref, ".")
			if !ok || refCol != "id" {
				continue
			}
			for _, target := range s.Modules {
				if target.Table == refTable && target.ID != f.Type && containsString(IDTypes, target.ID) {
					add("module %q field %q: references %s, whose id is %s, so the field must be type %s (got %s)",
						m.Name, f.Name, f.Ref, target.ID, target.ID, f.Type)
				}
			}
		}
	}
}

func validateModule(m *Module, add func(string, ...any)) {
	if m.Options.SoftDelete {
		add("module %q: options.soft_delete is not supported yet", m.Name)
	}
	if len(m.Fields) == 0 {
		add("module %q: must define at least one field", m.Name)
	}
	if !containsString(IDTypes, m.ID) {
		add("module %q: id must be one of %s (got %q)", m.Name, strings.Join(IDTypes, ", "), m.ID)
	}

	seen := map[string]bool{}
	for _, f := range m.Fields {
		validateField(m.Name, f, seen, add)
	}

	// DTOs
	inCreate := map[string]bool{}
	for _, n := range m.DTO.Create {
		if _, ok := m.Field(n); !ok {
			add("module %q: dto.create references unknown field %q", m.Name, n)
		}
		inCreate[n] = true
	}
	for _, n := range m.DTO.Update {
		if _, ok := m.Field(n); !ok {
			add("module %q: dto.update references unknown field %q", m.Name, n)
		}
	}
	for _, f := range m.Fields {
		if f.Required && !inCreate[f.Name] {
			add("module %q: field %q is required but missing from dto.create", m.Name, f.Name)
		}
	}

	// Endpoints
	crud := map[string]bool{}
	for _, a := range m.Endpoints.CRUD {
		ok := false
		for _, known := range CRUDActions {
			if a == known {
				ok = true
			}
		}
		if !ok {
			add("module %q: unknown crud endpoint %q (valid: %s)", m.Name, a, strings.Join(CRUDActions, ", "))
		}
		crud[a] = true
	}
	customNames := map[string]bool{}
	for _, c := range m.Endpoints.Custom {
		switch {
		case !identRe.MatchString(c.Name):
			add("module %q: custom endpoint name %q must be snake_case", m.Name, c.Name)
		case customNames[c.Name]:
			add("module %q: custom endpoint %q is declared twice", m.Name, c.Name)
		case containsString(CRUDActions, c.Name):
			add("module %q: custom endpoint %q clashes with the built-in %q endpoint", m.Name, c.Name, c.Name)
		}
		customNames[c.Name] = true
		if !httpMethods[c.Method] {
			add("module %q: custom endpoint %q has invalid method %q", m.Name, c.Name, c.Method)
		}
		if !strings.HasPrefix(c.Path, "/") {
			add("module %q: custom endpoint %q: path must start with /", m.Name, c.Name)
		}
		for _, p := range pathParamRe.FindAllString(c.Path, -1) {
			if p != "{id}" {
				add("module %q: custom endpoint %q: only the {id} path parameter is supported (got %s)", m.Name, c.Name, p)
			}
		}
	}

	// Security
	for action := range m.Security.Permissions {
		if !crud[action] && !customNames[action] {
			add("module %q: security.permissions references unknown endpoint %q", m.Name, action)
		}
	}
	if m.Security.OwnerScoped != "" {
		if _, ok := m.Field(m.Security.OwnerScoped); !ok {
			add("module %q: security.owner_scoped references unknown field %q", m.Name, m.Security.OwnerScoped)
		}
	}
}

func validateField(module string, f Field, seen map[string]bool, add func(string, ...any)) {
	ctx := fmt.Sprintf("module %q field %q", module, f.Name)

	if !identRe.MatchString(f.Name) {
		add("%s: name must be snake_case (a-z, 0-9, _)", ctx)
		return
	}
	if reservedFields[f.Name] {
		add("%s: name is reserved (id, created_at and updated_at are added automatically)", ctx)
		return
	}
	if seen[f.Name] {
		add("%s: declared twice", ctx)
		return
	}
	seen[f.Name] = true

	if !containsString(TypeNames, f.Type) {
		add("%s: unknown type %q (valid: %s)", ctx, f.Type, strings.Join(TypeNames, ", "))
		return
	}
	if f.Type == "enum" && len(f.Values) == 0 {
		add("%s: enum requires values: [a, b, c]", ctx)
	}
	if f.Type != "enum" && len(f.Values) > 0 {
		add("%s: values is only valid for type enum", ctx)
	}
	for _, v := range f.Values {
		if v == "" || strings.ContainsAny(v, " \t\"'`\\") {
			add("%s: enum value %q must be non-empty and contain no spaces or quotes", ctx, v)
		}
	}
	if f.Required && f.Nullable {
		add("%s: required and nullable contradict each other", ctx)
	}
	if f.Unique && f.Nullable {
		add("%s: unique fields cannot be nullable", ctx)
	}
	if f.Unique && !f.Required {
		add("%s: unique fields must also be required (otherwise every row without a value would collide on \"\")", ctx)
	}
	if f.Unique && !uniqueTypes[f.Type] {
		add("%s: type %s cannot be unique", ctx, f.Type)
	}
	if f.Format != "" {
		if f.Type != "string" {
			add("%s: format is only valid for type string", ctx)
		} else if f.Format != "email" && f.Format != "url" {
			add("%s: format must be email or url", ctx)
		}
	}
	if (f.Min != nil || f.Max != nil) && !rangeTypes[f.Type] {
		add("%s: min/max are not supported for type %s", ctx, f.Type)
	}
	if f.Min != nil && f.Max != nil && *f.Min > *f.Max {
		add("%s: min is greater than max", ctx)
	}
	if f.Default != "" {
		validateDefault(ctx, f, add)
	}
	if f.Ref != "" {
		if !containsString(IDTypes, f.Type) {
			add("%s: ref requires type %s (matching the referenced id)", ctx, strings.Join(IDTypes, ", "))
		}
		if !refRe.MatchString(f.Ref) {
			add("%s: ref must look like table.column (got %q)", ctx, f.Ref)
		}
	}
}

func validateDefault(ctx string, f Field, add func(string, ...any)) {
	if f.Nullable {
		add("%s: default is not supported on nullable fields", ctx)
		return
	}
	switch f.Type {
	case "int", "bigint":
		if _, err := strconv.ParseInt(f.Default, 10, 64); err != nil {
			add("%s: default %q is not an integer", ctx, f.Default)
		}
	case "float", "decimal":
		if _, err := strconv.ParseFloat(f.Default, 64); err != nil {
			add("%s: default %q is not a number", ctx, f.Default)
		}
	case "bool":
		if f.Default != "true" && f.Default != "false" {
			add("%s: default must be true or false", ctx)
		}
	case "enum":
		if !containsString(f.Values, f.Default) {
			add("%s: default %q is not one of the enum values", ctx, f.Default)
		}
	case "uuid", "timestamp", "text":
		add("%s: default is not supported for type %s", ctx, f.Type)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ── Authoring helpers ────────────────────────────────────────────────────

// ParseFieldSpec parses the CLI shorthand name:type[:opt,opt,...].
// Options: required nullable unique index email url min=N max=N default=X values=a|b|c ref=table.col
func ParseFieldSpec(spec string) (Field, error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
		return Field{}, fmt.Errorf("invalid --field %q (expected name:type[:opt,opt])", spec)
	}
	f := Field{Name: naming.Snake(parts[0]), Type: strings.ToLower(strings.TrimSpace(parts[1]))}

	if len(parts) == 3 {
		for _, opt := range strings.Split(parts[2], ",") {
			opt = strings.TrimSpace(opt)
			if opt == "" {
				continue
			}
			key, val, _ := strings.Cut(opt, "=")
			switch key {
			case "required":
				f.Required = true
			case "nullable":
				f.Nullable = true
			case "unique":
				f.Unique = true
			case "index":
				f.Index = true
			case "email", "url":
				f.Format = key
			case "default":
				f.Default = val
			case "ref":
				f.Ref = val
			case "values":
				f.Values = strings.Split(val, "|")
			case "min", "max":
				n, err := strconv.Atoi(val)
				if err != nil {
					return Field{}, fmt.Errorf("invalid --field %q: %s must be an integer", spec, key)
				}
				if key == "min" {
					f.Min = &n
				} else {
					f.Max = &n
				}
			default:
				return Field{}, fmt.Errorf("invalid --field %q: unknown option %q", spec, key)
			}
		}
	}
	return f, nil
}

// RenderModule renders a module as YAML to append under `modules:`.
// Call it on a module that has not been normalized so defaults are not written out.
func RenderModule(m Module) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s:\n", m.Name)
	if m.Table != "" {
		fmt.Fprintf(&b, "    table: %s\n", m.Table)
	}
	if m.Plural != "" {
		fmt.Fprintf(&b, "    plural: %s\n", m.Plural)
	}
	if m.ID != "" {
		fmt.Fprintf(&b, "    id: %s\n", m.ID)
	}
	b.WriteString("    fields:\n")
	for _, f := range m.Fields {
		fmt.Fprintf(&b, "      %s: %s\n", f.Name, renderField(f))
	}
	if len(m.Endpoints.Custom) > 0 {
		b.WriteString("    endpoints:\n      custom:\n")
		for _, c := range m.Endpoints.Custom {
			fmt.Fprintf(&b, "        - { name: %s, method: %s, path: %s }\n", c.Name, strings.ToUpper(c.Method), strconv.Quote(c.Path))
		}
	}
	return b.String()
}

func renderField(f Field) string {
	parts := []string{"type: " + f.Type}
	if f.Required {
		parts = append(parts, "required: true")
	}
	if f.Nullable {
		parts = append(parts, "nullable: true")
	}
	if f.Unique {
		parts = append(parts, "unique: true")
	}
	if f.Index {
		parts = append(parts, "index: true")
	}
	if f.Format != "" {
		parts = append(parts, "format: "+f.Format)
	}
	if f.Min != nil {
		parts = append(parts, "min: "+strconv.Itoa(*f.Min))
	}
	if f.Max != nil {
		parts = append(parts, "max: "+strconv.Itoa(*f.Max))
	}
	if f.Default != "" {
		parts = append(parts, "default: "+strconv.Quote(f.Default))
	}
	if len(f.Values) > 0 {
		quoted := make([]string, len(f.Values))
		for i, v := range f.Values {
			quoted[i] = strconv.Quote(v)
		}
		parts = append(parts, "values: ["+strings.Join(quoted, ", ")+"]")
	}
	if f.Validate != "" {
		parts = append(parts, "validate: "+strconv.Quote(f.Validate))
	}
	if f.Ref != "" {
		parts = append(parts, "ref: "+f.Ref)
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// AppendModule appends a module under `modules:` without touching comments or formatting.
// It requires `modules:` to be the last top-level key (the layout `goforge new` writes).
func AppendModule(path string, m Module) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(b)

	loc := modulesKey.FindStringIndex(text)
	if loc == nil {
		return fmt.Errorf("%w: no block-style `modules:` key found", ErrNotEditable)
	}
	for _, line := range strings.Split(text[loc[1]:], "\n") {
		if line != "" && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
			return fmt.Errorf("%w: `modules:` must be the last top-level key", ErrNotEditable)
		}
	}

	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += RenderModule(m)
	return os.WriteFile(path, []byte(text), 0o644)
}

// AddMiddleware registers a middleware name under the top-level `middleware:` list.
func AddMiddleware(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")

	for i, line := range lines {
		if !strings.HasPrefix(line, "middleware:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "middleware:"))
		if idx := strings.Index(rest, "#"); idx >= 0 {
			rest = strings.TrimSpace(rest[:idx])
		}

		insertAt := i + 1
		switch rest {
		case "[]":
			lines[i] = "middleware:"
		case "":
			for insertAt < len(lines) && strings.HasPrefix(strings.TrimLeft(lines[insertAt], " "), "- ") {
				if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[insertAt]), "-")) == name {
					return nil // already registered
				}
				insertAt++
			}
		default:
			return fmt.Errorf("%w: write `middleware:` as a block list", ErrNotEditable)
		}

		entry := "  - " + name
		lines = append(lines[:insertAt], append([]string{entry}, lines[insertAt:]...)...)
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
	}
	return fmt.Errorf("%w: no `middleware:` key found", ErrNotEditable)
}

// RenderInitial returns the forge.yaml written by `goforge new`.
func RenderInitial(p Project) string {
	return fmt.Sprintf(`# forge.yaml - the single source of truth for this project.
# Edit it, then run `+"`goforge sync`"+` to regenerate code, migrations and wiring.
version: 1

project:
  name: %s
  module: %s
  layout: layered      # layered = internal/{handlers,services,repositories,...}
  database: %s   # postgres | mysql
  swagger: %t        # add swaggo annotations to generated handlers
  id_type: %s          # primary key of every module: int | bigint (auto-increment) | uuid

# Global middleware, applied in order (functions in internal/middleware).
# Managed by `+"`goforge generate middleware`"+`; keep it a block list.
middleware: []

# Coming soon: features (auth, rbac, notifications, monitoring).

# Keep `+"`modules:`"+` as the LAST key so `+"`goforge generate module`"+` can append to it.
#
# Example:
#   order:
#     id: uuid           # optional: overrides project.id_type for this module
#     fields:
#       customer_id: { type: int, required: true, ref: customers.id }
#       status:      { type: enum, values: [pending, paid, shipped], default: pending }
#       total:       { type: decimal, required: true }
#       note:        { type: string, nullable: true, max: 500 }
#     endpoints:
#       custom:
#         - { name: cancel, method: POST, path: "/{id}/cancel", permission: "order:cancel" }
modules:
`, p.Name, p.Module, p.Database, p.Swagger, p.IDType)
}

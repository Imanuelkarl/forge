package gen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Imanuelkarl/forge/internal/dialect"
	"github.com/Imanuelkarl/forge/internal/schema"
)

// buildSQL builds the repository statements for a module. It returns the SQL view and a
// copy of the fields with FindBySQL filled in for unique fields.
func buildSQL(m *schema.Module, mv *ModuleView, d dialect.Dialect) (SQLView, []FieldView) {
	q := d.Quote

	cols := []string{"id"}
	scanArgs := []string{"&item.ID"}
	for _, f := range mv.Fields {
		cols = append(cols, f.Col)
		scanArgs = append(scanArgs, "&item."+f.GoName)
	}
	cols = append(cols, "created_at", "updated_at")
	scanArgs = append(scanArgs, "&item.CreatedAt", "&item.UpdatedAt")

	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = q(c)
	}
	colList := strings.Join(quoted, ", ")
	table := q(m.Table)
	selectBase := "SELECT " + colList + " FROM " + table

	// INSERT: auto-increment ids are left to the database and read back afterwards.
	insertCols := cols
	itemArgs := make([]string, 0, len(scanArgs))
	for _, a := range scanArgs {
		itemArgs = append(itemArgs, strings.TrimPrefix(a, "&"))
	}
	if mv.ID.Auto {
		insertCols, itemArgs = cols[1:], itemArgs[1:]
	}
	insQuoted := make([]string, len(insertCols))
	placeholders := make([]string, len(insertCols))
	for i, c := range insertCols {
		insQuoted[i] = q(c)
		placeholders[i] = d.Placeholder(i + 1)
	}
	insert := "INSERT INTO " + table + " (" + strings.Join(insQuoted, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	returning := mv.ID.Auto && d.SupportsReturning()
	if returning {
		insert += " RETURNING " + q("id")
	}

	// UPDATE: every field plus updated_at, keyed by id.
	var sets, updArgs []string
	n := 0
	for _, f := range mv.Fields {
		n++
		sets = append(sets, q(f.Col)+" = "+d.Placeholder(n))
		updArgs = append(updArgs, "item."+f.GoName)
	}
	n++
	sets = append(sets, q("updated_at")+" = "+d.Placeholder(n))
	updArgs = append(updArgs, "item.UpdatedAt")
	n++
	updArgs = append(updArgs, "item.ID")

	view := SQLView{
		SelectByID: strconv.Quote(selectBase + " WHERE " + q("id") + " = " + d.Placeholder(1)),
		List: strconv.Quote(selectBase + " ORDER BY " + q("created_at") + " DESC, " + q("id") +
			" DESC LIMIT " + d.Placeholder(1) + " OFFSET " + d.Placeholder(2)),
		Count:      strconv.Quote("SELECT COUNT(*) FROM " + table),
		Insert:     strconv.Quote(insert),
		Update:     strconv.Quote("UPDATE " + table + " SET " + strings.Join(sets, ", ") + " WHERE " + q("id") + " = " + d.Placeholder(n)),
		Delete:     strconv.Quote("DELETE FROM " + table + " WHERE " + q("id") + " = " + d.Placeholder(1)),
		ScanArgs:   strings.Join(scanArgs, ", "),
		InsertArgs: strings.Join(itemArgs, ", "),
		UpdateArgs: strings.Join(updArgs, ", "),

		InsertReturning: returning,
		InsertLastID:    mv.ID.Auto && !returning,
	}

	fields := make([]FieldView, len(mv.Fields))
	copy(fields, mv.Fields)
	for i := range fields {
		if fields[i].Unique {
			fields[i].FindBySQL = strconv.Quote(selectBase + " WHERE " + q(fields[i].Col) + " = " + d.Placeholder(1))
		}
	}
	return view, fields
}

func sqlType(f schema.Field, d dialect.Dialect) string {
	switch f.Type {
	case "string":
		n := 255
		if f.Max != nil {
			n = *f.Max
		}
		return fmt.Sprintf("varchar(%d)", n)
	case "text":
		return "text"
	case "uuid":
		return d.UUIDType()
	case "enum":
		return "varchar(64)"
	case "int":
		return "integer"
	case "bigint":
		return "bigint"
	case "float":
		return "double precision"
	case "decimal":
		return "decimal(18,2)"
	case "bool":
		return "boolean"
	default: // timestamp
		return d.TimestampType()
	}
}

func sqlDefault(f schema.Field) string {
	if f.Default == "" {
		return ""
	}
	switch f.Type {
	case "int", "bigint", "float", "decimal":
		return " DEFAULT " + f.Default
	case "bool":
		return " DEFAULT " + strings.ToUpper(f.Default)
	default:
		return " DEFAULT '" + strings.ReplaceAll(f.Default, "'", "''") + "'"
	}
}

// migrationSQL builds the initial up/down migration for the module at idx.
// Foreign keys are only emitted for tables created by modules declared earlier,
// so the migration order always satisfies them.
func migrationSQL(modules schema.Modules, idx int, d dialect.Dialect) (up, down string) {
	m := modules[idx]
	q := d.Quote

	var defs, notes, indexes []string
	switch m.ID {
	case "int", "bigint":
		defs = append(defs, q("id")+" "+d.AutoIncrementPK(m.ID == "bigint"))
	default: // uuid
		defs = append(defs, q("id")+" "+d.UUIDType()+" NOT NULL PRIMARY KEY")
	}

	for _, f := range m.Fields {
		null := "NOT NULL"
		if f.Nullable {
			null = "NULL"
		}
		defs = append(defs, q(f.Name)+" "+sqlType(f, d)+" "+null+sqlDefault(f))
	}
	ts := d.TimestampType()
	defs = append(defs, q("created_at")+" "+ts+" NOT NULL", q("updated_at")+" "+ts+" NOT NULL")

	for _, f := range m.Fields {
		if f.Ref != "" {
			refTable, refCol, _ := strings.Cut(f.Ref, ".")
			if refCol == "id" && tableDeclaredBefore(modules, idx, refTable) {
				name := "fk_" + m.Table + "_" + f.Name
				defs = append(defs, fmt.Sprintf("CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
					q(name), q(f.Name), q(refTable), q("id")))
			} else {
				notes = append(notes, fmt.Sprintf("-- NOTE: %s.%s references %s, which no earlier module creates; add the foreign key once that table exists.",
					m.Table, f.Name, f.Ref))
			}
		}

		switch {
		case f.Unique:
			name := "uq_" + m.Table + "_" + f.Name
			if d.InlineIndexes() {
				defs = append(defs, fmt.Sprintf("UNIQUE KEY %s (%s)", q(name), q(f.Name)))
			} else {
				indexes = append(indexes, fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s);", q(name), q(m.Table), q(f.Name)))
			}
		case f.Index || f.Ref != "":
			name := "idx_" + m.Table + "_" + f.Name
			if d.InlineIndexes() {
				defs = append(defs, fmt.Sprintf("KEY %s (%s)", q(name), q(f.Name)))
			} else {
				indexes = append(indexes, fmt.Sprintf("CREATE INDEX %s ON %s (%s);", q(name), q(m.Table), q(f.Name)))
			}
		}
	}

	var b strings.Builder
	for _, n := range notes {
		b.WriteString(n + "\n")
	}
	b.WriteString("CREATE TABLE " + q(m.Table) + " (\n    " + strings.Join(defs, ",\n    ") + "\n)")
	if d.InlineIndexes() {
		b.WriteString(" ENGINE=InnoDB DEFAULT CHARSET=utf8mb4")
	}
	b.WriteString(";\n")
	if len(indexes) > 0 {
		b.WriteString("\n" + strings.Join(indexes, "\n") + "\n")
	}

	return b.String(), "DROP TABLE IF EXISTS " + q(m.Table) + ";\n"
}

func tableDeclaredBefore(modules schema.Modules, idx int, table string) bool {
	for i := 0; i < idx; i++ {
		if modules[i].Table == table {
			return true
		}
	}
	return false
}

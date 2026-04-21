package postgres

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/zclconf/go-cty/cty"
)

// executeCopy implements the "copy" action: bulk-load rows via
// PostgreSQL's COPY FROM STDIN protocol.
//
// The action supports two mutually exclusive data sources:
//   - `data` — inline string (may come from a variable, a heredoc,
//     or another action's stdout).
//   - `file` — absolute path to a data file (resolved by the engine
//     before this function sees it).
//
// Format, delimiter, and header map directly to COPY's WITH clause.
// The return value is { rows_copied = <n> }.
func (p *Provider) executeCopy(ctx context.Context, attrs map[string]cty.Value) (map[string]cty.Value, error) {
	table := attrs["table"].AsString()
	if table == "" {
		return nil, fmt.Errorf("postgres: copy: table is required")
	}

	columns, err := extractColumns(attrs["columns"])
	if err != nil {
		return nil, fmt.Errorf("postgres: copy: %w", err)
	}

	format := "csv"
	if v, ok := attrs["format"]; ok && !v.IsNull() {
		format = v.AsString()
	}

	header := false
	if v, ok := attrs["header"]; ok && !v.IsNull() {
		header = v.True()
	}

	var delimiter string
	if v, ok := attrs["delimiter"]; ok && !v.IsNull() {
		delimiter = v.AsString()
	}

	reader, cleanup, err := openDataSource(attrs)
	if err != nil {
		return nil, fmt.Errorf("postgres: copy: %w", err)
	}
	defer cleanup()

	sql := buildCopySQL(table, columns, format, header, delimiter)

	conn, err := p.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: copy: acquire connection: %w", err)
	}
	defer conn.Release()

	tag, err := conn.Conn().PgConn().CopyFrom(ctx, reader, sql)
	if err != nil {
		return nil, fmt.Errorf("postgres: copy %s: %w", table, err)
	}

	return map[string]cty.Value{
		"rows_copied": cty.NumberIntVal(tag.RowsAffected()),
	}, nil
}

// openDataSource returns an io.Reader for the COPY data and a
// cleanup function. Exactly one of `data` or `file` must be set.
func openDataSource(attrs map[string]cty.Value) (io.Reader, func(), error) {
	hasData := false
	if v, ok := attrs["data"]; ok && !v.IsNull() && v.AsString() != "" {
		hasData = true
	}
	hasFile := false
	if v, ok := attrs["file"]; ok && !v.IsNull() && v.AsString() != "" {
		hasFile = true
	}

	if hasData && hasFile {
		return nil, nil, fmt.Errorf("data and file are mutually exclusive; provide one or the other")
	}
	if !hasData && !hasFile {
		return nil, nil, fmt.Errorf("either data or file is required")
	}

	if hasData {
		return strings.NewReader(attrs["data"].AsString()), func() {}, nil
	}

	path := attrs["file"].AsString()
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// buildCopySQL assembles the COPY ... FROM STDIN WITH (...) command.
// Table and column names are sanitised via pgx.Identifier to prevent
// SQL injection.
func buildCopySQL(table string, columns []string, format string, header bool, delimiter string) string {
	colIdents := make([]string, len(columns))
	for i, c := range columns {
		colIdents[i] = pgx.Identifier{c}.Sanitize()
	}

	var withParts []string

	switch strings.ToLower(format) {
	case "tsv":
		withParts = append(withParts, "FORMAT csv") // csv + tab delimiter
		if delimiter == "" {
			delimiter = "\t"
		}
	case "text":
		withParts = append(withParts, "FORMAT text")
	default: // csv
		withParts = append(withParts, "FORMAT csv")
	}

	if delimiter != "" {
		// Postgres COPY expects the delimiter as a single-char string.
		withParts = append(withParts, fmt.Sprintf("DELIMITER %s", pgx.Identifier{delimiter}.Sanitize()))
	}
	if header {
		withParts = append(withParts, "HEADER true")
	}

	return fmt.Sprintf("COPY %s (%s) FROM STDIN WITH (%s)",
		pgx.Identifier{table}.Sanitize(),
		strings.Join(colIdents, ", "),
		strings.Join(withParts, ", "),
	)
}

// extractColumns pulls a cty list(string) into a Go slice.
func extractColumns(v cty.Value) ([]string, error) {
	if v.IsNull() {
		return nil, fmt.Errorf("columns is required")
	}
	var out []string
	it := v.ElementIterator()
	for it.Next() {
		_, el := it.Element()
		if el.Type() != cty.String {
			return nil, fmt.Errorf("columns must be a list of strings")
		}
		out = append(out, el.AsString())
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("columns must not be empty")
	}
	return out, nil
}

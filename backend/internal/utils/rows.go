package utils

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
)

// Rows runs a query and returns every row as column name → value. Send the result with c.JSON:
// Go's encoding/json turns it into the response.
func Rows(ctx context.Context, database *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{} // empty list, not null, when there are no rows
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col.Name()] = jsonValue(col.DatabaseTypeName(), vals[i])
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// Row returns the first row of a query, or sql.ErrNoRows when there is none.
func Row(ctx context.Context, database *sql.DB, query string, args ...any) (map[string]any, error) {
	rows, err := Rows(ctx, database, query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}

// SendRow sends the first row of a query as the response (404 when there is none).
func SendRow(c *gin.Context, database *sql.DB, query string, args ...any) {
	row, err := Row(c, database, query, args...)
	if err != nil {
		Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, row)
}

// SendRows sends all rows of a query as a JSON list.
func SendRows(c *gin.Context, database *sql.DB, query string, args ...any) {
	rows, err := Rows(c, database, query, args...)
	if err != nil {
		Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}

// SendPage sends one page of a query's rows (see Page).
func SendPage(c *gin.Context, database *sql.DB, query string, args ...any) {
	items, total, limit, offset, err := Page(c, database, query, args...)
	if err != nil {
		Fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// Page returns one page of a query's rows plus the total row count, for {"items", "total", "limit", "offset"}.
// ?limit= and ?offset= in the URL pick the page. query is a SELECT without LIMIT/OFFSET.
func Page(c *gin.Context, database *sql.DB, query string, args ...any) (items []map[string]any, total, limit, offset int, err error) {
	limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	limitArg, offsetArg := "$"+strconv.Itoa(len(args)+1), "$"+strconv.Itoa(len(args)+2) // after the query's own $1, $2…
	items, err = Rows(c, database, query+" LIMIT "+limitArg+" OFFSET "+offsetArg, append(args, limit, offset)...)
	if err != nil {
		return
	}
	err = database.QueryRowContext(c, `SELECT count(*) FROM (`+query+`) t`, args...).Scan(&total)
	return
}

// jsonValue converts a scanned database value into what the frontend expects in JSON.
func jsonValue(dbType string, v any) any {
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	switch x := v.(type) {
	case string:
		switch {
		case dbType == "NUMERIC": // money and quantities: send as numbers
			if f, err := strconv.ParseFloat(x, 64); err == nil {
				return f
			}
		case dbType == "JSON" || dbType == "JSONB": // already JSON: pass through unchanged
			return json.RawMessage(x)
		case strings.HasPrefix(dbType, "_"): // a Postgres array such as TEXT[]
			var arr []string
			if err := pgtype.NewMap().SQLScanner(&arr).Scan(x); err == nil {
				return arr
			}
		}
	case time.Time:
		if dbType == "DATE" { // plain dates as 2026-10-06, like before
			return x.Format("2006-01-02")
		}
	}
	return v
}

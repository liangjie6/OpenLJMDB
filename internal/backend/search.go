package backend

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Highlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}
type SearchResult struct {
	DocumentID        string      `json:"document_id"`
	ID                string      `json:"id"`
	KnowledgeBaseID   string      `json:"knowledge_base_id"`
	KnowledgeBaseName string      `json:"knowledge_base_name"`
	Title             string      `json:"title"`
	UpdatedAt         int64       `json:"updated_at"`
	Snippet           string      `json:"snippet"`
	Highlights        []Highlight `json:"highlights"`
	TitleHighlights   []Highlight `json:"title_highlights"`
	SearchMode        string      `json:"search_mode"`
	score             float64
	titleRank         int
	text              string
	fts               bool
}

// literalFTSQuery never exposes FTS operators to the HTTP search box.
func literalFTSQuery(q string) (string, bool) {
	var terms []string
	var token strings.Builder
	flush := func() {
		if token.Len() > 0 {
			terms = append(terms, `"`+strings.ReplaceAll(token.String(), `"`, `""`)+`"`)
			token.Reset()
		}
	}
	for _, r := range q {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			token.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return strings.Join(terms, " AND "), len(terms) > 0
}

func escapeLike(q string) string {
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, "%", `\%`)
	return strings.ReplaceAll(q, "_", `\_`)
}
func hasChinese(q string) bool {
	for _, r := range q {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}
func needsLiteralFallback(q string) bool {
	for _, r := range q {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) && !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func runeHighlights(text, query string) []Highlight {
	textRunes, queryRunes := []rune(text), []rune(query)
	marks := []Highlight{}
	if len(queryRunes) == 0 {
		return marks
	}
	for i := 0; i+len(queryRunes) <= len(textRunes); {
		match := true
		for j, r := range queryRunes {
			if unicode.ToLower(textRunes[i+j]) != unicode.ToLower(r) {
				match = false
				break
			}
		}
		if match {
			marks = append(marks, Highlight{i, i + len(queryRunes)})
			i += len(queryRunes)
		} else {
			i++
		}
	}
	return marks
}

func searchSnippet(text, q string) (string, []Highlight) {
	runes := []rune(text)
	marks := runeHighlights(text, q)
	start := 0
	if len(marks) > 0 && marks[0].Start > 45 {
		start = marks[0].Start - 45
	}
	end := start + 180
	if end > len(runes) {
		end = len(runes)
	}
	if start > len(runes) {
		start = len(runes)
	}
	snippet := string(runes[start:end])
	offset := 0
	if start > 0 {
		snippet = "…" + snippet
		offset = 1
	}
	if end < len(runes) {
		snippet += "…"
	}
	shown := []Highlight{}
	for _, mark := range marks {
		if mark.Start >= start && mark.End <= end {
			shown = append(shown, Highlight{mark.Start - start + offset, mark.End - start + offset})
		}
	}
	return snippet, shown
}

func (a *App) search(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	if len(values["q"]) != 1 {
		WriteError(w, r, Err(400, "INVALID_SEARCH_QUERY", "请提供一个搜索关键词", nil))
		return
	}
	q := strings.TrimSpace(values.Get("q"))
	if q == "" || !utf8.ValidString(q) || utf8.RuneCountInString(q) > 200 {
		WriteError(w, r, Err(400, "INVALID_SEARCH_QUERY", "搜索关键词须为 1 到 200 个字符", nil))
		return
	}
	page, size, err := Page(r)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	kbID := values.Get("knowledge_base_id")
	if kbID != "" && !ValidID(kbID) {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "知识库 ID 无效", nil))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	fail := func(err error) {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = Err(503, "SEARCH_TIMEOUT", "搜索超过执行时间限制，请限定知识库或使用更具体的关键词后重试", map[string]any{"timeout_ms": 5000})
		}
		WriteError(w, r, err)
	}
	tx, err := a.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fail(err)
		return
	}
	defer tx.Rollback()
	if kbID != "" {
		var exists int
		err = tx.QueryRowContext(ctx, "SELECT 1 FROM knowledge_bases WHERE id=? AND deleted_at IS NULL", kbID).Scan(&exists)
		if err == sql.ErrNoRows {
			WriteError(w, r, Err(404, "NOT_FOUND", "知识库不存在", nil))
			return
		}
		if err != nil {
			fail(err)
			return
		}
	}
	ftsQuery, ftsEnabled := literalFTSQuery(q)
	ftsEnabled = ftsEnabled && !needsLiteralFallback(q)
	activeWhere := "d.deleted_at IS NULL AND k.deleted_at IS NULL"
	kbArgs := []any{}
	if kbID != "" {
		activeWhere += " AND d.knowledge_base_id=?"
		kbArgs = append(kbArgs, kbID)
	}
	ftsSQL := "SELECT d.row_id,bm25(documents_fts,5.0,1.0) AS score FROM documents_fts JOIN documents d ON d.row_id=documents_fts.rowid JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE documents_fts MATCH ? AND " + activeWhere
	ftsArgs := append([]any{ftsQuery}, kbArgs...)
	hasFTS := false
	if ftsEnabled {
		existsQuery := "SELECT EXISTS(SELECT 1 FROM documents_fts JOIN documents d ON d.row_id=documents_fts.rowid JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE documents_fts MATCH ? AND " + activeWhere + " LIMIT 1)"
		if err = tx.QueryRowContext(ctx, existsQuery, ftsArgs...).Scan(&hasFTS); err != nil {
			fail(err)
			return
		}
	}
	likeEnabled := hasChinese(q) || !ftsEnabled || !hasFTS
	mode := "fts"
	if likeEnabled {
		if hasFTS {
			mode = "fts+like"
		} else {
			mode = "like"
		}
	}
	pattern := "%" + escapeLike(q) + "%"
	likeSQL := `SELECT d.row_id FROM documents d JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE ` + activeWhere + ` AND CASE WHEN EXISTS(SELECT 1 FROM fts_matches f WHERE f.row_id=d.row_id) THEN 0 ELSE (d.title LIKE ? ESCAPE '\' OR d.search_text LIKE ? ESCAPE '\') END`
	likeArgs := append(append([]any{}, kbArgs...), pattern, pattern)
	args := []any{}
	if !ftsEnabled {
		ftsSQL = "SELECT 0 AS row_id,0.0 AS score WHERE 0"
	} else {
		args = append(args, ftsArgs...)
	}
	if !likeEnabled {
		likeSQL = "SELECT 0 AS row_id WHERE 0"
	} else {
		args = append(args, likeArgs...)
	}
	// MATERIALIZED keeps bm25 in its required FTS cursor context. Merge only
	// integer IDs and scores; retrieve search text for the requested page.
	cte := "WITH fts_matches AS MATERIALIZED (" + ftsSQL + "),like_matches AS (" + likeSQL + "),matches AS (SELECT row_id FROM fts_matches UNION SELECT row_id FROM like_matches) "
	var total int
	// Count and paginate the merged IDs in one pass. Ranking/window state
	// carries only metadata; fetch full search text after LIMIT has selected
	// the page. An empty out-of-range page uses a separate count below.
	countCTE := strings.Replace(cte, "bm25(documents_fts,5.0,1.0) AS score", "0.0 AS score", 1)
	query := cte + `, paged AS (
		SELECT m.row_id, f.score IS NOT NULL AS has_fts, count(*) OVER() AS total,
		CASE WHEN d.title = ? COLLATE NOCASE THEN 0 WHEN d.title LIKE ? ESCAPE '\' THEN 1 ELSE 2 END AS title_rank,
		CASE WHEN f.score IS NOT NULL THEN 0 ELSE 1 END AS fts_order,f.score,d.updated_at,d.id AS document_id
		FROM matches m JOIN documents d ON d.row_id=m.row_id LEFT JOIN fts_matches f ON f.row_id=m.row_id
		ORDER BY title_rank,fts_order,f.score ASC,d.updated_at DESC,d.id ASC LIMIT ? OFFSET ?
	) SELECT d.id,d.knowledge_base_id,k.name,d.title,d.search_text,d.updated_at,p.has_fts,p.total
	FROM paged p JOIN documents d ON d.row_id=p.row_id JOIN knowledge_bases k ON k.id=d.knowledge_base_id
	ORDER BY p.title_rank,p.fts_order,p.score ASC,p.updated_at DESC,p.document_id ASC`

	pageArgs := append(append([]any{}, args...), q, pattern, size, (page-1)*size)
	rows, err := tx.QueryContext(ctx, query, pageArgs...)
	if err != nil {
		fail(err)
		return
	}
	data := make([]SearchResult, 0, size)
	for rows.Next() {
		var result SearchResult
		if err = rows.Scan(&result.DocumentID, &result.KnowledgeBaseID, &result.KnowledgeBaseName, &result.Title, &result.text, &result.UpdatedAt, &result.fts, &total); err != nil {
			rows.Close()
			fail(err)
			return
		}
		result.ID = result.DocumentID
		result.SearchMode = "like"
		if result.fts {
			result.SearchMode = "fts"
		}
		result.TitleHighlights = runeHighlights(result.Title, q)
		result.Snippet, result.Highlights = searchSnippet(result.text, q)
		if result.fts && len(result.Highlights) == 0 {
			for _, term := range strings.Fields(q) {
				snippet, marks := searchSnippet(result.text, term)
				if len(marks) > 0 {
					result.Snippet, result.Highlights = snippet, marks
					break
				}
			}
		}
		data = append(data, result)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(err)
		return
	}
	if len(data) == 0 && page > 1 {
		if err = tx.QueryRowContext(ctx, countCTE+"SELECT count(*) FROM matches", args...).Scan(&total); err != nil {
			fail(err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(err)
		return
	}
	WriteData(w, 200, data, map[string]any{"total": total, "page": page, "page_size": size, "search_mode": mode, "query": q})
}

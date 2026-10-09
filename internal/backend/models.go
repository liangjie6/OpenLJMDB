package backend

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

func utf8Valid(s string) bool { return utf8.ValidString(s) }

// MaxTreeNodes bounds a complete tree response and the associated in-memory
// integrity checks. Deleted documents do not occupy the active tree budget.
const MaxTreeNodes = 10000

type KnowledgeBase struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Description   string  `json:"description"`
	SortOrder     int     `json:"sort_order"`
	TreeRevision  int     `json:"tree_revision"`
	DocumentCount int     `json:"document_count"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
	DeletedAt     *int64  `json:"deleted_at,omitempty"`
	DeleteBatchID *string `json:"delete_batch_id,omitempty"`
}
type Document struct {
	ID              string  `json:"id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	ParentID        *string `json:"parent_id"`
	Title           string  `json:"title"`
	Markdown        string  `json:"markdown"`
	HTML            string  `json:"html"`
	SearchText      string  `json:"-"`
	RenderVersion   string  `json:"render_version"`
	SortOrder       int     `json:"sort_order"`
	Revision        int     `json:"revision"`
	CreatedAt       int64   `json:"created_at"`
	UpdatedAt       int64   `json:"updated_at"`
	DeletedAt       *int64  `json:"deleted_at,omitempty"`
	DeleteBatchID   *string `json:"delete_batch_id,omitempty"`
}
type TreeNode struct {
	ID        string      `json:"id"`
	ParentID  *string     `json:"parent_id"`
	Title     string      `json:"title"`
	SortOrder int         `json:"sort_order"`
	Revision  int         `json:"revision"`
	UpdatedAt int64       `json:"updated_at"`
	Children  []*TreeNode `json:"children"`
}
type Revision struct {
	ID         string `json:"id"`
	DocumentID string `json:"document_id"`
	Revision   int    `json:"revision"`
	Title      string `json:"title"`
	Markdown   string `json:"markdown"`
	Reason     string `json:"reason"`
	CreatedAt  int64  `json:"created_at"`
}
type TrashBatch struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind"`
	RootDocumentID  *string `json:"root_document_id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	CreatedAt       int64   `json:"created_at"`
	RestoredAt      *int64  `json:"restored_at,omitempty"`
	PurgedAt        *int64  `json:"purged_at,omitempty"`
	Title           string  `json:"title"`
	DocumentCount   int     `json:"document_count"`
}

const documentColumns = "d.id,d.knowledge_base_id,d.parent_id,d.title,d.markdown,d.html,d.search_text,d.render_version,d.sort_order,d.revision,d.created_at,d.updated_at,d.deleted_at,d.delete_batch_id"

type rowScanner interface{ Scan(...any) error }

func scanDocument(row rowScanner) (Document, error) {
	var d Document
	err := row.Scan(&d.ID, &d.KnowledgeBaseID, &d.ParentID, &d.Title, &d.Markdown, &d.HTML, &d.SearchText, &d.RenderVersion, &d.SortOrder, &d.Revision, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt, &d.DeleteBatchID)
	return d, err
}
func LoadDocument(ctx context.Context, q Queryer, id string, activeOnly bool) (Document, error) {
	query := "SELECT " + documentColumns + " FROM documents d JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE d.id=?"
	if activeOnly {
		query += " AND d.deleted_at IS NULL AND k.deleted_at IS NULL"
	}
	d, err := scanDocument(q.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = Err(404, "NOT_FOUND", "文档不存在或已进入回收站", nil)
	}
	return d, err
}
func scanKnowledgeBase(row rowScanner) (KnowledgeBase, error) {
	var k KnowledgeBase
	err := row.Scan(&k.ID, &k.Name, &k.Description, &k.SortOrder, &k.TreeRevision, &k.CreatedAt, &k.UpdatedAt, &k.DeletedAt, &k.DeleteBatchID, &k.DocumentCount)
	return k, err
}

const knowledgeBaseColumns = "k.id,k.name,k.description,k.sort_order,k.tree_revision,k.created_at,k.updated_at,k.deleted_at,k.delete_batch_id,(SELECT count(*) FROM documents d WHERE d.knowledge_base_id=k.id AND d.deleted_at IS NULL)"

func LoadKnowledgeBase(ctx context.Context, q Queryer, id string, activeOnly bool) (KnowledgeBase, error) {
	query := "SELECT " + knowledgeBaseColumns + " FROM knowledge_bases k WHERE k.id=?"
	if activeOnly {
		query += " AND k.deleted_at IS NULL"
	}
	k, err := scanKnowledgeBase(q.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = Err(404, "NOT_FOUND", "知识库不存在或已进入回收站", nil)
	}
	return k, err
}

func treeConflict(k KnowledgeBase, expected int) error {
	if expected < 1 {
		return Err(400, "INVALID_ARGUMENT", "必须提供有效 expected_tree_revision", nil)
	}
	if k.TreeRevision != expected {
		return Err(409, "TREE_REVISION_CONFLICT", "文档树已变化，请刷新后操作", map[string]any{"tree_revision": k.TreeRevision})
	}
	return nil
}
func revisionConflict(d Document, expected int) error {
	if expected < 1 {
		return Err(400, "INVALID_ARGUMENT", "必须提供有效 expected_revision", nil)
	}
	if d.Revision != expected {
		runes := []rune(d.Markdown)
		if len(runes) > 200 {
			runes = runes[:200]
		}
		return Err(409, "REVISION_CONFLICT", "文档已由其他编辑会话更新", map[string]any{"revision": d.Revision, "title": d.Title, "updated_at": d.UpdatedAt, "markdown_summary": string(runes)})
	}
	return nil
}

// ParentDepth checks every ancestor instead of treating a truncated recursive
// query as proof that a damaged tree has no cycle.
func ParentDepth(ctx context.Context, q Queryer, kbID string, parentID *string) (int, error) {
	depth := 0
	seen := map[string]bool{}
	current := parentID
	for current != nil {
		if *current == "" {
			return 0, Err(400, "INVALID_ARGUMENT", "parent_id 不得为空字符串", nil)
		}
		if seen[*current] {
			return 0, Err(409, "TREE_INTEGRITY_ERROR", "文档树存在循环，请执行完整性检查", nil)
		}
		seen[*current] = true
		var libraryID string
		var next *string
		err := q.QueryRowContext(ctx, "SELECT d.knowledge_base_id,d.parent_id FROM documents d JOIN knowledge_bases k ON k.id=d.knowledge_base_id WHERE d.id=? AND d.deleted_at IS NULL AND k.deleted_at IS NULL", *current).Scan(&libraryID, &next)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, Err(404, "NOT_FOUND", "父文档不存在或已进入回收站", nil)
		}
		if err != nil {
			return 0, err
		}
		if libraryID != kbID {
			return 0, Err(400, "INVALID_ARGUMENT", "父文档必须属于目标知识库", nil)
		}
		depth++
		if depth > 32 {
			return 0, Err(409, "TREE_INTEGRITY_ERROR", "已有树深度异常，请执行完整性检查", nil)
		}
		current = next
	}
	return depth, nil
}

func ValidateTree(ctx context.Context, q Queryer, kbID string) error {
	rows, err := q.QueryContext(ctx, "SELECT id,parent_id FROM documents WHERE knowledge_base_id=? AND deleted_at IS NULL LIMIT ?", kbID, MaxTreeNodes+1)
	if err != nil {
		return err
	}
	parents := map[string]*string{}
	for rows.Next() {
		var id string
		var p *string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return err
		}
		parents[id] = p
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(parents) > MaxTreeNodes {
		return Err(413, "TREE_LIMIT_EXCEEDED", "知识库有效文档数量超过文档树限制", map[string]any{"max_tree_nodes": MaxTreeNodes})
	}
	depths := map[string]int{}
	for id := range parents {
		if _, ok := depths[id]; ok {
			continue
		}
		chain := []string{}
		seen := map[string]bool{}
		cur := id
		base := 0
		for {
			if d, ok := depths[cur]; ok {
				base = d
				break
			}
			if seen[cur] {
				return Err(409, "TREE_INTEGRITY_ERROR", "文档树存在循环，请执行修复", nil)
			}
			p, ok := parents[cur]
			if !ok {
				return Err(409, "TREE_INTEGRITY_ERROR", "文档树父节点不存在或不可用", nil)
			}
			seen[cur] = true
			chain = append(chain, cur)
			if p == nil {
				break
			}
			cur = *p
		}
		for i := len(chain) - 1; i >= 0; i-- {
			base++
			if base > 32 {
				return Err(409, "TREE_DEPTH_EXCEEDED", "文档树超过 32 层限制", map[string]any{"max_depth": 32})
			}
			depths[chain[i]] = base
		}
	}
	return nil
}

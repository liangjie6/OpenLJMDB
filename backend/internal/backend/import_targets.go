package backend

import "context"

// Markdown files may have separate destinations while sharing one transaction,
// attachment preparation and source-to-new-ID mapping. ZIPs keep their tree.
type importTarget struct {
	SourceID              string  `json:"source_id"`
	TargetKnowledgeBaseID string  `json:"target_knowledge_base_id"`
	TargetParentID        *string `json:"target_parent_id"`
	ExpectedTreeRevision  int     `json:"expected_tree_revision"`
}

type importPlan struct {
	Documents map[string]importTarget
	Libraries map[string]KnowledgeBase
}

func resolveImportTargets(ctx context.Context, q Queryer, req importRequest, p ImportPreview) (importPlan, error) {
	plan := importPlan{Documents: map[string]importTarget{}, Libraries: map[string]KnowledgeBase{}}
	invalid := func(message string) (importPlan, error) {
		return plan, Err(400, "INVALID_ARGUMENT", message, nil)
	}
	if len(req.Targets) > 0 {
		if p.SourceKind != "markdown" {
			return invalid("仅独立 Markdown 文件支持逐篇选择位置，ZIP 必须保留原有树结构")
		}
		if len(req.Targets) != len(p.Documents) {
			return invalid("必须为每篇导入文档指定一个位置")
		}
		sources := map[string]bool{}
		for _, d := range p.Documents {
			if d.ParentSourceID != nil || d.Synthetic {
				return invalid("带父子结构的资料包必须使用整包导入位置")
			}
			sources[d.SourceID] = true
		}
		for _, target := range req.Targets {
			if !sources[target.SourceID] {
				return invalid("导入位置包含未知的 source_id")
			}
			if _, exists := plan.Documents[target.SourceID]; exists {
				return invalid("同一篇文档只能指定一个导入位置")
			}
			plan.Documents[target.SourceID] = target
		}
	} else {
		for _, d := range p.Documents {
			plan.Documents[d.SourceID] = importTarget{
				SourceID: d.SourceID, TargetKnowledgeBaseID: req.TargetKnowledgeBaseID,
				TargetParentID: req.TargetParentID, ExpectedTreeRevision: req.ExpectedTreeRevision,
			}
		}
	}
	counts := map[string]int{}
	for _, d := range p.Documents {
		target := plan.Documents[d.SourceID]
		if !ValidID(target.TargetKnowledgeBaseID) {
			return invalid("无效的目标知识库 ID")
		}
		library, loaded := plan.Libraries[target.TargetKnowledgeBaseID]
		if !loaded {
			var err error
			library, err = LoadKnowledgeBase(ctx, q, target.TargetKnowledgeBaseID, true)
			if err != nil {
				return plan, err
			}
			plan.Libraries[library.ID] = library
		}
		if err := treeConflict(library, target.ExpectedTreeRevision); err != nil {
			return plan, err
		}
		depth, err := ParentDepth(ctx, q, library.ID, target.TargetParentID)
		if err != nil {
			return plan, err
		}
		if depth >= 32 {
			return plan, Err(400, "TREE_DEPTH_EXCEEDED", "导入后树深度超过 32 层", nil)
		}
		counts[library.ID]++
	}
	for id, count := range counts {
		library := plan.Libraries[id]
		if library.DocumentCount+count > MaxTreeNodes {
			return plan, Err(413, "ARCHIVE_LIMIT_EXCEEDED", "导入后知识库文档数量超过上限", map[string]any{
				"knowledge_base_id": id, "max_documents": MaxTreeNodes,
				"current_documents": library.DocumentCount, "import_documents": count,
			})
		}
	}
	return plan, nil
}

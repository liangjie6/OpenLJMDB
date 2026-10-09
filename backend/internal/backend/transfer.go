package backend

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// These are portable offline reading resources, not the application's frontend.
//
//go:embed exportassets
var offlineAssets embed.FS

type ContentDocument struct {
	SourceID       string  `json:"source_id"`
	Title          string  `json:"title"`
	ParentSourceID *string `json:"parent_source_id"`
	SortOrder      int     `json:"sort_order"`
	Path           string  `json:"path"`
	Markdown       string  `json:"-"`
	SHA256         string  `json:"sha256,omitempty"`
	Synthetic      bool    `json:"synthetic,omitempty"`
}
type ContentAttachment struct {
	SourceID     string `json:"source_id"`
	OriginalName string `json:"original_name"`
	MediaType    string `json:"media_type"`
	Path         string `json:"path"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
}
type ContentManifest struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"format_version"`
	CreatedAt     string `json:"created_at"`
	KnowledgeBase struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"knowledge_base"`
	Documents       []ContentDocument   `json:"documents"`
	Attachments     []ContentAttachment `json:"attachments"`
	UnresolvedLinks []string            `json:"unresolved_links"`
}
type ImportPreview struct {
	ID          string              `json:"preview_id"`
	SourceKind  string              `json:"source_kind"`
	ExpiresAt   int64               `json:"expires_at"`
	Documents   []ContentDocument   `json:"documents"`
	Attachments []ContentAttachment `json:"attachments"`
	Warnings    []string            `json:"warnings"`
	TotalBytes  int64               `json:"total_bytes"`
}
type operationJob struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"`
	Phase        string  `json:"phase,omitempty"`
	State        string  `json:"state"`
	Progress     int     `json:"progress"`
	ResultPath   *string `json:"-"`
	ErrorCode    *string `json:"error_code,omitempty"`
	ErrorSummary *string `json:"error_summary,omitempty"`
	CreatedAt    int64   `json:"created_at"`
	UpdatedAt    int64   `json:"updated_at"`
	DownloadURL  string  `json:"download_url,omitempty"`
	Result       any     `json:"result,omitempty"`
}

func (a *App) RegisterTransfer(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/imports/preflight", a.importPreflight)
	mux.HandleFunc("POST /api/v1/imports/classify", a.importClassify)
	mux.HandleFunc("POST /api/v1/imports", a.importCommit)
	mux.HandleFunc("POST /api/v1/exports", a.exportCreate)
	mux.HandleFunc("POST /api/v1/backups", a.backupCreate)
	mux.HandleFunc("GET /api/v1/backups", a.backupList)
	mux.HandleFunc("POST /api/v1/backups/validate", a.backupValidate)
	mux.HandleFunc("POST /api/v1/backups/restore", a.backupRestore)
	mux.HandleFunc("GET /api/v1/jobs/{id}", a.jobGet)
	mux.HandleFunc("GET /api/v1/jobs/{id}/download", a.jobDownload)
}

func readJSONFile(filename string, dst any) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 32<<20))
	dec.DisallowUnknownFields()
	if err = dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err = dec.Decode(&trailing); err != io.EOF {
		return errors.New("unexpected trailing JSON")
	}
	return nil
}
func receiveUpload(r *http.Request, dest string, limit int64) (string, error) {
	var src io.Reader = r.Body
	name := r.URL.Query().Get("filename")
	if name == "" {
		name = r.Header.Get("X-Filename")
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		mr, err := r.MultipartReader()
		if err != nil {
			return "", err
		}
		found := false
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if part.FileName() == "" {
				if _, err = io.Copy(io.Discard, io.LimitReader(part, 64<<10)); err != nil {
					return "", err
				}
				part.Close()
				continue
			}
			src = part
			name = part.FileName()
			found = true
			break
		}
		if !found {
			return "", errors.New("upload requires a file part")
		}
	}
	if name == "" {
		name = "upload.zip"
	}
	name = filepath.Base(name)
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	var n int64
	if limit > 0 {
		n, err = io.Copy(f, io.LimitReader(src, limit+1))
	} else {
		n, err = io.Copy(f, src)
	}
	if err == nil && limit > 0 && n > limit {
		err = fmt.Errorf("uploaded file exceeds %d bytes", limit)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	return name, err
}

func (a *App) importPreflight(w http.ResponseWriter, r *http.Request) {
	id := NewID()
	dir := filepath.Join(a.DataDir, "tmp", "imports", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		WriteError(w, r, err)
		return
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	preview, err := receiveImport(r, dir)
	if err != nil {
		WriteError(w, r, transferValidationError(err, false))
		return
	}
	preview.ID = id
	preview.ExpiresAt = Now() + int64(24*time.Hour/time.Millisecond)
	if err = atomicJSON(filepath.Join(dir, "preview.json"), preview); err != nil {
		WriteError(w, r, err)
		return
	}
	ok = true
	WriteData(w, 200, preview, nil)
}

// Multipart Markdown files share one preview, including links between selected files.
func receiveImport(r *http.Request, dir string) (ImportPreview, error) {
	upload := filepath.Join(dir, "upload")
	dest := filepath.Join(dir, "files")
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		name, err := receiveUpload(r, upload, ContentZIPLimit)
		if err != nil {
			return ImportPreview{}, err
		}
		return preflightImportContext(r.Context(), upload, name, dest)
	}
	mr, err := r.MultipartReader()
	if err != nil {
		return ImportPreview{}, err
	}
	if err = os.MkdirAll(dest, 0700); err != nil {
		return ImportPreview{}, err
	}
	entries := map[string]archiveEntry{}
	seen := map[string]bool{}
	var total int64
	zipName := ""
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ImportPreview{}, err
		}
		name := part.FileName()
		if name == "" {
			part.Close()
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if ext != ".md" && ext != ".markdown" && ext != ".zip" {
			return ImportPreview{}, errors.New("only Markdown and ZIP files are supported")
		}
		if zipName != "" || ext == ".zip" && len(entries) > 0 {
			return ImportPreview{}, errors.New("select multiple Markdown files or a single ZIP package")
		}
		if _, err = safeArchivePath(name); err != nil {
			return ImportPreview{}, err
		}
		key := strings.ToLower(norm.NFC.String(name))
		if seen[key] {
			return ImportPreview{}, fmt.Errorf("duplicate normalized file name: %s", name)
		}
		seen[key] = true
		if len(seen) > ContentFileLimit {
			return ImportPreview{}, errors.New("too many import files")
		}
		limit := int64(MarkdownLimit)
		target := filepath.Join(dest, name)
		if ext == ".zip" {
			limit = ContentZIPLimit
			target = upload
			zipName = name
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return ImportPreview{}, err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(part, min(limit, ContentZIPLimit-total)+1))
		closeErr := f.Close()
		if copyErr != nil {
			return ImportPreview{}, copyErr
		}
		if closeErr != nil {
			return ImportPreview{}, closeErr
		}
		total += n
		if n > limit || total > ContentZIPLimit {
			return ImportPreview{}, errors.New("uploaded files exceed import size limit")
		}
		part.Close()
		entries[name] = archiveEntry{name, n, hex.EncodeToString(hash.Sum(nil))}
	}
	if zipName != "" {
		return preflightImportContext(r.Context(), upload, zipName, dest)
	}
	return preflightImportEntries(r.Context(), entries, dest)
}

func preflightImport(upload, filename, dest string) (ImportPreview, error) {
	return preflightImportContext(context.Background(), upload, filename, dest)
}
func preflightImportContext(ctx context.Context, upload, filename, dest string) (ImportPreview, error) {
	p := ImportPreview{Documents: []ContentDocument{}, Attachments: []ContentAttachment{}, Warnings: []string{}}
	entries := map[string]archiveEntry{}
	if strings.EqualFold(filepath.Ext(filename), ".md") || strings.EqualFold(filepath.Ext(filename), ".markdown") {
		if err := os.MkdirAll(dest, 0700); err != nil {
			return p, err
		}
		info, err := os.Stat(upload)
		if err != nil {
			return p, err
		}
		if info.Size() > MarkdownLimit {
			return p, fmt.Errorf("Markdown size exceeds %d bytes", MarkdownLimit)
		}
		name := path.Base(filename)
		if _, err := safeArchivePath(name); err != nil {
			return p, err
		}
		if err := copyFile(upload, filepath.Join(dest, name)); err != nil {
			return p, err
		}
		hash, n, err := hashFile(upload)
		if err != nil {
			return p, err
		}
		entries[name] = archiveEntry{name, n, hash}
	} else if strings.EqualFold(filepath.Ext(filename), ".zip") {
		var err error
		entries, err = extractArchiveContext(ctx, upload, dest, archiveLimits{ContentExpandedLimit, ContentFileLimit, 34, 200})
		if err != nil {
			return p, err
		}
	} else {
		return p, errors.New("only Markdown and ZIP files are supported")
	}
	preview, err := preflightImportEntries(ctx, entries, dest)
	if strings.EqualFold(filepath.Ext(filename), ".zip") {
		preview.SourceKind = "zip"
	}
	return preview, err
}

func preflightImportEntries(ctx context.Context, entries map[string]archiveEntry, dest string) (ImportPreview, error) {
	p := ImportPreview{SourceKind: "markdown", Documents: []ContentDocument{}, Attachments: []ContentAttachment{}, Warnings: []string{}}
	for _, e := range entries {
		p.TotalBytes += e.Size
	}
	if _, exists := entries["manifest.json"]; exists {
		var m ContentManifest
		if err := readJSONFile(filepath.Join(dest, "manifest.json"), &m); err != nil {
			return p, err
		}
		if m.Format != "ljmdb-content" || m.FormatVersion != 1 {
			return p, errors.New("unsupported content manifest; full backups use the restore API")
		}
		p.Documents = m.Documents
		p.Attachments = m.Attachments
		p.Warnings = append(p.Warnings, m.UnresolvedLinks...)
		used := map[string]bool{"manifest.json": true}
		ids := map[string]bool{}
		for i := range p.Documents {
			d := &p.Documents[i]
			if !ValidID(d.SourceID) || ids[d.SourceID] {
				return p, errors.New("invalid or duplicate document source ID")
			}
			ids[d.SourceID] = true
			if !strings.EqualFold(path.Ext(d.Path), ".md") && !strings.EqualFold(path.Ext(d.Path), ".markdown") {
				return p, errors.New("only Markdown content packages can be imported")
			}
			if !strings.HasPrefix(d.Path, "documents/") {
				return p, errors.New("manifest document path must be inside documents/")
			}
			if _, err := safeArchivePath(d.Path); err != nil {
				return p, err
			}
			if used[d.Path] {
				return p, errors.New("duplicate manifest path")
			}
			used[d.Path] = true
			if _, ok := entries[d.Path]; !ok {
				return p, fmt.Errorf("missing document %s", d.Path)
			}
		}
		assetIDs := map[string]bool{}
		for _, a := range p.Attachments {
			if !ValidID(a.SourceID) || assetIDs[a.SourceID] {
				return p, errors.New("invalid or duplicate attachment source ID")
			}
			assetIDs[a.SourceID] = true
			if !strings.HasPrefix(a.Path, "assets/") {
				return p, errors.New("manifest resource path must be inside assets/")
			}
			if _, err := safeArchivePath(a.Path); err != nil {
				return p, err
			}
			if used[a.Path] {
				return p, errors.New("duplicate manifest path")
			}
			used[a.Path] = true
			e, ok := entries[a.Path]
			if !ok || e.SHA256 != a.SHA256 || e.Size != a.SizeBytes {
				return p, fmt.Errorf("resource hash or length mismatch: %s", a.Path)
			}
			if e.Size > AttachmentLimit {
				return p, errors.New("resource exceeds attachment size limit")
			}
		}
		for name := range entries {
			if !used[name] {
				return p, fmt.Errorf("unexpected content package file: %s", name)
			}
		}
	} else {
		names := []string{}
		for n := range entries {
			ext := strings.ToLower(path.Ext(n))
			if ext == ".md" || ext == ".markdown" {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		// A single outer folder is commonly just the folder selected for packing.
		// Keep original paths for link resolution, but omit its empty tree node.
		wrapper := ""
		for n := range entries {
			first, _, nested := strings.Cut(n, "/")
			if !nested || (wrapper != "" && first != wrapper) {
				wrapper = ""
				break
			}
			wrapper = first
		}
		for _, n := range names {
			if path.Dir(n) == wrapper && strings.EqualFold(strings.TrimSuffix(path.Base(n), path.Ext(n)), "index") {
				wrapper = ""
				break
			}
		}
		dirs := map[string]string{}
		docByPath := map[string]int{}
		for _, n := range names {
			parentDir := path.Dir(n)
			parts := strings.Split(parentDir, "/")
			cur := ""
			var parent *string
			if parentDir != "." {
				for _, part := range parts {
					if cur == "" {
						cur = part
					} else {
						cur += "/" + part
					}
					if cur == wrapper {
						continue
					}
					id, ok := dirs[cur]
					if !ok {
						id = NewID()
						dirs[cur] = id
						p.Documents = append(p.Documents, ContentDocument{SourceID: id, Title: part, ParentSourceID: parent, Path: cur + "/index.md"})
						docByPath[cur] = len(p.Documents) - 1
					}
					parent = &id
				}
			}
			base := strings.TrimSuffix(path.Base(n), path.Ext(n))
			if strings.EqualFold(base, "index") && parentDir != "." {
				idx := docByPath[parentDir]
				p.Documents[idx].Path = n
				continue
			}
			p.Documents = append(p.Documents, ContentDocument{SourceID: NewID(), Title: base, ParentSourceID: parent, Path: n})
		}
		mdPaths := map[string]bool{}
		for _, d := range p.Documents {
			mdPaths[d.Path] = true
		}
		resourceNames := []string{}
		for n := range entries {
			if !mdPaths[n] {
				resourceNames = append(resourceNames, n)
			}
		}
		sort.Strings(resourceNames)
		for _, n := range resourceNames {
			e := entries[n]
			if e.Size > AttachmentLimit {
				return p, fmt.Errorf("resource %s exceeds attachment limit", n)
			}
			p.Attachments = append(p.Attachments, ContentAttachment{NewID(), path.Base(n), mime.TypeByExtension(path.Ext(n)), n, e.Size, e.SHA256})
		}
	}
	if len(p.Documents) > MaxTreeNodes {
		return p, fmt.Errorf("document count exceeds %d", MaxTreeNodes)
	}
	if len(p.Documents) == 0 {
		return p, errors.New("package contains no Markdown documents")
	}
	parents := map[string]*string{}
	byPath := map[string]bool{}
	assets := map[string]bool{}
	titles := map[string]int{}
	for _, a := range p.Attachments {
		assets[a.Path] = true
	}
	for i := range p.Documents {
		d := &p.Documents[i]
		title, err := ValidateTitle(d.Title)
		if err != nil {
			return p, err
		}
		d.Title = title
		parents[d.SourceID] = d.ParentSourceID
		byPath[d.Path] = true
		titles[d.Title]++
		if _, ok := entries[d.Path]; ok {
			if entries[d.Path].Size > MarkdownLimit {
				return p, errors.New("document exceeds Markdown limit")
			}
			b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(d.Path)))
			if err != nil {
				return p, err
			}
			if !utf8.Valid(b) {
				return p, fmt.Errorf("document is not UTF-8: %s", d.Path)
			}
			if d.SHA256 != "" && d.SHA256 != entries[d.Path].SHA256 {
				return p, fmt.Errorf("document checksum mismatch: %s", d.Path)
			}
			d.SHA256 = entries[d.Path].SHA256
			_ = b
		}
	}
	for id := range parents {
		seen := map[string]bool{}
		cur := id
		depth := 0
		for {
			if seen[cur] {
				return p, errors.New("manifest document tree has a cycle")
			}
			seen[cur] = true
			depth++
			if depth > 32 {
				return p, errors.New("document tree exceeds 32 levels")
			}
			par, ok := parents[cur]
			if !ok {
				return p, errors.New("manifest references missing parent")
			}
			if par == nil {
				break
			}
			cur = *par
		}
	}
	for i := range p.Documents {
		d := &p.Documents[i]
		markdown := ""
		if _, exists := entries[d.Path]; exists {
			b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(d.Path)))
			if err != nil {
				return p, err
			}
			markdown = string(b)
		} else {
			d.Synthetic = true
		}
		for _, link := range ExtractLinks(markdown) {
			kind, target, _, err := packageLink(d.Path, link)
			if err != nil {
				return p, err
			}
			switch kind {
			case "external":
				p.Warnings = append(p.Warnings, "external dependency: "+link)
			case "local":
				if !byPath[target] && !assets[target] {
					return p, fmt.Errorf("missing local resource or document %s referenced by %s", target, d.Path)
				}
			case "stable_document":
				if _, ok := parents[target]; !ok {
					p.Warnings = append(p.Warnings, "unresolved document: "+link)
				}
			case "stable_attachment":
				found := false
				for _, a := range p.Attachments {
					if a.SourceID == target {
						found = true
						break
					}
				}
				if !found {
					return p, fmt.Errorf("missing attachment %s", target)
				}
			}
		}
	}
	for t, n := range titles {
		if n > 1 {
			p.Warnings = append(p.Warnings, fmt.Sprintf("duplicate title allowed: %s (%d)", t, n))
		}
	}
	return p, nil
}

// packageLink returns an URL suffix separately so fragments survive link mapping.
func packageLink(documentPath, link string) (kind, target, suffix string, err error) {
	u, err := url.Parse(link)
	if err != nil {
		return "", "", "", err
	}
	if u.Scheme != "" || u.Host != "" {
		return "external", link, "", nil
	}
	if u.RawQuery != "" {
		suffix = "?" + u.RawQuery
	}
	if u.Fragment != "" {
		suffix += "#" + u.EscapedFragment()
	}
	if u.Path == "" {
		return "anchor", "", suffix, nil
	}
	if strings.HasPrefix(u.Path, "/documents/") {
		return "stable_document", strings.TrimPrefix(u.Path, "/documents/"), suffix, nil
	}
	if strings.HasPrefix(u.Path, "/api/v1/attachments/") && strings.HasSuffix(u.Path, "/content") {
		return "stable_attachment", strings.TrimSuffix(strings.TrimPrefix(u.Path, "/api/v1/attachments/"), "/content"), suffix, nil
	}
	if strings.HasPrefix(u.Path, "/") || strings.ContainsAny(u.Path, "\\\x00:") {
		return "", "", "", fmt.Errorf("invalid local link: %s", link)
	}
	target = path.Clean(path.Join(path.Dir(documentPath), u.Path))
	if target == ".." || strings.HasPrefix(target, "../") {
		return "", "", "", fmt.Errorf("local link leaves package: %s", link)
	}
	return "local", target, suffix, nil
}

type importRequest struct {
	PreviewID             string         `json:"preview_id"`
	TargetKnowledgeBaseID string         `json:"target_knowledge_base_id"`
	TargetParentID        *string        `json:"target_parent_id"`
	ExpectedTreeRevision  int            `json:"expected_tree_revision"`
	Targets               []importTarget `json:"targets,omitempty"`
}

func (a *App) importCommit(w http.ResponseWriter, r *http.Request) {
	var req importRequest
	if err := Decode(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	if !ValidID(req.PreviewID) {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "无效 preview_id", nil))
		return
	}
	var p ImportPreview
	if err := readJSONFile(filepath.Join(a.DataDir, "tmp", "imports", req.PreviewID, "preview.json"), &p); err != nil || p.ExpiresAt < Now() {
		WriteError(w, r, Err(410, "IMPORT_PREVIEW_EXPIRED", "导入预览不存在或已过期，请重新预检", nil))
		return
	}
	_, err := resolveImportTargets(r.Context(), a.DB, req, p)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	id, err := a.createJob(r.Context(), "import")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	a.startJob(id, false, func(ctx context.Context) (string, any, error) {
		result, err := a.applyImport(ctx, id, req, p)
		return "", result, err
	})
	WriteData(w, 202, map[string]any{"job_id": id, "state": "queued"}, nil)
}
func (a *App) applyImport(ctx context.Context, jobID string, req importRequest, p ImportPreview) (any, error) {
	if p.ExpiresAt < Now() {
		return nil, Err(410, "IMPORT_PREVIEW_EXPIRED", "导入预览已过期，请重新预检", nil)
	}
	plan, err := resolveImportTargets(ctx, a.DB, req, p)
	if err != nil {
		return nil, err
	}
	docIDs := map[string]string{}
	pathDocs := map[string]string{}
	assetIDs := map[string]string{}
	pathAssets := map[string]string{}
	for _, d := range p.Documents {
		docIDs[d.SourceID] = NewID()
		pathDocs[d.Path] = docIDs[d.SourceID]
	}
	prepared := []Attachment{}
	defer func() {
		if err := a.RegisterUnreferencedAttachments(context.Background(), prepared); err != nil && a.Logger != nil {
			a.Logger.Printf("import prepared attachment reconciliation failed job=%s error=%s", jobID, safeErrorClass(err))
		}
	}()
	for _, asset := range p.Attachments {
		f, err := os.Open(filepath.Join(a.DataDir, "tmp", "imports", p.ID, "files", filepath.FromSlash(asset.Path)))
		if err != nil {
			return nil, err
		}
		attachment, err := a.PrepareAttachment(contextReader{ctx, f}, asset.OriginalName, AttachmentLimit)
		f.Close()
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, attachment)
		if attachment.SHA256 != asset.SHA256 || attachment.SizeBytes != asset.SizeBytes {
			return nil, errors.New("staged resource changed after preflight")
		}
		assetIDs[asset.SourceID] = attachment.ID
		pathAssets[asset.Path] = attachment.ID
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Revalidate every destination in the transaction that creates the entire batch.
	plan, err = resolveImportTargets(ctx, tx, req, p)
	if err != nil {
		return nil, err
	}
	for _, att := range prepared {
		if err = InsertAttachment(ctx, tx, att); err != nil {
			return nil, err
		}
	}
	pending := append([]ContentDocument(nil), p.Documents...)
	sort.SliceStable(pending, func(i, j int) bool {
		a, b := pending[i], pending[j]
		ap, bp := "", ""
		if a.ParentSourceID != nil {
			ap = *a.ParentSourceID
		}
		if b.ParentSourceID != nil {
			bp = *b.ParentSourceID
		}
		if ap != bp {
			return ap < bp
		}
		return a.SortOrder < b.SortOrder
	})
	created := map[string]bool{}
	now := Now()
	report := []map[string]any{}
	for len(pending) > 0 {
		next := []ContentDocument{}
		for _, d := range pending {
			if d.ParentSourceID != nil && !created[*d.ParentSourceID] {
				next = append(next, d)
				continue
			}
			target := plan.Documents[d.SourceID]
			parent := target.TargetParentID
			if d.ParentSourceID != nil {
				x := docIDs[*d.ParentSourceID]
				parent = &x
			}
			depth, err := ParentDepth(ctx, tx, target.TargetKnowledgeBaseID, parent)
			if err != nil {
				return nil, err
			}
			if depth >= 32 {
				return nil, Err(400, "TREE_DEPTH_EXCEEDED", "导入后树深度超过 32 层", nil)
			}
			sourceMarkdown := d.Markdown
			if !d.Synthetic {
				sourcePath := filepath.Join(a.DataDir, "tmp", "imports", p.ID, "files", filepath.FromSlash(d.Path))
				hash, n, err := hashFile(sourcePath)
				if err != nil || hash != d.SHA256 || n > MarkdownLimit {
					return nil, errors.New("staged document changed after preflight")
				}
				b, err := os.ReadFile(sourcePath)
				if err != nil {
					return nil, err
				}
				sourceMarkdown = string(b)
			}
			markdown, err := RewriteLinks(sourceMarkdown, func(link string) (string, error) {
				kind, target, suffix, err := packageLink(d.Path, link)
				if err != nil {
					return "", err
				}
				switch kind {
				case "stable_document":
					if id := docIDs[target]; id != "" {
						return "/documents/" + id + suffix, nil
					}
				case "stable_attachment":
					if id := assetIDs[target]; id != "" {
						return "/api/v1/attachments/" + id + "/content" + suffix, nil
					}
				case "local":
					if id := pathDocs[target]; id != "" {
						return "/documents/" + id + suffix, nil
					}
					if id := pathAssets[target]; id != "" {
						return "/api/v1/attachments/" + id + "/content" + suffix, nil
					}
					return "", fmt.Errorf("missing staged link %s", target)
				}
				return link, nil
			})
			if err != nil {
				return nil, err
			}
			rendered := RenderMarkdown(markdown)
			var order int
			if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sort_order),-1)+1 FROM documents WHERE knowledge_base_id=? AND parent_id IS ? AND deleted_at IS NULL", target.TargetKnowledgeBaseID, parent).Scan(&order); err != nil {
				return nil, err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO documents(id,knowledge_base_id,parent_id,title,markdown,html,search_text,render_version,sort_order,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,1,?,?)", docIDs[d.SourceID], target.TargetKnowledgeBaseID, parent, d.Title, markdown, rendered.HTML, rendered.SearchText, rendered.Version, order, now, now)
			if err != nil {
				return nil, err
			}
			if err = SyncDocumentAttachments(ctx, tx, docIDs[d.SourceID], rendered.AttachmentIDs); err != nil {
				return nil, err
			}
			created[d.SourceID] = true
			report = append(report, map[string]any{"source_id": d.SourceID, "id": docIDs[d.SourceID], "title": d.Title, "path": d.Path, "knowledge_base_id": target.TargetKnowledgeBaseID, "parent_id": parent})
		}
		if len(next) == len(pending) {
			return nil, errors.New("unresolvable import tree")
		}
		pending = next
	}
	revisions := map[string]int{}
	for id, library := range plan.Libraries {
		if err = ValidateTree(ctx, tx, id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE knowledge_bases SET tree_revision=tree_revision+1,updated_at=? WHERE id=?", now, id); err != nil {
			return nil, err
		}
		revisions[id] = library.TreeRevision + 1
	}
	result := map[string]any{"documents": report, "attachment_count": len(prepared), "tree_revisions": revisions, "warnings": p.Warnings}
	if len(revisions) == 1 {
		for _, revision := range revisions {
			result["tree_revision"] = revision
		}
	}
	if err = atomicJSON(filepath.Join(a.DataDir, "tmp", "jobs", jobID+".json"), result); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE operation_jobs SET state='succeeded',progress=100,updated_at=? WHERE id=?", Now(), jobID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
func (a *App) createJob(ctx context.Context, kind string) (string, error) {
	id := NewID()
	now := Now()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO operation_jobs(id,kind,state,created_at,updated_at) VALUES(?,?,'queued',?,?)", id, kind, now, now); err != nil {
		return "", err
	}
	if err = SaveIdempotency(ctx, tx, 202, map[string]any{"job_id": id, "state": "queued"}, nil); err != nil {
		return "", err
	}
	if input, ok := ctx.Value(idempotencyContextKey{}).(idempotencyInput); ok {
		if _, err = tx.ExecContext(ctx, "UPDATE idempotency_records SET job_id=? WHERE scope=? AND idempotency_key=?", id, input.Scope, input.Key); err != nil {
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	a.liveJobs.Store(id, operationJob{ID: id, Kind: kind, State: "queued", CreatedAt: now, UpdatedAt: now})
	return id, nil
}
func jobFailure(err error) (string, string) {
	var api *APIError
	if errors.As(err, &api) {
		return api.Code, api.Message
	}
	if errors.Is(err, syscall.ENOSPC) || strings.Contains(err.Error(), "SQLITE_FULL") {
		return "INSUFFICIENT_STORAGE", "磁盘空间不足，任务已停止"
	}
	if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
		return "DATABASE_BUSY", "数据库繁忙，请稍后重新创建任务"
	}
	if errors.Is(err, context.Canceled) {
		return "JOB_CANCELLED", "任务因应用退出而取消"
	}
	return "JOB_FAILED", "任务未能完成，请查看服务日志后重试"
}
func (a *App) RecoverTransferJobs() error {
	_, err := a.DB.Exec("UPDATE operation_jobs SET state='cancelled',error_code='JOB_INTERRUPTED',error_summary='应用退出时任务尚未完成，请重新创建任务',updated_at=? WHERE state IN ('queued','running')", Now())
	return err
}
func (a *App) startJob(id string, maintenance bool, run func(context.Context) (string, any, error)) {
	epoch := a.identity.Load().(identity).DataEpoch
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if maintenance {
			defer a.maintenance.Store(false)
			a.Mu.Lock()
			defer a.Mu.Unlock()
		} else {
			a.Mu.RLock()
			defer a.Mu.RUnlock()
			a.WriteMu.Lock()
			defer a.WriteMu.Unlock()
		}
		j, _ := a.liveJobs.Load(id)
		job := j.(operationJob)
		ctx := a.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		var preErr error
		if ctx.Err() != nil {
			preErr = ctx.Err()
		} else if a.identity.Load().(identity).DataEpoch != epoch {
			preErr = Err(409, "DATA_EPOCH_CHANGED", "任务的数据世代已改变，请重新创建任务", nil)
		}
		if preErr != nil {
			code, message := jobFailure(preErr)
			job.State = "cancelled"
			job.ErrorCode = &code
			job.ErrorSummary = &message
			job.UpdatedAt = Now()
			a.liveJobs.Store(id, job)
			a.DB.Exec("UPDATE operation_jobs SET state='cancelled',error_code=?,error_summary=?,updated_at=? WHERE id=?", code, message, Now(), id)
			return
		}
		job.State = "running"
		job.Progress = 5
		job.UpdatedAt = Now()
		a.liveJobs.Store(id, job)
		a.DB.Exec("UPDATE operation_jobs SET state='running',progress=5,updated_at=? WHERE id=?", Now(), id)
		resultPath, result, err := run(ctx)
		if latest, ok := a.liveJobs.Load(id); ok {
			job = latest.(operationJob)
		}
		if err == nil && result != nil && job.Kind != "import" {
			err = atomicJSON(filepath.Join(a.DataDir, "tmp", "jobs", id+".json"), result)
		}
		var output any
		if resultPath != "" {
			output = resultPath
		}
		if err == nil && job.Kind != "import" {
			var updated sql.Result
			updated, err = a.DB.Exec("UPDATE operation_jobs SET state='succeeded',progress=100,result_path=?,updated_at=? WHERE id=?", output, Now(), id)
			if err == nil {
				var n int64
				n, err = updated.RowsAffected()
				if err == nil && n != 1 {
					err = errors.New("job row disappeared before final commit")
				}
			}
		}
		if err != nil {
			os.Remove(filepath.Join(a.DataDir, "tmp", "jobs", id+".json"))
			code, message := jobFailure(err)
			job.State = "failed"
			job.ErrorCode = &code
			job.ErrorSummary = &message
			job.UpdatedAt = Now()
			if a.Logger != nil {
				a.Logger.Printf("job failed id=%s code=%s error=%s", id, code, safeErrorClass(err))
			}
			a.DB.Exec("UPDATE operation_jobs SET state='failed',error_code=?,error_summary=?,updated_at=? WHERE id=?", code, message, Now(), id)
			a.liveJobs.Store(id, job)
			return
		}
		if resultPath != "" {
			job.ResultPath = &resultPath
			job.DownloadURL = "/api/v1/jobs/" + id + "/download"
		}
		job.State = "succeeded"
		job.Progress = 100
		job.Result = result
		job.UpdatedAt = Now()
		a.liveJobs.Store(id, job)

	}()
}
func (a *App) jobProgress(id string, progress int) {
	cached, ok := a.liveJobs.Load(id)
	if !ok {
		return
	}
	job := cached.(operationJob)
	if job.State != "running" || progress <= job.Progress {
		return
	}
	job.Progress = progress
	job.UpdatedAt = Now()
	a.liveJobs.Store(id, job)
	a.DB.Exec("UPDATE operation_jobs SET progress=?,updated_at=? WHERE id=? AND state='running'", progress, job.UpdatedAt, id)
}
func (a *App) jobEstimate(id string, estimate any) {
	cached, ok := a.liveJobs.Load(id)
	if !ok {
		return
	}
	job := cached.(operationJob)
	job.Result = estimate
	a.liveJobs.Store(id, job)
	atomicJSON(filepath.Join(a.DataDir, "tmp", "jobs", id+".json"), estimate)
}
func (a *App) jobGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if cached, ok := a.liveJobs.Load(id); ok && cached.(operationJob).Kind == "restore" {
		WriteData(w, 200, cached, nil)
		return
	}
	if state, err := readRestoreJob(a.DataDir, id); err == nil && state.JobID == id {
		WriteData(w, 200, state.job(), nil)
		return
	}
	if job, ok := a.liveJobs.Load(id); ok {
		WriteData(w, 200, job, nil)
		return
	}
	if a.maintenance.Load() {
		WriteError(w, r, Err(503, "MAINTENANCE_MODE", "数据维护中，请稍后查询", nil))
		return
	}
	a.Mu.RLock()
	defer a.Mu.RUnlock()
	job, err := a.loadJob(id)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteData(w, 200, job, nil)
}
func (a *App) loadJob(id string) (operationJob, error) {
	var j operationJob
	err := a.DB.QueryRow("SELECT id,kind,state,progress,result_path,error_code,error_summary,created_at,updated_at FROM operation_jobs WHERE id=?", id).Scan(&j.ID, &j.Kind, &j.State, &j.Progress, &j.ResultPath, &j.ErrorCode, &j.ErrorSummary, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, Err(404, "NOT_FOUND", "任务不存在", nil)
	}
	if err != nil {
		return j, err
	}
	if j.State == "succeeded" && j.ResultPath != nil {
		j.DownloadURL = "/api/v1/jobs/" + j.ID + "/download"
	}
	var result any
	if readJSONFile(filepath.Join(a.DataDir, "tmp", "jobs", id+".json"), &result) == nil {
		j.Result = result
	}
	return j, nil
}
func (a *App) jobDownload(w http.ResponseWriter, r *http.Request) {
	j, err := a.loadJob(r.PathValue("id"))
	if err != nil {
		WriteError(w, r, err)
		return
	}
	if j.State != "succeeded" || j.ResultPath == nil || j.Kind == "import" || j.Kind == "restore" {
		WriteError(w, r, Err(409, "JOB_NOT_READY", "任务没有可下载结果", nil))
		return
	}
	allowed := false
	if ValidID(j.ID) {
		if j.Kind == "backup" {
			allowed = *j.ResultPath == "backups/"+j.ID+".zip"
		} else if j.Kind == "export" {
			allowed = *j.ResultPath == "tmp/exports/"+j.ID+".zip" || *j.ResultPath == "tmp/exports/"+j.ID+".md"
		}
	}
	if !allowed {
		WriteError(w, r, Err(500, "INVALID_JOB_RESULT", "任务结果路径与任务身份不匹配", nil))
		return
	}
	p := filepath.Join(a.DataDir, *j.ResultPath)
	rel, err := filepath.Rel(a.DataDir, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		WriteError(w, r, Err(500, "INVALID_JOB_RESULT", "无效任务路径", nil))
		return
	}
	current := a.DataDir
	for _, part := range strings.Split(filepath.ToSlash(*j.ResultPath), "/") {
		current = filepath.Join(current, part)
		info, pathErr := os.Lstat(current)
		if pathErr != nil {
			WriteError(w, r, Err(404, "JOB_RESULT_MISSING", "任务结果文件不存在", nil))
			return
		}
		if info.Mode()&os.ModeSymlink != 0 {
			WriteError(w, r, Err(500, "INVALID_JOB_RESULT", "任务结果不允许符号链接", nil))
			return
		}
	}
	if _, err = os.Stat(p); err != nil {
		WriteError(w, r, Err(404, "JOB_RESULT_MISSING", "任务结果文件不存在", nil))
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	http.ServeFile(w, r, p)
}

type exportRequest struct {
	Scope              string `json:"scope"`
	ID                 string `json:"id"`
	Format             string `json:"format"`
	IncludeAttachments *bool  `json:"include_attachments"`
}

func (a *App) exportCreate(w http.ResponseWriter, r *http.Request) {
	var req exportRequest
	if err := Decode(r, &req); err != nil {
		WriteError(w, r, err)
		return
	}
	if req.Scope != "document" && req.Scope != "knowledge_base" {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "scope 必须为 document 或 knowledge_base", nil))
		return
	}
	switch req.Format {
	case "md", "markdown", "markdown_zip", "html", "html_zip":
	default:
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "format 必须为 md、markdown_zip 或 html_zip", nil))
		return
	}
	if req.Format == "md" && req.Scope != "document" {
		WriteError(w, r, Err(400, "INVALID_ARGUMENT", "纯 Markdown 仅支持单篇文档", nil))
		return
	}
	var err error
	if req.Scope == "document" {
		_, err = LoadDocument(r.Context(), a.DB, req.ID, true)
	} else {
		_, err = LoadKnowledgeBase(r.Context(), a.DB, req.ID, true)
	}
	if err != nil {
		WriteError(w, r, err)
		return
	}
	id, err := a.createJob(r.Context(), "export")
	if err != nil {
		WriteError(w, r, err)
		return
	}
	a.startJob(id, false, func(ctx context.Context) (string, any, error) { return a.buildExport(ctx, id, req) })
	WriteData(w, 202, map[string]any{"job_id": id, "state": "queued"}, nil)
}
func safeExportName(title, id string) string {
	runes := []rune{}
	for _, r := range title {
		if unicode.IsControl(r) || strings.ContainsRune("<>:\"/\\|?*", r) {
			r = '_'
		}
		runes = append(runes, r)
		if len(runes) >= 50 {
			break
		}
	}
	s := strings.Trim(string(runes), " .")
	if s == "" {
		s = "document"
	}
	upper := strings.ToUpper(strings.Split(s, ".")[0])
	if upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" || strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT") {
		s = "_" + s
	}
	return s + "-" + id
}
func safeAssetExtension(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if len(ext) > 16 {
		return ""
	}
	for _, r := range ext {
		if r != '.' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return ""
		}
	}
	return ext
}
func relativeURL(from, to string) string {
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(from)), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	for i, p := range parts {
		if p != ".." {
			parts[i] = url.PathEscape(p)
		}
	}
	return strings.Join(parts, "/")
}
func (a *App) buildExport(ctx context.Context, id string, req exportRequest) (string, any, error) {
	a.FileMu.RLock()
	defer a.FileMu.RUnlock()
	tx, err := a.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	docs := []Document{}
	var k KnowledgeBase
	if req.Scope == "document" {
		d, err := LoadDocument(ctx, tx, req.ID, true)
		if err != nil {
			return "", nil, err
		}
		docs = append(docs, d)
		k, err = LoadKnowledgeBase(ctx, tx, d.KnowledgeBaseID, true)
		if err != nil {
			return "", nil, err
		}
	} else {
		k, err = LoadKnowledgeBase(ctx, tx, req.ID, true)
		if err != nil {
			return "", nil, err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+documentColumns+" FROM documents d WHERE knowledge_base_id=? AND deleted_at IS NULL ORDER BY sort_order,id", req.ID)
		if err != nil {
			return "", nil, err
		}
		for rows.Next() {
			d, err := scanDocument(rows)
			if err != nil {
				rows.Close()
				return "", nil, err
			}
			docs = append(docs, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", nil, err
		}
	}
	m := ContentManifest{Format: "ljmdb-content", FormatVersion: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339), Documents: []ContentDocument{}, Attachments: []ContentAttachment{}, UnresolvedLinks: []string{}}
	m.KnowledgeBase.Name = k.Name
	m.KnowledgeBase.Description = k.Description
	paths := map[string]string{}
	children := map[string]bool{}
	selected := map[string]Document{}
	for _, d := range docs {
		selected[d.ID] = d
		if d.ParentID != nil {
			children[*d.ParentID] = true
		}
	}
	htmlExport := req.Format == "html" || req.Format == "html_zip"
	ext := ".md"
	if htmlExport {
		ext = ".html"
	}
	var mapPath func(string) (string, error)
	visiting := map[string]bool{}
	mapPath = func(id string) (string, error) {
		if p := paths[id]; p != "" {
			return p, nil
		}
		if visiting[id] {
			return "", errors.New("document tree has cycle")
		}
		visiting[id] = true
		d := selected[id]
		base := "documents"
		if d.ParentID != nil {
			if _, ok := selected[*d.ParentID]; ok {
				p, err := mapPath(*d.ParentID)
				if err != nil {
					return "", err
				}
				base = path.Dir(p)
			}
		}
		p := base + "/" + safeExportName(d.Title, d.ID) + ext
		if children[d.ID] {
			p = base + "/" + safeExportName(d.Title, d.ID) + "/index" + ext
		}
		if len(p) > 220 {
			p = "documents/" + d.ID + ext
		}
		paths[id] = p
		delete(visiting, id)
		return p, nil
	}
	for _, d := range docs {
		if _, err = mapPath(d.ID); err != nil {
			return "", nil, err
		}
	}
	assets := map[string]Attachment{}
	include := req.Format != "md" && (req.IncludeAttachments == nil || *req.IncludeAttachments)
	for _, d := range docs {
		rendered := RenderMarkdown(d.Markdown)
		for _, attID := range rendered.AttachmentIDs {
			if _, ok := assets[attID]; ok {
				continue
			}
			if !include {
				assets[attID] = Attachment{ID: attID, OriginalName: "attachment"}
				continue
			}
			var at Attachment
			err = tx.QueryRowContext(ctx, "SELECT id,original_name,storage_path,media_type,size_bytes,sha256,state,created_at,updated_at FROM attachments WHERE id=? AND state='ready'", attID).Scan(&at.ID, &at.OriginalName, &at.StoragePath, &at.MediaType, &at.SizeBytes, &at.SHA256, &at.State, &at.CreatedAt, &at.UpdatedAt)
			if err != nil {
				return "", nil, fmt.Errorf("referenced attachment unavailable: %s", attID)
			}
			full, pathErr := a.SafeAttachmentPath(at.ID, at.StoragePath)
			if pathErr != nil {
				return "", nil, pathErr
			}
			hash, n, err := hashFile(full)
			if err != nil || hash != at.SHA256 || n != at.SizeBytes {
				return "", nil, fmt.Errorf("referenced attachment damaged: %s", attID)
			}
			assets[attID] = at
		}
	}
	if err = tx.Commit(); err != nil {
		return "", nil, err
	}
	a.jobProgress(id, 25)
	targetDir := filepath.Join(a.DataDir, "tmp", "exports")
	if err = os.MkdirAll(targetDir, 0700); err != nil {
		return "", nil, err
	}
	suffix := ".zip"
	if req.Format == "md" {
		suffix = ".md"
	}
	relative := filepath.ToSlash(filepath.Join("tmp", "exports", id+suffix))
	output := filepath.Join(a.DataDir, filepath.FromSlash(relative))
	f, err := os.OpenFile(output+".partial", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", nil, err
	}
	completed := false
	defer func() {
		f.Close()
		if !completed {
			os.Remove(output + ".partial")
		}
	}()
	if req.Format == "md" {
		d := docs[0]
		_, err = f.WriteString("# " + d.Title + "\n\n" + d.Markdown)
		for _, at := range assets {
			m.UnresolvedLinks = append(m.UnresolvedLinks, "attachment omitted: "+at.ID)
		}
	} else {
		z := zip.NewWriter(f)
		assetPaths := map[string]string{}
		ids := []string{}
		for aid := range assets {
			ids = append(ids, aid)
		}
		sort.Strings(ids)
		var assetTotal, assetDone int64
		for _, at := range assets {
			assetTotal += at.SizeBytes
		}
		for _, aid := range ids {
			at := assets[aid]
			p := "assets/" + at.ID + safeAssetExtension(at.OriginalName)
			if path.Ext(p) == "." {
				p = strings.TrimSuffix(p, ".")
			}
			assetPaths[aid] = p
			if include {
				full, pathErr := a.SafeAttachmentPath(at.ID, at.StoragePath)
				if pathErr != nil {
					z.Close()
					return "", nil, pathErr
				}
				if err = addZIPFileProgress(ctx, z, p, full, func(n int64) error { a.jobProgress(id, 25+int((assetDone+n)*40/max(int64(1), assetTotal))); return nil }); err != nil {
					z.Close()
					return "", nil, err
				}
				assetDone += at.SizeBytes
				m.Attachments = append(m.Attachments, ContentAttachment{at.ID, at.OriginalName, at.MediaType, p, at.SizeBytes, at.SHA256})
			} else {
				m.UnresolvedLinks = append(m.UnresolvedLinks, "attachment omitted: "+aid)
			}
		}
		for docIndex, d := range docs {
			if err := ctx.Err(); err != nil {
				z.Close()
				return "", nil, err
			}
			a.jobProgress(id, 65+(docIndex*25)/max(1, len(docs)))
			p := paths[d.ID]
			markdown, err := RewriteLinks(d.Markdown, func(link string) (string, error) {
				u, err := url.Parse(link)
				if err != nil {
					return "", err
				}
				suffix := ""
				if u.RawQuery != "" {
					suffix = "?" + u.RawQuery
				}
				if u.Fragment != "" {
					suffix += "#" + u.EscapedFragment()
				}
				if strings.HasPrefix(u.Path, "/documents/") {
					target := strings.TrimPrefix(u.Path, "/documents/")
					if mapped := paths[target]; mapped != "" {
						return relativeURL(p, mapped) + suffix, nil
					}
					m.UnresolvedLinks = append(m.UnresolvedLinks, link)
				} else if strings.HasPrefix(u.Path, "/api/v1/attachments/") && strings.HasSuffix(u.Path, "/content") {
					aid := strings.TrimSuffix(strings.TrimPrefix(u.Path, "/api/v1/attachments/"), "/content")
					if include && assetPaths[aid] != "" {
						return relativeURL(p, assetPaths[aid]) + suffix, nil
					}
					m.UnresolvedLinks = append(m.UnresolvedLinks, link)
				} else if u.Scheme != "" || u.Host != "" {
					m.UnresolvedLinks = append(m.UnresolvedLinks, link)
				}
				return link, nil
			})
			if err != nil {
				z.Close()
				return "", nil, err
			}
			body := []byte(markdown)
			if htmlExport {
				rendered := RenderMarkdown(markdown)
				body = []byte(exportHTML(d.Title, rendered.HTML, relativeURL(p, "assets/runtime/")))
			}
			if err = addZIPBytes(z, p, body); err != nil {
				z.Close()
				return "", nil, err
			}
			parent := d.ParentID
			if parent != nil {
				if _, ok := selected[*parent]; !ok {
					parent = nil
				}
			}
			hash := sha256.Sum256(body)
			m.Documents = append(m.Documents, ContentDocument{SourceID: d.ID, Title: d.Title, ParentSourceID: parent, SortOrder: d.SortOrder, Path: p, SHA256: hex.EncodeToString(hash[:])})
		}
		if htmlExport {
			err = fs.WalkDir(offlineAssets, "exportassets", func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				b, err := offlineAssets.ReadFile(p)
				if err != nil {
					return err
				}
				return addZIPBytes(z, "assets/runtime/"+strings.TrimPrefix(p, "exportassets/"), b)
			})
			if err != nil {
				z.Close()
				return "", nil, err
			}
			var b strings.Builder
			b.WriteString("<!doctype html><meta charset=\"utf-8\"><title>" + html.EscapeString(k.Name) + "</title><h1>" + html.EscapeString(k.Name) + "</h1><ul>")
			for _, d := range docs {
				b.WriteString("<li><a href=\"" + html.EscapeString(relativeURL("index.html", paths[d.ID])) + "\">" + html.EscapeString(d.Title) + "</a></li>")
			}
			b.WriteString("</ul>")
			if err = addZIPBytes(z, "index.html", []byte(b.String())); err != nil {
				z.Close()
				return "", nil, err
			}
		}
		manifest, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			z.Close()
			return "", nil, err
		}
		if err = addZIPBytes(z, "manifest.json", manifest); err != nil {
			z.Close()
			return "", nil, err
		}
		err = z.Close()
	}
	if err != nil {
		return "", nil, err
	}
	if err = f.Sync(); err != nil {
		return "", nil, err
	}
	if err = f.Close(); err != nil {
		return "", nil, err
	}
	if err = os.Rename(output+".partial", output); err != nil {
		return "", nil, err
	}
	completed = true
	return relative, map[string]any{"document_count": len(docs), "attachment_count": len(m.Attachments), "unresolved_links": m.UnresolvedLinks}, nil
}
func exportHTML(title, body, resourceBase string) string {
	resourceBase = strings.TrimSuffix(resourceBase, "/") + "/"
	return "<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; script-src 'self' file:; style-src 'self' file: 'unsafe-inline'; img-src 'self' file: data:; font-src 'self' file: data:; base-uri 'none'; object-src 'none'\"><title>" + html.EscapeString(title) + "</title><link rel=\"stylesheet\" href=\"" + resourceBase + "katex.min.css\"><link rel=\"stylesheet\" href=\"" + resourceBase + "export.css\"><script defer src=\"" + resourceBase + "katex.min.js\"></script><script defer src=\"" + resourceBase + "mermaid.min.js\"></script><script defer src=\"" + resourceBase + "export.js\"></script></head><body><header><h1>" + html.EscapeString(title) + "</h1></header><main>" + body + "</main></body></html>"
}

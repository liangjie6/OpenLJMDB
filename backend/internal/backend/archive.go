package backend

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/unicode/norm"
)

const (
	ContentZIPLimit      int64 = 500 << 20
	ContentExpandedLimit int64 = 1 << 30
	ContentFileLimit           = 10000
	MarkdownLimit        int64 = 10 << 20
	AttachmentLimit      int64 = 50 << 20
)

type archiveLimits struct {
	Bytes int64
	Files int
	Depth int
	Ratio uint64
}
type archiveEntry struct {
	Path   string
	Size   int64
	SHA256 string
}

func safeArchivePath(name string) (string, error) {
	if name == "" || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00:") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	n := strings.TrimSuffix(name, "/")
	for _, p := range strings.Split(n, "/") {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("unsafe archive path %q", name)
		}
	}
	if path.Clean(n) != n || len(n) > 4096 {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return n, nil
}

// ZIP entries without the UTF-8 flag may use the creator's local encoding.
// Preserve valid UTF-8 names; otherwise decode GBK before checking path safety.
func archiveFilePath(f *zip.File) (string, error) {
	name := f.Name
	if !utf8.ValidString(name) {
		if f.Flags&0x800 != 0 {
			return "", fmt.Errorf("invalid archive filename encoding %q: ZIP declares UTF-8", name)
		}
		decoded, err := simplifiedchinese.GBK.NewDecoder().String(name)
		// The decoder replaces malformed sequences instead of returning an error.
		if err != nil || strings.ContainsRune(decoded, utf8.RuneError) {
			return "", fmt.Errorf("invalid archive filename encoding %q: expected UTF-8 or GBK", name)
		}
		name = decoded
	}
	return safeArchivePath(name)
}

// extractArchive counts bytes actually decompressed, never trusting ZIP headers.
// The destination is an isolated temporary directory, never the live uploads tree.
func extractArchive(filename, dest string, limits archiveLimits) (map[string]archiveEntry, error) {
	return extractArchiveContext(context.Background(), filename, dest, limits)
}
func extractArchiveContext(ctx context.Context, filename, dest string, limits archiveLimits) (map[string]archiveEntry, error) {
	z, err := zip.OpenReader(filename)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	if limits.Files > 0 && len(z.File) > limits.Files {
		return nil, fmt.Errorf("archive entry count %d exceeds %d", len(z.File), limits.Files)
	}
	var declared int64
	headerPaths := map[string]bool{}
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := archiveFilePath(f)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(norm.NFC.String(p))
		if headerPaths[key] {
			return nil, fmt.Errorf("duplicate normalized archive path: %s", p)
		}
		headerPaths[key] = true
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return nil, fmt.Errorf("non-regular archive entry: %s", p)
		}
		if f.UncompressedSize64 > uint64(1<<60-declared) {
			return nil, errors.New("archive size overflows")
		}
		declared += int64(f.UncompressedSize64)
	}
	if limits.Bytes > 0 && declared > limits.Bytes {
		return nil, fmt.Errorf("archive expanded size exceeds %d bytes", limits.Bytes)
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		return nil, err
	}
	if err := ensureDiskSpace(dest, declared); err != nil {
		return nil, err
	}
	entries := map[string]archiveEntry{}
	seen := map[string]bool{}
	var total int64
	for _, f := range z.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p, err := archiveFilePath(f)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(norm.NFC.String(p))
		if seen[key] {
			return nil, fmt.Errorf("duplicate normalized archive path: %s", p)
		}
		seen[key] = true
		if limits.Depth > 0 && len(strings.Split(p, "/")) > limits.Depth {
			return nil, fmt.Errorf("archive depth exceeds %d: %s", limits.Depth, p)
		}
		if f.Mode()&os.ModeSymlink != 0 || (!f.FileInfo().IsDir() && !f.Mode().IsRegular()) {
			return nil, fmt.Errorf("non-regular archive entry: %s", p)
		}
		target := filepath.Join(dest, filepath.FromSlash(p))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0700); err != nil {
				return nil, err
			}
			continue
		}
		if limits.Bytes > 0 && f.UncompressedSize64 > uint64(limits.Bytes-total) {
			return nil, fmt.Errorf("archive expanded size exceeds %d bytes", limits.Bytes)
		}
		if limits.Ratio > 0 && f.UncompressedSize64 > 1<<20 && (f.CompressedSize64 == 0 || f.UncompressedSize64/f.CompressedSize64 > limits.Ratio) {
			return nil, fmt.Errorf("suspicious compression ratio: %s", p)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		src, err := f.Open()
		if err != nil {
			return nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			return nil, err
		}
		h := sha256.New()
		var r io.Reader = contextReader{ctx, src}
		if limits.Bytes > 0 {
			r = io.LimitReader(contextReader{ctx, src}, limits.Bytes-total+1)
		}
		count, copyErr := io.Copy(io.MultiWriter(out, h), r)
		syncErr := out.Sync()
		closeErr := out.Close()
		srcErr := src.Close()
		total += count
		if copyErr != nil {
			return nil, copyErr
		}
		if syncErr != nil {
			return nil, syncErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if srcErr != nil {
			return nil, srcErr
		}
		if limits.Bytes > 0 && total > limits.Bytes {
			return nil, fmt.Errorf("archive expanded size exceeds %d bytes", limits.Bytes)
		}
		if count != int64(f.UncompressedSize64) {
			return nil, fmt.Errorf("archive length mismatch: %s", p)
		}
		entries[p] = archiveEntry{Path: p, Size: count, SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	return entries, nil
}

func hashFile(filename string) (string, int64, error) {
	return hashFileProgress(context.Background(), filename, nil)
}
func hashFileProgress(ctx context.Context, filename string, progress func(int64) error) (string, int64, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, &progressReader{reader: contextReader{ctx, f}, progress: progress})
	return hex.EncodeToString(h.Sum(nil)), n, err
}
func atomicJSON(filename string, value any) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(filename), ".state-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filename); err != nil {
		return err
	}
	return syncDir(filepath.Dir(filename))
}
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	err = f.Sync()
	if runtime.GOOS == "windows" && (errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.EACCES)) {
		return nil
	}
	return err
}
func copyFile(src, dst string) error { return copyFileContext(context.Background(), src, dst) }
func copyFileContext(ctx context.Context, src, dst string) error {
	return copyFileProgress(ctx, src, dst, nil)
}
func copyFileProgress(ctx context.Context, src, dst string, progress func(int64) error) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, &progressReader{reader: contextReader{ctx, in}, progress: progress})
	if err == nil {
		err = out.Sync()
	}
	ce := out.Close()
	if err == nil {
		err = ce
	}
	return err
}
func addZIPFile(z *zip.Writer, name, src string) error {
	return addZIPFileContext(context.Background(), z, name, src)
}
func addZIPFileContext(ctx context.Context, z *zip.Writer, name, src string) error {
	return addZIPFileProgress(ctx, z, name, src, nil)
}
func addZIPFileProgress(ctx context.Context, z *zip.Writer, name, src string, progress func(int64) error) error {
	n, err := safeArchivePath(name)
	if err != nil {
		return err
	}
	w, err := z.Create(n)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, &progressReader{reader: contextReader{ctx, f}, progress: progress})
	return err
}
func addZIPBytes(z *zip.Writer, name string, data []byte) error {
	n, err := safeArchivePath(name)
	if err != nil {
		return err
	}
	w, err := z.Create(n)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func ensureDiskSpace(dir string, bytes int64) error {
	free, err := freeDiskBytes(dir)
	if err != nil {
		return err
	}
	if bytes < 0 || uint64(bytes) > free || free-uint64(bytes) < 16<<20 {
		return Err(507, "INSUFFICIENT_STORAGE", "可用磁盘空间不足，任务已停止", map[string]any{"required_bytes": bytes, "available_bytes": free, "reserve_bytes": 16 << 20})
	}
	return nil
}
func transferValidationError(err error, backup bool) error {
	var api *APIError
	var disk *os.PathError
	var maximum *http.MaxBytesError
	if errors.As(err, &maximum) {
		return Err(413, "PAYLOAD_TOO_LARGE", "上传请求超过大小限制", map[string]any{"max_bytes": maximum.Limit})
	}
	if errors.As(err, &api) || errors.As(err, &disk) || errors.Is(err, syscall.ENOSPC) {
		return err
	}
	message := "导入包预检失败，请检查包内文件、资源引用和清单"
	code := "IMPORT_PREFLIGHT_FAILED"
	status := 422
	if backup {
		message = "备份校验失败，请检查文件完整性和校验清单"
		code = "BACKUP_INVALID"
		status = 400
	}
	reason := err.Error()
	switch {
	case strings.Contains(reason, "exceeds") || strings.Contains(reason, "overflows") || strings.Contains(reason, "compression ratio"):
		return Err(413, "ARCHIVE_LIMIT_EXCEEDED", "归档超过文件大小、数量、深度或压缩比限制", map[string]any{"reason": reason})
	case strings.Contains(reason, "archive filename encoding"):
		message = "ZIP 文件名编码无效，请使用 UTF-8 或 GBK 编码重新打包"
	case strings.Contains(reason, "unsafe archive") || strings.Contains(reason, "non-regular") || strings.Contains(reason, "leaves package") || strings.Contains(reason, "invalid local link"):
		message = "归档包含危险路径、符号链接或越界资源引用"
	case strings.Contains(reason, "missing local") || strings.Contains(reason, "missing attachment"):
		message = "导入包缺少正文引用的本地资源，整包已拒绝"
	case strings.Contains(reason, "hash") || strings.Contains(reason, "checksum"):
		message = "归档文件校验和不一致，内容可能已损坏"
	case strings.Contains(reason, "only Markdown"):
		return Err(415, "UNSUPPORTED_FILE_TYPE", "仅支持 Markdown 文件与 Markdown 资源 ZIP", nil)
	}
	return Err(status, code, message, map[string]any{"reason": reason})
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type progressReader struct {
	reader   io.Reader
	progress func(int64) error
	bytes    int64
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	if n > 0 && r.progress != nil {
		if progressErr := r.progress(r.bytes); progressErr != nil {
			return n, progressErr
		}
	}
	return n, err
}

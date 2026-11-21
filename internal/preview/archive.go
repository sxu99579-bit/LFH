package preview

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	MaxArchiveEntries        = 2000
	MaxArchiveEntryReadBytes = 64 * 1024 * 1024
	MaxTextPreviewBytes      = 2 * 1024 * 1024
)

type ArchiveEntry struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Dir        string `json:"dir,omitempty"`
	IsDir      bool   `json:"is_dir"`
	Size       int64  `json:"size"`
	Compressed int64  `json:"compressed,omitempty"`
	ModTime    string `json:"mod_time,omitempty"`
	Ext        string `json:"ext,omitempty"`
	CanPreview bool   `json:"can_preview"`
}

type ArchiveResult struct {
	Type       string         `json:"type"`   // "archive"
	Format     string         `json:"format"` // "zip", "tar", "tar.gz", "gz"
	TotalFiles int            `json:"total_files"`
	TotalDirs  int            `json:"total_dirs"`
	TotalSize  int64          `json:"total_size"`
	Truncated  bool           `json:"truncated,omitempty"`
	Entries    []ArchiveEntry `json:"entries"`
}

func DetectArchiveFormat(name string) string {
	low := strings.ToLower(strings.TrimSpace(filepath.Base(name)))
	switch {
	case strings.HasSuffix(low, ".zip"):
		return "zip"
	case strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz"):
		return "tar.gz"
	case strings.HasSuffix(low, ".tar"):
		return "tar"
	case strings.HasSuffix(low, ".gz"):
		return "gz"
	default:
		return ""
	}
}

func IsArchiveName(name string) bool {
	return DetectArchiveFormat(name) != ""
}

func CleanArchivePath(raw string) (string, bool) {
	s := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	s = strings.TrimLeft(s, "/")
	if s == "" {
		return "", false
	}
	cleaned := path.Clean(s)
	if cleaned == "" || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", false
	}
	if len(cleaned) >= 2 && cleaned[1] == ':' {
		return "", false
	}
	return cleaned, true
}

func CanPreviewExt(ext string) bool {
	if IsTextPreviewExt(ext) {
		return true
	}
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "docx", "xlsx", "pptx", "pdf",
		"jpg", "jpeg", "png", "gif", "webp", "svg", "bmp",
		"mp4", "webm", "mov", "m4v", "ogv",
		"mp3", "wav", "flac", "ogg", "m4a":
		return true
	default:
		return false
	}
}

func IsTextPreviewExt(ext string) bool {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	switch ext {
	case "md", "markdown", "txt", "json", "yaml", "yml", "log", "csv",
		"go", "js", "ts", "jsx", "tsx", "py", "html", "css", "xml",
		"sh", "bat", "ps1", "ini", "conf", "toml", "env", "sql",
		"c", "cpp", "h", "java", "rs", "php", "rb":
		return true
	default:
		return false
	}
}

func buildEntry(cleanPath string, isDir bool, size, compressed int64, mod time.Time) ArchiveEntry {
	name := path.Base(cleanPath)
	dir := path.Dir(cleanPath)
	if dir == "." {
		dir = ""
	}
	ext := ""
	if !isDir {
		ext = strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	}
	modStr := ""
	if !mod.IsZero() && mod.Year() > 1970 {
		modStr = mod.Format("2006-01-02 15:04:05")
	}
	return ArchiveEntry{
		Path:       cleanPath,
		Name:       name,
		Dir:        dir,
		IsDir:      isDir,
		Size:       size,
		Compressed: compressed,
		ModTime:    modStr,
		Ext:        ext,
		CanPreview: !isDir && CanPreviewExt(ext),
	}
}

func InspectArchive(filePath, origName string) (*ArchiveResult, error) {
	lowExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(origName), "."))
	if lowExt == "rar" || lowExt == "7z" {
		return nil, errors.New("暂不支持 .rar / .7z 在线解压，请下载查看或转换为 .zip / .tar.gz")
	}

	format := DetectArchiveFormat(origName)
	if format == "" {
		return nil, errors.New("不支持的压缩包格式")
	}

	res := &ArchiveResult{
		Type:    "archive",
		Format:  format,
		Entries: []ArchiveEntry{},
	}

	switch format {
	case "zip":
		zr, err := zip.OpenReader(filePath)
		if err != nil {
			return nil, fmt.Errorf("解析 ZIP 压缩包失败: %w", err)
		}
		defer zr.Close()

		for _, f := range zr.File {
			cleanPath, ok := CleanArchivePath(f.Name)
			if !ok {
				continue
			}
			isDir := f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/")
			if isDir {
				res.TotalDirs++
			} else {
				res.TotalFiles++
				res.TotalSize += int64(f.UncompressedSize64)
			}
			if len(res.Entries) >= MaxArchiveEntries {
				res.Truncated = true
				continue
			}
			res.Entries = append(res.Entries, buildEntry(
				cleanPath,
				isDir,
				int64(f.UncompressedSize64),
				int64(f.CompressedSize64),
				f.Modified,
			))
		}

	case "tar", "tar.gz":
		f, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()

		var r io.Reader = f
		if format == "tar.gz" {
			gz, gzErr := gzip.NewReader(f)
			if gzErr != nil {
				return nil, fmt.Errorf("解析 GZIP 压缩包失败: %w", gzErr)
			}
			defer gz.Close()
			r = gz
		}

		tr := tar.NewReader(r)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("解析 TAR 压缩包失败: %w", err)
			}
			cleanPath, ok := CleanArchivePath(hdr.Name)
			if !ok {
				continue
			}
			isDir := hdr.Typeflag == tar.TypeDir || strings.HasSuffix(hdr.Name, "/")
			if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA && !isDir {
				continue
			}
			if isDir {
				res.TotalDirs++
			} else {
				res.TotalFiles++
				res.TotalSize += hdr.Size
			}
			if len(res.Entries) >= MaxArchiveEntries {
				res.Truncated = true
				continue
			}
			res.Entries = append(res.Entries, buildEntry(cleanPath, isDir, hdr.Size, 0, hdr.ModTime))
		}

	case "gz":
		f, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("解析 GZIP 文件失败: %w", err)
		}
		defer gz.Close()

		innerName := strings.TrimSpace(gz.Name)
		if innerName == "" {
			base := filepath.Base(origName)
			innerName = strings.TrimSuffix(base, filepath.Ext(base))
			if innerName == "" {
				innerName = "content.bin"
			}
		}
		cleanPath, ok := CleanArchivePath(innerName)
		if !ok {
			cleanPath = "content.bin"
		}
		n, err := io.Copy(io.Discard, io.LimitReader(gz, MaxArchiveEntryReadBytes+1))
		if err != nil {
			return nil, fmt.Errorf("读取 GZIP 内容失败: %w", err)
		}
		res.TotalFiles = 1
		res.TotalSize = n
		res.Entries = append(res.Entries, buildEntry(cleanPath, false, n, 0, gz.ModTime))
	}

	sort.Slice(res.Entries, func(i, j int) bool {
		if res.Entries[i].IsDir != res.Entries[j].IsDir {
			return res.Entries[i].IsDir
		}
		return res.Entries[i].Path < res.Entries[j].Path
	})
	return res, nil
}

func ReadArchiveEntry(filePath, origName, targetPath string, maxBytes int64) ([]byte, string, string, error) {
	if maxBytes <= 0 {
		maxBytes = MaxArchiveEntryReadBytes
	}
	cleanTarget, ok := CleanArchivePath(targetPath)
	if !ok {
		return nil, "", "", errors.New("压缩包内路径非法")
	}

	format := DetectArchiveFormat(origName)
	switch format {
	case "zip":
		zr, err := zip.OpenReader(filePath)
		if err != nil {
			return nil, "", "", err
		}
		defer zr.Close()

		for _, f := range zr.File {
			cleanPath, ok := CleanArchivePath(f.Name)
			if !ok || f.FileInfo().IsDir() || cleanPath != cleanTarget {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, "", "", err
			}
			data, readErr := readLimited(rc, maxBytes)
			_ = rc.Close()
			if readErr != nil {
				return nil, "", "", readErr
			}
			name := path.Base(cleanPath)
			return data, name, strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")), nil
		}
		return nil, "", "", errors.New("压缩包中未找到该文件")

	case "tar", "tar.gz":
		f, err := os.Open(filePath)
		if err != nil {
			return nil, "", "", err
		}
		defer f.Close()

		var r io.Reader = f
		if format == "tar.gz" {
			gz, gzErr := gzip.NewReader(f)
			if gzErr != nil {
				return nil, "", "", gzErr
			}
			defer gz.Close()
			r = gz
		}

		tr := tar.NewReader(r)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, "", "", err
			}
			cleanPath, ok := CleanArchivePath(hdr.Name)
			if !ok || hdr.Typeflag == tar.TypeDir || cleanPath != cleanTarget {
				continue
			}
			if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
				continue
			}
			data, readErr := readLimited(tr, maxBytes)
			if readErr != nil {
				return nil, "", "", readErr
			}
			name := path.Base(cleanPath)
			return data, name, strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")), nil
		}
		return nil, "", "", errors.New("压缩包中未找到该文件")

	case "gz":
		f, err := os.Open(filePath)
		if err != nil {
			return nil, "", "", err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, "", "", err
		}
		defer gz.Close()
		data, err := readLimited(gz, maxBytes)
		if err != nil {
			return nil, "", "", err
		}
		name := path.Base(cleanTarget)
		return data, name, strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")), nil

	default:
		return nil, "", "", errors.New("暂不支持在线读取该压缩格式")
	}
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("压缩包内单个文件超过大小限制，请直接下载查看")
	}
	return data, nil
}

func WalkArchiveFiles(filePath, origName string, maxFiles int, maxTotalBytes int64, fn func(relPath string, size int64, r io.Reader) error) error {
	if maxFiles <= 0 {
		maxFiles = 200
	}
	if maxTotalBytes <= 0 {
		maxTotalBytes = 256 * 1024 * 1024
	}
	format := DetectArchiveFormat(origName)
	var count int
	var total int64

	switch format {
	case "zip":
		zr, err := zip.OpenReader(filePath)
		if err != nil {
			return err
		}
		defer zr.Close()

		for _, f := range zr.File {
			cleanPath, ok := CleanArchivePath(f.Name)
			if !ok || f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
				continue
			}
			count++
			if count > maxFiles {
				return fmt.Errorf("压缩包内文件数量超过在线解压上限 (%d 个)", maxFiles)
			}
			sz := int64(f.UncompressedSize64)
			if sz < 0 || total+sz > maxTotalBytes {
				return errors.New("解压总大小超过安全上限 (256 MB)")
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			lr := io.LimitReader(rc, maxTotalBytes-total+1)
			err = fn(cleanPath, sz, lr)
			_ = rc.Close()
			if err != nil {
				return err
			}
			total += sz
		}
		return nil

	case "tar", "tar.gz":
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()

		var r io.Reader = f
		if format == "tar.gz" {
			gz, gzErr := gzip.NewReader(f)
			if gzErr != nil {
				return gzErr
			}
			defer gz.Close()
			r = gz
		}

		tr := tar.NewReader(r)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			cleanPath, ok := CleanArchivePath(hdr.Name)
			if !ok || hdr.Typeflag == tar.TypeDir || (hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA) {
				continue
			}
			count++
			if count > maxFiles {
				return fmt.Errorf("压缩包内文件数量超过在线解压上限 (%d 个)", maxFiles)
			}
			if hdr.Size < 0 || total+hdr.Size > maxTotalBytes {
				return errors.New("解压总大小超过安全上限 (256 MB)")
			}
			lr := io.LimitReader(tr, hdr.Size)
			if err := fn(cleanPath, hdr.Size, lr); err != nil {
				return err
			}
			total += hdr.Size
		}
		return nil

	case "gz":
		f, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()

		innerName := strings.TrimSpace(gz.Name)
		if innerName == "" {
			base := filepath.Base(origName)
			innerName = strings.TrimSuffix(base, filepath.Ext(base))
		}
		cleanPath, ok := CleanArchivePath(innerName)
		if !ok {
			cleanPath = "extracted.bin"
		}
		return fn(cleanPath, 0, io.LimitReader(gz, maxTotalBytes))

	default:
		return errors.New("暂不支持在线解压该压缩格式")
	}
}

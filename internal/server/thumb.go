package server

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"lfh/internal/auth"
	"lfh/internal/preview"
)

func init() {
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
}

func (s *Server) getThumbCache() *preview.ThumbCache {
	s.thumbCacheMu.Lock()
	defer s.thumbCacheMu.Unlock()
	if s.thumbCache == nil {
		s.thumbCache = preview.NewThumbCache(filepath.Join(s.store.DataDir(), "cache", "thumbs"))
	}
	return s.thumbCache
}

func (s *Server) handleClusterThumb(w http.ResponseWriter, r *http.Request) {
	s.serveFileThumb(w, r, r.PathValue("id"))
}

func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id := r.PathValue("id")
	_, _, peer, isLocal, status, errMsg := s.findAccessibleFileForPrincipal(id, p)
	if status != http.StatusOK {
		if status == http.StatusNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, errMsg, status)
		}
		return
	}
	if isLocal {
		s.serveFileThumb(w, r, id)
		return
	}
	s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+id+"/thumb")
}

func (s *Server) serveFileThumb(w http.ResponseWriter, r *http.Request, id string) {
	rec, fp, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	st, statErr := os.Stat(fp)
	if statErr != nil {
		if isExternalSourceMissingErr(rec, statErr) {
			s.store.RecordReadError()
			http.Error(w, "源文件已移动或删除，外部引用已失效", http.StatusGone)
			return
		}
		http.NotFound(w, r)
		return
	}
	if !preview.IsThumbImageExt(rec.Ext) {
		http.Error(w, "该格式暂不支持服务端缩略图", http.StatusUnsupportedMediaType)
		return
	}

	reqW, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("w")))
	maxDim := preview.ClampThumbWidth(reqW)

	ver := rec.Version
	if ver <= 0 {
		ver = 1
	}
	stamp := strings.TrimSpace(rec.SHA256)
	if len(stamp) > 12 {
		stamp = stamp[:12]
	}
	if stamp == "" {
		stamp = fmt.Sprintf("%d_%d", st.Size(), st.ModTime().Unix())
	}
	cacheKey := fmt.Sprintf("%s_v%d_%s_%d", id, ver, stamp, maxDim)
	etag := fmt.Sprintf("%q", cacheKey)

	if match := strings.TrimSpace(r.Header.Get("If-None-Match")); match != "" {
		if match == etag || match == "W/"+etag || strings.Contains(match, cacheKey) {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	thumbPath, contentType, err := s.getThumbCache().GetOrBuild(cacheKey, fp, maxDim)
	if err != nil {
		http.Error(w, "生成缩略图失败: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	data, err := os.ReadFile(thumbPath)
	if err != nil {
		http.Error(w, "读取缩略图缓存失败", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=604800, immutable")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func trimValidUTF8Prefix(raw []byte) string {
	for len(raw) > 0 && !utf8.Valid(raw) {
		// Drop at most 3 trailing incomplete UTF-8 continuation bytes.
		if len(raw) >= 1 && utf8.RuneStart(raw[len(raw)-1]) {
			raw = raw[:len(raw)-1]
			break
		}
		raw = raw[:len(raw)-1]
	}
	return strings.ToValidUTF8(string(raw), "")
}

func truncateSnippetRunes(s string, maxRunes int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxRunes {
		return string(r)
	}
	return string(r[:maxRunes])
}

func extractOfficeSnippetLines(res any) []string {
	lines := make([]string, 0, 16)
	push := func(s string) {
		if len(lines) >= 16 {
			return
		}
		clean := truncateSnippetRunes(s, 120)
		if clean != "" {
			lines = append(lines, clean)
		}
	}

	switch v := res.(type) {
	case *preview.DocxResult:
		for _, b := range v.Blocks {
			if len(lines) >= 16 {
				break
			}
			if b.Text != "" {
				push(b.Text)
			}
			for _, row := range b.Rows {
				if len(lines) >= 16 {
					break
				}
				push(strings.Join(row, " | "))
			}
		}
	case preview.DocxResult:
		return extractOfficeSnippetLines(&v)
	case *preview.XlsxResult:
		for _, sh := range v.Sheets {
			if len(lines) >= 16 {
				break
			}
			if sh.Name != "" {
				push("[" + sh.Name + "]")
			}
			for _, row := range sh.Rows {
				if len(lines) >= 16 {
					break
				}
				push(strings.Join(row, " | "))
			}
		}
	case preview.XlsxResult:
		return extractOfficeSnippetLines(&v)
	case *preview.PptxResult:
		for _, sl := range v.Slides {
			if len(lines) >= 16 {
				break
			}
			if sl.Title != "" {
				push(sl.Title)
			}
			for _, l := range sl.Lines {
				if len(lines) >= 16 {
					break
				}
				push(l)
			}
		}
	case preview.PptxResult:
		return extractOfficeSnippetLines(&v)
	}
	return lines
}

func (s *Server) serveTextSnippet(w http.ResponseWriter, fp, ext string) {
	const maxSnippetBytes = 2048
	f, err := os.Open(fp)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		return
	}
	defer f.Close()

	buf, err := io.ReadAll(io.LimitReader(f, maxSnippetBytes+1))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		return
	}
	truncated := false
	if len(buf) > maxSnippetBytes {
		buf = buf[:maxSnippetBytes]
		truncated = true
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"type":      ext,
		"is_md":     ext == "md" || ext == "markdown",
		"content":   trimValidUTF8Prefix(buf),
		"snippet":   true,
		"truncated": truncated,
	})
}

func (s *Server) serveOfficeSnippet(w http.ResponseWriter, fp, ext string) {
	res, err := preview.ParseOffice(fp, ext)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"type":    ext,
		"lines":   extractOfficeSnippetLines(res),
		"snippet": true,
	})
}

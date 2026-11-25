package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lfh/internal/auth"
	"lfh/internal/storage"
)

type FolderEntry struct {
	Path      string `json:"path"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

// FolderStore persists explicitly created virtual folders atomically in <dataDir>/folders.json.
type FolderStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]FolderEntry
}

func NewFolderStore(dataDir string) *FolderStore {
	fs := &FolderStore{
		path:    filepath.Join(dataDir, "folders.json"),
		entries: make(map[string]FolderEntry),
	}
	fs.load()
	return fs
}

func (f *FolderStore) load() {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return
	}
	var list []FolderEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		return
	}
	for _, e := range list {
		clean := storage.SanitizeFolderPath(e.Path)
		if clean == "" {
			continue
		}
		e.Path = clean
		f.entries[clean] = e
	}
}

func (f *FolderStore) saveLocked() error {
	list := make([]FolderEntry, 0, len(f.entries))
	for _, e := range f.entries {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].Path < list[j].Path
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	tmp := f.path + ".tmp." + hex.EncodeToString(b)
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, f.path)
}

func (f *FolderStore) Ensure(folderPath, createdBy string) (string, error) {
	clean := storage.SanitizeFolderPath(folderPath)
	if clean == "" {
		return "", nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.entries[clean]; !ok {
		f.entries[clean] = FolderEntry{
			Path:      clean,
			CreatedBy: createdBy,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		if err := f.saveLocked(); err != nil {
			return "", err
		}
	}
	return clean, nil
}

func (f *FolderStore) List() []FolderEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FolderEntry, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Path < out[j].Path
	})
	return out
}

func (f *FolderStore) RenamePrefix(oldPath, newPath, actor string) error {
	oldClean := storage.SanitizeFolderPath(oldPath)
	newClean := storage.SanitizeFolderPath(newPath)
	if oldClean == "" || newClean == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := oldClean + "/"
	changed := false
	for k, e := range f.entries {
		if k == oldClean || strings.HasPrefix(k, prefix) {
			delete(f.entries, k)
			suffix := strings.TrimPrefix(k, oldClean)
			nextKey := storage.SanitizeFolderPath(newClean + suffix)
			if nextKey != "" {
				e.Path = nextKey
				f.entries[nextKey] = e
			}
			changed = true
		}
	}
	if _, ok := f.entries[newClean]; !ok {
		f.entries[newClean] = FolderEntry{
			Path:      newClean,
			CreatedBy: actor,
			CreatedAt: time.Now().Format(time.RFC3339),
		}
		changed = true
	}
	if changed {
		return f.saveLocked()
	}
	return nil
}

func (f *FolderStore) RemovePrefix(folderPath string) error {
	clean := storage.SanitizeFolderPath(folderPath)
	if clean == "" {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := clean + "/"
	changed := false
	for k := range f.entries {
		if k == clean || strings.HasPrefix(k, prefix) {
			delete(f.entries, k)
			changed = true
		}
	}
	if changed {
		return f.saveLocked()
	}
	return nil
}

func (s *Server) getFolderStore() *FolderStore {
	s.folderStoreMu.Lock()
	defer s.folderStoreMu.Unlock()
	if s.folderStore == nil {
		s.folderStore = NewFolderStore(s.store.DataDir())
	}
	return s.folderStore
}

func addFolderWithAncestors(set map[string]struct{}, folderPath string) {
	clean := storage.SanitizeFolderPath(folderPath)
	if clean == "" {
		return
	}
	parts := strings.Split(clean, "/")
	for i := range parts {
		sub := strings.Join(parts[:i+1], "/")
		if sub != "" {
			set[sub] = struct{}{}
		}
	}
}

func canSeeUserFolderEntry(p auth.Principal, folderPath string) bool {
	if p.User.CanAccessFolder(folderPath) {
		return true
	}
	clean := storage.SanitizeFolderPath(folderPath)
	if clean == "" {
		return true
	}
	for _, rule := range p.User.AllowedFolders {
		if auth.FolderMatchesPrefix(rule, clean) {
			return true
		}
	}
	return false
}

func (s *Server) collectAccessibleFolderPaths(p auth.Principal) ([]string, []FolderEntry) {
	explicit := s.getFolderStore().List()
	pathSet := make(map[string]struct{})
	visibleExplicit := make([]FolderEntry, 0, len(explicit))
	for _, e := range explicit {
		if !canSeeUserFolderEntry(p, e.Path) {
			continue
		}
		if p.IsAgent && !p.CanReadFolder(e.Path) {
			continue
		}
		visibleExplicit = append(visibleExplicit, e)
		addFolderWithAncestors(pathSet, e.Path)
	}
	for _, rec := range s.filterAccessibleForPrincipal(s.cluster.AllClusterFiles(), p) {
		addFolderWithAncestors(pathSet, rec.FolderPath)
	}
	folders := make([]string, 0, len(pathSet))
	for fp := range pathSet {
		folders = append(folders, fp)
	}
	sort.Strings(folders)
	return folders, visibleExplicit
}

func matchFolderTree(folderPath, root string) bool {
	fp := storage.SanitizeFolderPath(folderPath)
	return fp == root || strings.HasPrefix(fp, root+"/")
}

func parentFolderPath(folderPath string) string {
	clean := storage.SanitizeFolderPath(folderPath)
	if clean == "" {
		return ""
	}
	dir := path.Dir(clean)
	if dir == "." || dir == "/" {
		return ""
	}
	return storage.SanitizeFolderPath(dir)
}

func (s *Server) handleClusterUpdateFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FolderPath string `json:"folder_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	rec, err := s.store.SetFolderPath(r.PathValue("id"), req.FolderPath)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	folders, explicit := s.collectAccessibleFolderPaths(p)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"folders":  folders,
		"explicit": explicit,
	})
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	if !p.User.EffectiveCanManageFolders() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号为只读权限或无权创建文件夹"})
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	clean := storage.SanitizeFolderPath(req.Path)
	if clean == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件夹路径不能为空或包含非法字符"})
		return
	}
	if !p.User.CanAccessFolder(clean) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号无权在该目录范围创建文件夹"})
		return
	}
	if p.IsAgent && !p.CanWriteFolder(clean) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥无权在该目录创建文件夹"})
		return
	}
	if _, err := s.getFolderStore().Ensure(clean, p.User.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存文件夹失败: " + err.Error()})
		return
	}
	s.logAudit(r, p.User.Username, "folder_create", clean, "ok", p.ActorLabel())
	s.events.Publish(FileChangeEvent{
		Kind:        "folder",
		Uploader:    p.User.Username,
		ActorName:   p.ActorLabel(),
		FolderPaths: []string{clean},
	})
	folders, _ := s.collectAccessibleFolderPaths(p)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":      true,
		"path":    clean,
		"folders": folders,
	})
}

func (s *Server) handleUpdateFileFolder(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	u := p.User
	if !u.EffectiveCanManageFolders() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号为只读权限或无权移动文件夹内文件"})
		return
	}
	id := r.PathValue("id")
	rec, _, _, _, status, errMsg := s.findAccessibleFileForPrincipal(id, p)
	if status != http.StatusOK {
		writeJSON(w, status, map[string]string{"error": errMsg})
		return
	}
	if u.Role != "admin" && rec.Uploader != u.Username {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "仅管理员或原上传者可移动该文件"})
		return
	}
	if rec.MountID != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "外部虚拟挂载目录内的文件不支持移动虚拟目录"})
		return
	}
	var req struct {
		FolderPath string `json:"folder_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	newFolder := storage.SanitizeFolderPath(req.FolderPath)
	oldFolder := storage.SanitizeFolderPath(rec.FolderPath)
	if !u.CanAccessFolder(oldFolder) || !u.CanAccessFolder(newFolder) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号无权操作来源或目标目录"})
		return
	}
	if p.IsAgent && (!p.CanWriteFolder(oldFolder) || !p.CanWriteFolder(newFolder)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥无权在来源或目标目录移动文件"})
		return
	}

	updated, err := s.cluster.UpdateFileFolder(id, newFolder)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	if newFolder != "" {
		_, _ = s.getFolderStore().Ensure(newFolder, u.Username)
	}
	s.logAudit(r, u.Username, "file_move_folder", rec.Name, "ok", oldFolder+" -> "+newFolder)
	s.events.Publish(FileChangeEvent{
		Kind:         "folder",
		Uploader:     u.Username,
		ActorName:    p.ActorLabel(),
		Count:        1,
		FileNames:    []string{rec.Name},
		FolderPaths:  []string{oldFolder, newFolder},
		AllowedUsers: updated.AllowedUsers,
	})
	writeJSON(w, http.StatusOK, map[string]any{"file": s.sanitizeRecordForPrincipal(updated, p)})
}

func (s *Server) handleRenameFolder(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	u := p.User
	if !u.EffectiveCanManageFolders() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号为只读权限或无权管理文件夹"})
		return
	}
	var req struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	oldPath := storage.SanitizeFolderPath(req.OldPath)
	newPath := storage.SanitizeFolderPath(req.NewPath)
	if oldPath == "" || newPath == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "原目录与新目录路径均不能为空"})
		return
	}
	if !u.CanAccessFolder(oldPath) || !u.CanAccessFolder(newPath) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号无权操作该目录范围"})
		return
	}
	if oldPath == newPath {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": newPath, "moved": 0})
		return
	}
	if strings.HasPrefix(newPath, oldPath+"/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不能将目录移动到其自身的子目录下"})
		return
	}
	if p.IsAgent && (!p.CanWriteFolder(oldPath) || !p.CanWriteFolder(newPath)) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥无权修改该目录"})
		return
	}

	allFiles := s.cluster.AllClusterFiles()
	targets := make([]storage.FileRecord, 0)
	for _, rec := range allFiles {
		if !matchFolderTree(rec.FolderPath, oldPath) {
			continue
		}
		if rec.MountID != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "目录内包含外部只读/虚拟挂载文件，暂不支持批量重命名"})
			return
		}
		if u.Role != "admin" && rec.Uploader != u.Username {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "目录内包含其他成员的文件，仅管理员可批量重命名"})
			return
		}
		targets = append(targets, rec)
	}

	moved := 0
	for _, rec := range targets {
		fp := storage.SanitizeFolderPath(rec.FolderPath)
		suffix := strings.TrimPrefix(fp, oldPath)
		nextFolder := storage.SanitizeFolderPath(newPath + suffix)
		if _, err := s.cluster.UpdateFileFolder(rec.ID, nextFolder); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "重命名部分文件失败: " + err.Error()})
			return
		}
		moved++
	}

	if err := s.getFolderStore().RenamePrefix(oldPath, newPath, u.Username); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "folder_rename", oldPath, "ok", "-> "+newPath)
	s.events.Publish(FileChangeEvent{
		Kind:        "folder",
		Uploader:    u.Username,
		ActorName:   p.ActorLabel(),
		Count:       moved,
		FolderPaths: []string{oldPath, newPath},
	})
	folders, _ := s.collectAccessibleFolderPaths(p)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"path":    newPath,
		"moved":   moved,
		"folders": folders,
	})
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	u := p.User
	if !u.EffectiveCanManageFolders() {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号为只读权限或无权删除文件夹"})
		return
	}
	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"` // empty_only | move_to_parent | delete_files
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	clean := storage.SanitizeFolderPath(req.Path)
	if clean == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件夹路径不能为空"})
		return
	}
	if !u.CanAccessFolder(clean) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "当前账号无权删除该目录范围"})
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "move_to_parent"
	}
	if mode != "empty_only" && mode != "move_to_parent" && mode != "delete_files" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不支持的删除模式: " + mode})
		return
	}

	allFiles := s.cluster.AllClusterFiles()
	targets := make([]storage.FileRecord, 0)
	for _, rec := range allFiles {
		if !matchFolderTree(rec.FolderPath, clean) {
			continue
		}
		if rec.MountID != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "目录内包含虚拟挂载文件，请从挂载管理中卸载"})
			return
		}
		if u.Role != "admin" && rec.Uploader != u.Username {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "目录内包含其他成员的文件，仅管理员可操作"})
			return
		}
		if mode == "delete_files" && p.IsAgent && !p.CanDeleteFolder(rec.FolderPath) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Agent 密钥无权删除目录内文件"})
			return
		}
		targets = append(targets, rec)
	}

	if mode == "empty_only" && len(targets) > 0 {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "文件夹非空，请先移走文件或选择「移回上级 / 连同删除」"})
		return
	}

	affected := 0
	parent := parentFolderPath(clean)
	for _, rec := range targets {
		switch mode {
		case "move_to_parent":
			if _, err := s.cluster.UpdateFileFolder(rec.ID, parent); err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": "移出目录内文件失败: " + err.Error()})
				return
			}
			affected++
		case "delete_files":
			_, _, peer, isLocal, status, errMsg := s.findAccessibleFileForPrincipal(rec.ID, p)
			if status != http.StatusOK {
				writeJSON(w, status, map[string]string{"error": errMsg})
				return
			}
			if isLocal {
				if err := s.store.DeleteWithConcurrency(rec.ID, storage.WriteConcurrencyOptions{}); err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
					return
				}
			} else {
				if err := s.cluster.ProxyDeleteWithConcurrency(peer, rec.ID, storage.WriteConcurrencyOptions{}); err != nil {
					writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
					return
				}
			}
			affected++
		}
	}

	if err := s.getFolderStore().RemovePrefix(clean); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "folder_delete", clean, "ok", mode)
	s.events.Publish(FileChangeEvent{
		Kind:        "folder",
		Uploader:    u.Username,
		ActorName:   p.ActorLabel(),
		Count:       affected,
		FolderPaths: []string{clean, parent},
	})
	folders, _ := s.collectAccessibleFolderPaths(p)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"affected": affected,
		"folders":  folders,
	})
}

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
	"lfh/internal/mcp"
	"lfh/internal/storage"
)

type serverMCPBackend struct {
	srv *Server
}

func newServerMCPBackend(srv *Server) mcp.Backend {
	return &serverMCPBackend{srv: srv}
}

func (b *serverMCPBackend) Authenticate(r *http.Request) (auth.Principal, error) {
	return b.srv.auth.CurrentPrincipal(r)
}

func (b *serverMCPBackend) EventsDone() <-chan struct{} {
	if b.srv.events == nil {
		return nil
	}
	return b.srv.events.Done()
}

func hasReadableSubpath(p auth.Principal, prefix string) bool {
	if !p.IsAgent || p.AgentKey == nil {
		return true
	}
	if !p.HasScope(auth.ScopeRead) {
		return false
	}
	clean := strings.ToLower(storage.SanitizeFolderPath(prefix))
	checkRule := func(rawRule string) bool {
		r := strings.ToLower(strings.Trim(strings.ReplaceAll(strings.TrimSpace(rawRule), "\\", "/"), "/"))
		if r == "*" {
			return true
		}
		if clean == "" {
			return true
		}
		return r == clean || strings.HasPrefix(r, clean+"/")
	}
	for _, r := range p.AgentKey.ReadFolders {
		if checkRule(r) {
			return true
		}
	}
	for _, r := range p.AgentKey.WriteFolders {
		if checkRule(r) {
			return true
		}
	}
	return false
}

func mountVisibleToPrincipal(alias string, addedBy string, allowedUsers []string, p auth.Principal) bool {
	if len(allowedUsers) > 0 && !storage.CanUserAccess(storage.FileRecord{Uploader: addedBy, AllowedUsers: allowedUsers}, p.User.Username, p.User.Role) {
		return false
	}
	if !p.IsAgent {
		return true
	}
	cleanAlias := storage.SanitizeFolderPath(alias)
	return p.CanReadFolder(cleanAlias) || hasReadableSubpath(p, cleanAlias)
}

func formatMCPUpdatedBy(rec storage.FileRecord) string {
	if strings.HasPrefix(rec.DeviceName, "Agent · ") {
		return rec.DeviceName
	}
	if rec.Uploader != "" {
		return rec.Uploader
	}
	return rec.DeviceName
}

func (b *serverMCPBackend) toFileSummary(rec storage.FileRecord, lease *storage.FileLease) mcp.FileSummary {
	clean := b.srv.sanitizeRecordForPrincipal(rec, auth.Principal{IsAgent: true})
	var leaseCopy *storage.FileLease
	if lease != nil {
		l := *lease
		leaseCopy = &l
	}
	return mcp.FileSummary{
		ID:           clean.ID,
		Path:         storage.LogicalPathOf(clean.FolderPath, clean.Name),
		Name:         clean.Name,
		FolderPath:   clean.FolderPath,
		Size:         clean.Size,
		Version:      clean.Version,
		VersionCount: clean.VersionCount,
		SHA256:       clean.SHA256,
		UpdateMethod: clean.UpdateMethod,
		VersionNote:  clean.VersionNote,
		UpdatedBy:    formatMCPUpdatedBy(clean),
		UpdatedAt:    clean.UploadedAt,
		AllowedUsers: append([]string(nil), clean.AllowedUsers...),
		StorageMode:  clean.StorageMode,
		MountID:      clean.MountID,
		NodeID:       clean.NodeID,
		NodeName:     clean.NodeName,
		Lease:        leaseCopy,
	}
}

func (b *serverMCPBackend) WhoAmI(ctx context.Context, p auth.Principal) (mcp.WhoAmIResult, error) {
	var scopes []string
	var readFolders, writeFolders, defaultAllowed []string
	var agentKeyID, agentName, avatarID, displayAlias, deviceAlias, deviceKind string
	var allowSetVis bool

	if p.IsAgent && p.AgentKey != nil {
		scopes = append([]string(nil), p.AgentKey.Scopes...)
		readFolders = append([]string(nil), p.AgentKey.ReadFolders...)
		writeFolders = append([]string(nil), p.AgentKey.WriteFolders...)
		defaultAllowed = append([]string(nil), p.AgentKey.DefaultAllowedUsers...)
		agentKeyID = p.AgentKey.ID
		agentName = p.AgentKey.Name
		avatarID = p.AgentKey.AvatarID
		displayAlias = p.AgentKey.DisplayAlias
		deviceAlias = p.AgentKey.DeviceAlias
		deviceKind = p.AgentKey.DeviceKind
		allowSetVis = p.AgentKey.AllowSetVisibility
	} else {
		scopes = []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeShare, auth.ScopeVerify, auth.ScopeDelete}
		readFolders = []string{"*"}
		writeFolders = []string{"*"}
		allowSetVis = true
	}

	pool := b.srv.cluster.PoolSummaryForRole(false)
	allMounts := b.srv.cluster.ListAllMounts()
	visibleMounts := make([]mcp.MountView, 0, len(allMounts))
	for _, m := range allMounts {
		if !mountVisibleToPrincipal(m.Alias, m.AddedBy, m.AllowedUsers, p) {
			continue
		}
		var presetViews []mcp.VerifyPresetView
		for _, vp := range m.VerifyPresets {
			presetViews = append(presetViews, mcp.VerifyPresetView{
				ID:         vp.ID,
				Label:      vp.Label,
				WorkSubDir: vp.WorkSubDir,
				TimeoutSec: vp.TimeoutSec,
				LogRelPath: vp.LogRelPath,
			})
		}
		visibleMounts = append(visibleMounts, mcp.MountView{
			ID:            m.ID,
			Alias:         m.Alias,
			Writable:      m.Writable,
			ShadowBackup:  m.ShadowBackup,
			FileCount:     m.FileCount,
			TotalBytes:    m.TotalBytes,
			NodeID:        m.NodeID,
			NodeName:      m.NodeName,
			IsSelf:        m.IsSelf,
			Status:        m.Status,
			VerifyPresets: presetViews,
		})
	}

	return mcp.WhoAmIResult{
		Username:            p.User.Username,
		Role:                p.User.Role,
		IsAgent:             p.IsAgent,
		ActorLabel:          p.ActorLabel(),
		AgentKeyID:          agentKeyID,
		AgentName:           agentName,
		AvatarID:            avatarID,
		DisplayAlias:        displayAlias,
		DeviceAlias:         deviceAlias,
		DeviceKind:          deviceKind,
		Scopes:              scopes,
		ReadFolders:         readFolders,
		WriteFolders:        writeFolders,
		AllowSetVisibility:  allowSetVis,
		DefaultAllowedUsers: defaultAllowed,
		NodeID:              b.srv.cluster.NodeID(),
		NodeName:            b.srv.cluster.NodeName(),
		ClusterFreeBytes:    pool.TotalFreeBytes,
		Mounts:              visibleMounts,
	}, nil
}

func (b *serverMCPBackend) ListUsers(ctx context.Context, p auth.Principal) ([]mcp.SelectableUser, error) {
	active := b.srv.auth.ListActiveUsers()
	out := make([]mcp.SelectableUser, 0, len(active))
	for _, u := range active {
		out = append(out, mcp.SelectableUser{
			Username: u.Username,
			Role:     u.Role,
		})
	}
	return out, nil
}

func relativeFolderDepth(folderPath, baseFolder string) (int, bool) {
	if baseFolder == "" {
		if folderPath == "" {
			return 0, true
		}
		return len(strings.Split(folderPath, "/")), true
	}
	if folderPath == baseFolder {
		return 0, true
	}
	if strings.HasPrefix(strings.ToLower(folderPath), strings.ToLower(baseFolder)+"/") {
		rel := strings.Trim(folderPath[len(baseFolder)+1:], "/")
		if rel == "" {
			return 0, true
		}
		return len(strings.Split(rel, "/")), true
	}
	return 0, false
}

func (b *serverMCPBackend) ListTree(ctx context.Context, p auth.Principal, in mcp.ListTreeInput) (mcp.ListTreeResult, error) {
	folderFilter := storage.SanitizeFolderPath(in.FolderPath)
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	accessible := b.srv.filterAccessibleForPrincipal(b.srv.cluster.AllClusterFiles(), p)
	globPat := strings.ToLower(strings.TrimSpace(in.Glob))
	selfID := b.srv.cluster.NodeID()

	var filtered []storage.FileRecord
	for _, rec := range accessible {
		if folderFilter != "" && !auth.FolderMatchesPrefix(rec.FolderPath, folderFilter) {
			continue
		}
		if in.Depth > 0 {
			relDepth, ok := relativeFolderDepth(rec.FolderPath, folderFilter)
			if !ok || relDepth > in.Depth {
				continue
			}
		}
		if globPat != "" {
			mName, err1 := path.Match(globPat, strings.ToLower(rec.Name))
			mFull, err2 := path.Match(globPat, strings.ToLower(storage.LogicalPathOf(rec.FolderPath, rec.Name)))
			if (err1 != nil && err2 != nil) || (!mName && !mFull) {
				continue
			}
		}
		filtered = append(filtered, rec)
	}

	sort.Slice(filtered, func(i, j int) bool {
		pi := strings.ToLower(storage.LogicalPathOf(filtered[i].FolderPath, filtered[i].Name))
		pj := strings.ToLower(storage.LogicalPathOf(filtered[j].FolderPath, filtered[j].Name))
		if pi != pj {
			return pi < pj
		}
		return filtered[i].ID < filtered[j].ID
	})

	total := len(filtered)
	truncated := total > limit
	if truncated {
		filtered = filtered[:limit]
	}

	entries := make([]mcp.TreeEntry, 0, len(filtered))
	for _, rec := range filtered {
		entry := mcp.TreeEntry{
			FileID:      rec.ID,
			Path:        storage.LogicalPathOf(rec.FolderPath, rec.Name),
			Name:        rec.Name,
			FolderPath:  rec.FolderPath,
			Size:        rec.Size,
			Version:     rec.Version,
			UpdatedBy:   formatMCPUpdatedBy(rec),
			UpdatedAt:   rec.UploadedAt,
			StorageMode: rec.StorageMode,
			NodeID:      rec.NodeID,
			NodeName:    rec.NodeName,
		}
		if rec.NodeID == "" || rec.NodeID == selfID {
			if lease, locked := b.srv.store.GetLease(rec.ID); locked {
				entry.LockedBy = lease.HolderName
				exp := lease.ExpiresAt
				entry.LockExpiresAt = &exp
			}
		}
		entries = append(entries, entry)
	}

	return mcp.ListTreeResult{
		FolderPath:   folderFilter,
		TotalMatched: total,
		Truncated:    truncated,
		Entries:      entries,
	}, nil
}

func (b *serverMCPBackend) SearchFiles(ctx context.Context, p auth.Principal, in mcp.SearchFilesInput) (mcp.SearchFilesResult, error) {
	nameQ := strings.TrimSpace(in.NameQuery)
	contentQ := strings.TrimSpace(in.ContentQuery)
	if nameQ == "" && contentQ == "" {
		return mcp.SearchFilesResult{}, mcp.NewToolError("invalid_params", "请至少提供 name_query 或 content_query", "", nil)
	}

	folderFilter := storage.SanitizeFolderPath(in.FolderPath)
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	needleName := nameQ
	needleContent := contentQ
	if !in.CaseSensitive {
		needleName = strings.ToLower(nameQ)
		needleContent = strings.ToLower(contentQ)
	}

	accessible := b.srv.filterAccessibleForPrincipal(b.srv.cluster.AllClusterFiles(), p)
	sort.Slice(accessible, func(i, j int) bool {
		pi := strings.ToLower(storage.LogicalPathOf(accessible[i].FolderPath, accessible[i].Name))
		pj := strings.ToLower(storage.LogicalPathOf(accessible[j].FolderPath, accessible[j].Name))
		return pi < pj
	})

	const (
		maxContentFileBytes  int64 = 2 << 20
		maxContentFilesCount       = 500
		maxContentTotalBytes int64 = 50 << 20
		maxMatchesPerFile          = 5
	)

	var (
		results      []mcp.SearchFileMatch
		totalMatched int
		scannedFiles int
		scannedBytes int64
		truncated    bool
	)

	for _, rec := range accessible {
		if ctx.Err() != nil {
			break
		}
		if folderFilter != "" && !auth.FolderMatchesPrefix(rec.FolderPath, folderFilter) {
			continue
		}
		fullPath := storage.LogicalPathOf(rec.FolderPath, rec.Name)
		if nameQ != "" {
			hayPath := fullPath
			if !in.CaseSensitive {
				hayPath = strings.ToLower(fullPath)
			}
			if !strings.Contains(hayPath, needleName) {
				continue
			}
		}

		if contentQ == "" {
			totalMatched++
			if len(results) < limit {
				results = append(results, mcp.SearchFileMatch{
					FileID:  rec.ID,
					Path:    fullPath,
					Size:    rec.Size,
					Version: rec.Version,
				})
			} else {
				truncated = true
			}
			continue
		}

		if rec.Size > maxContentFileBytes {
			continue
		}
		if scannedFiles >= maxContentFilesCount || scannedBytes >= maxContentTotalBytes {
			truncated = true
			break
		}

		_, localPath, peer, isLocal, status, _ := b.srv.findAccessibleFileForPrincipal(rec.ID, p)
		if status != http.StatusOK {
			continue
		}
		raw, err := b.srv.readFileBytesForPrincipal(rec, localPath, peer, isLocal, maxContentFileBytes)
		if err != nil || storage.IsLikelyBinaryBytes(raw) {
			continue
		}
		scannedFiles++
		scannedBytes += int64(len(raw))

		norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
		var lines []string
		if norm != "" {
			lines = strings.Split(strings.TrimSuffix(norm, "\n"), "\n")
		}
		var matches []mcp.SearchLineMatch
		for idx, lineText := range lines {
			hay := lineText
			if !in.CaseSensitive {
				hay = strings.ToLower(lineText)
			}
			if strings.Contains(hay, needleContent) {
				runes := []rune(lineText)
				if len(runes) > 240 {
					lineText = string(runes[:240]) + "..."
				}
				matches = append(matches, mcp.SearchLineMatch{
					Line: idx + 1,
					Text: lineText,
				})
				if len(matches) >= maxMatchesPerFile {
					break
				}
			}
		}
		if len(matches) == 0 {
			continue
		}

		totalMatched++
		if len(results) < limit {
			results = append(results, mcp.SearchFileMatch{
				FileID:  rec.ID,
				Path:    fullPath,
				Size:    rec.Size,
				Version: rec.Version,
				Matches: matches,
			})
		} else {
			truncated = true
		}
	}

	return mcp.SearchFilesResult{
		TotalMatched:        totalMatched,
		ScannedContentFiles: scannedFiles,
		Truncated:           truncated,
		Results:             results,
	}, nil
}

func (b *serverMCPBackend) ReadFile(ctx context.Context, p auth.Principal, in mcp.ReadFileInput) (mcp.ReadFileResult, error) {
	rec, localPath, peer, isLocal, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return mcp.ReadFileResult{}, statusErrToMCP(status, errMsg)
	}
	pathLabel := storage.LogicalPathOf(rec.FolderPath, rec.Name)

	var leasePtr *storage.FileLease
	if isLocal {
		if lease, locked := b.srv.store.GetLease(rec.ID); locked {
			leasePtr = &lease
		}
	} else if lease, locked, lErr := b.srv.cluster.ProxyGetLease(peer, rec.ID); lErr == nil && locked {
		leasePtr = &lease
	}

	ifNoneMatch := strings.Trim(strings.TrimSpace(strings.TrimPrefix(in.IfNoneMatch, "W/")), `"`)
	if ifNoneMatch != "" && rec.SHA256 != "" && strings.EqualFold(ifNoneMatch, rec.SHA256) {
		return mcp.ReadFileResult{
			FileID:      rec.ID,
			Path:        pathLabel,
			Version:     rec.Version,
			SHA256:      rec.SHA256,
			Size:        rec.Size,
			NotModified: true,
			UpdatedBy:   formatMCPUpdatedBy(rec),
			UpdatedAt:   rec.UploadedAt,
			Lease:       leasePtr,
		}, nil
	}

	const maxReadBytes int64 = 4 << 20
	raw, err := b.srv.readFileBytesForPrincipal(rec, localPath, peer, isLocal, maxReadBytes)
	if err != nil {
		return mcp.ReadFileResult{}, err
	}
	if storage.IsLikelyBinaryBytes(raw) {
		return mcp.ReadFileResult{}, mcp.NewToolError("binary_file", "检测到二进制文件，请改用下载接口获取", "", nil)
	}

	shaHex := rec.SHA256
	if shaHex == "" {
		sum := sha256.Sum256(raw)
		shaHex = hex.EncodeToString(sum[:])
	}
	if ifNoneMatch != "" && strings.EqualFold(ifNoneMatch, shaHex) {
		return mcp.ReadFileResult{
			FileID:      rec.ID,
			Path:        pathLabel,
			Version:     rec.Version,
			SHA256:      shaHex,
			Size:        rec.Size,
			NotModified: true,
			UpdatedBy:   formatMCPUpdatedBy(rec),
			UpdatedAt:   rec.UploadedAt,
			Lease:       leasePtr,
		}, nil
	}

	norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var lines []string
	if norm != "" {
		lines = strings.Split(strings.TrimSuffix(norm, "\n"), "\n")
	}
	totalLines := len(lines)

	startLine := in.StartLine
	endLine := in.EndLine
	if startLine <= 0 && endLine <= 0 {
		outEnd := totalLines
		outStart := 1
		if totalLines == 0 {
			outStart = 0
		}
		return mcp.ReadFileResult{
			FileID:     rec.ID,
			Path:       pathLabel,
			Version:    rec.Version,
			SHA256:     shaHex,
			Size:       rec.Size,
			Content:    string(raw),
			TotalLines: totalLines,
			StartLine:  outStart,
			EndLine:    outEnd,
			Truncated:  false,
			UpdatedBy:  formatMCPUpdatedBy(rec),
			UpdatedAt:  rec.UploadedAt,
			Lease:      leasePtr,
		}, nil
	}

	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > totalLines {
		endLine = totalLines
	}
	if totalLines == 0 || startLine > totalLines || startLine > endLine {
		return mcp.ReadFileResult{
			FileID:     rec.ID,
			Path:       pathLabel,
			Version:    rec.Version,
			SHA256:     shaHex,
			Size:       rec.Size,
			Content:    "",
			TotalLines: totalLines,
			StartLine:  startLine,
			EndLine:    endLine,
			Truncated:  false,
			UpdatedBy:  formatMCPUpdatedBy(rec),
			UpdatedAt:  rec.UploadedAt,
			Lease:      leasePtr,
		}, nil
	}

	sliced := strings.Join(lines[startLine-1:endLine], "\n")
	if endLine == totalLines && strings.HasSuffix(norm, "\n") {
		sliced += "\n"
	}
	return mcp.ReadFileResult{
		FileID:     rec.ID,
		Path:       pathLabel,
		Version:    rec.Version,
		SHA256:     shaHex,
		Size:       rec.Size,
		Content:    sliced,
		TotalLines: totalLines,
		StartLine:  startLine,
		EndLine:    endLine,
		Truncated:  endLine < totalLines,
		UpdatedBy:  formatMCPUpdatedBy(rec),
		UpdatedAt:  rec.UploadedAt,
		Lease:      leasePtr,
	}, nil
}

func (b *serverMCPBackend) WriteFile(ctx context.Context, r *http.Request, p auth.Principal, in mcp.WriteFileInput) (mcp.FileSummary, error) {
	folderPath, fileName, err := storage.SplitLogicalPath(in.Path)
	if err != nil {
		return mcp.FileSummary{}, err
	}
	if p.IsAgent && !p.CanWriteFolder(folderPath) {
		return mcp.FileSummary{}, mcp.NewToolError("forbidden", "Agent 密钥无权写入目录: "+folderPath, "", nil)
	}
	if in.ExpectedVersion != nil && *in.ExpectedVersion < 0 {
		return mcp.FileSummary{}, mcp.NewToolError("invalid_params", "expected_version 不能为负数", "", nil)
	}
	if err := b.srv.auth.CheckUploadAllowed(fileName); err != nil {
		return mcp.FileSummary{}, err
	}
	candidates := in.AllowedUsers
	if p.IsAgent {
		resolved, rErr := p.ResolveAllowedUsers(candidates)
		if rErr != nil {
			return mcp.FileSummary{}, mcp.NewToolError("forbidden", rErr.Error(), "", nil)
		}
		candidates = resolved
	}
	cleanUsers, uErr := b.srv.auth.ValidateExistingUsers(candidates)
	if uErr != nil {
		return mcp.FileSummary{}, uErr
	}

	dev := extractDeviceInfoForPrincipal(r, p)
	conc := concurrencyOptionsForPrincipal(p, in.ExpectedVersion, false)
	rec, status, saveErr := b.srv.dispatchUploadStreamWithConcurrency(
		p,
		fileName,
		folderPath,
		strings.TrimSpace(in.Note),
		int64(len(in.Content)),
		"auto",
		cleanUsers,
		dev,
		strings.NewReader(in.Content),
		conc,
	)
	if saveErr != nil {
		return mcp.FileSummary{}, mapStorageErrForMCP(status, saveErr)
	}

	if p.IsAgent {
		b.srv.logAudit(r, p.User.Username, "agent_mcp_write", rec.Name, "ok", fmt.Sprintf("%s v%d", p.ActorLabel(), rec.Version))
	}
	b.srv.publishRecordChangeWithActor("upload", p.User.Username, p.ActorLabel(), []storage.FileRecord{rec}, rec.AllowedUsers)
	return b.toFileSummary(rec, nil), nil
}

func (b *serverMCPBackend) PatchFile(ctx context.Context, r *http.Request, p auth.Principal, in mcp.PatchFileInput) (mcp.FileSummary, error) {
	rec, _, _, _, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return mcp.FileSummary{}, statusErrToMCP(status, errMsg)
	}
	method := storage.MethodPatch
	if p.IsAgent {
		method = storage.MethodAgentPatch
	}
	updated, pStatus, err := b.srv.patchFileForPrincipal(r, p, rec.ID, storage.PatchRequest{
		Mode:            in.Mode,
		Hunks:           in.Hunks,
		UnifiedDiff:     in.UnifiedDiff,
		ExpectedVersion: in.ExpectedVersion,
		Note:            in.Note,
	}, method, in.ExpectedVersion, false)
	if err != nil {
		return mcp.FileSummary{}, mapStorageErrForMCP(pStatus, err)
	}
	return b.toFileSummary(updated, nil), nil
}

func (b *serverMCPBackend) BatchCommit(ctx context.Context, r *http.Request, p auth.Principal, in mcp.BatchCommitInput) (storage.BatchCommitResult, error) {
	res, status, err := b.srv.batchCommitForPrincipal(r, p, storage.BatchCommitRequest{
		Note:       in.Note,
		Operations: in.Operations,
	}, false)
	if err != nil {
		return storage.BatchCommitResult{}, mapStorageErrForMCP(status, err)
	}
	for i := range res.Results {
		if res.Results[i].Record != nil {
			clean := b.srv.sanitizeRecordForPrincipal(*res.Results[i].Record, auth.Principal{IsAgent: true})
			res.Results[i].Record = &clean
		}
	}
	return res, nil
}

func (b *serverMCPBackend) DeleteFile(ctx context.Context, r *http.Request, p auth.Principal, in mcp.DeleteFileInput) (mcp.DeleteFileResult, error) {
	rec, _, peer, isLocal, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return mcp.DeleteFileResult{}, statusErrToMCP(status, errMsg)
	}
	if p.User.Role != "admin" && rec.Uploader != p.User.Username {
		return mcp.DeleteFileResult{}, mcp.NewToolError("forbidden", "仅上传者或管理员可删除该文件", "", nil)
	}
	if p.IsAgent && !p.CanDeleteFolder(rec.FolderPath) {
		return mcp.DeleteFileResult{}, mcp.NewToolError("forbidden", "Agent 密钥无权删除该目录文件", "", nil)
	}
	if in.ExpectedVersion != nil && *in.ExpectedVersion < 0 {
		return mcp.DeleteFileResult{}, mcp.NewToolError("invalid_params", "expected_version 不能为负数", "", nil)
	}
	conc := concurrencyOptionsForPrincipal(p, in.ExpectedVersion, false)

	var err error
	nodeName := b.srv.cluster.NodeName()
	if isLocal {
		err = b.srv.store.DeleteWithConcurrency(rec.ID, conc)
	} else {
		if !peer.Online {
			return mcp.DeleteFileResult{}, mcp.NewToolError("node_offline", "文件所在存储节点当前离线", "", nil)
		}
		nodeName = rec.NodeName
		err = b.srv.cluster.ProxyDeleteWithConcurrency(peer, rec.ID, conc)
	}
	if err != nil {
		return mcp.DeleteFileResult{}, mapStorageErrForMCP(http.StatusBadRequest, err)
	}

	action := "file_delete"
	detail := nodeName
	if p.IsAgent {
		action = "agent_file_delete"
		detail = p.ActorLabel() + " " + nodeName
	}
	b.srv.logAudit(r, p.User.Username, action, rec.Name, "ok", detail)
	b.srv.events.Publish(FileChangeEvent{
		Kind:         "delete",
		Uploader:     rec.Uploader,
		ActorName:    p.ActorLabel(),
		Count:        1,
		FileNames:    []string{rec.Name},
		FolderPaths:  []string{storage.SanitizeFolderPath(rec.FolderPath)},
		NodeName:     nodeName,
		AllowedUsers: rec.AllowedUsers,
	})
	return mcp.DeleteFileResult{
		OK:     true,
		FileID: rec.ID,
		Path:   storage.LogicalPathOf(rec.FolderPath, rec.Name),
	}, nil
}

func (b *serverMCPBackend) SetFileVisibility(ctx context.Context, r *http.Request, p auth.Principal, in mcp.SetVisibilityInput) (mcp.FileSummary, error) {
	rec, _, _, _, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return mcp.FileSummary{}, statusErrToMCP(status, errMsg)
	}
	if p.User.Role != "admin" && rec.Uploader != p.User.Username {
		return mcp.FileSummary{}, mcp.NewToolError("forbidden", "仅上传者或管理员可修改文件可见权限", "", nil)
	}
	if p.IsAgent && !p.CanShareFolder(rec.FolderPath) {
		return mcp.FileSummary{}, mcp.NewToolError("forbidden", "当前 Agent 密钥无权修改该文件的可见成员", "", nil)
	}

	candidates := in.AllowedUsers
	if p.IsAgent && len(storage.NormalizeAllowedUsers(candidates)) > 0 {
		resolved, rErr := p.ResolveAllowedUsers(candidates)
		if rErr != nil {
			return mcp.FileSummary{}, mcp.NewToolError("forbidden", rErr.Error(), "", nil)
		}
		candidates = resolved
	}
	cleanUsers, err := b.srv.auth.ValidateExistingUsers(candidates)
	if err != nil {
		return mcp.FileSummary{}, err
	}
	updated, err := b.srv.cluster.UpdateFileAccess(rec.ID, cleanUsers)
	if err != nil {
		return mcp.FileSummary{}, err
	}
	detail := strings.Join(cleanUsers, ",")
	if p.IsAgent {
		detail = strings.TrimSpace(p.ActorLabel() + " " + detail)
	}
	b.srv.logAudit(r, p.User.Username, "file_access", rec.Name, "ok", detail)
	b.srv.events.Publish(FileChangeEvent{
		Kind:        "access",
		Uploader:    p.User.Username,
		ActorName:   p.ActorLabel(),
		Count:       1,
		FolderPaths: []string{storage.SanitizeFolderPath(rec.FolderPath)},
	})
	return b.toFileSummary(updated, nil), nil
}

func (b *serverMCPBackend) AcquireLease(ctx context.Context, p auth.Principal, in mcp.AcquireLeaseInput) (storage.FileLease, error) {
	rec, _, peer, isLocal, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return storage.FileLease{}, statusErrToMCP(status, errMsg)
	}
	if p.IsAgent && !p.CanWriteFolder(rec.FolderPath) {
		return storage.FileLease{}, mcp.NewToolError("forbidden", "Agent 密钥无权锁定该目录文件", "", nil)
	}
	holderID, holderName := principalActorKeyID(p)
	if isLocal {
		return b.srv.store.AcquireLease(rec.ID, holderID, holderName, time.Duration(in.TTLSeconds)*time.Second, false)
	}
	if !peer.Online {
		return storage.FileLease{}, mcp.NewToolError("node_offline", "文件所在存储节点当前离线", "", nil)
	}
	return b.srv.cluster.ProxyAcquireLease(peer, rec.ID, holderID, holderName, in.TTLSeconds, false)
}

func (b *serverMCPBackend) ReleaseLease(ctx context.Context, p auth.Principal, in mcp.ReleaseLeaseInput) error {
	rec, _, peer, isLocal, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return statusErrToMCP(status, errMsg)
	}
	if p.IsAgent && !p.CanWriteFolder(rec.FolderPath) {
		return mcp.NewToolError("forbidden", "Agent 密钥无权释放该目录文件租约", "", nil)
	}
	holderID, _ := principalActorKeyID(p)
	if isLocal {
		return b.srv.store.ReleaseLease(rec.ID, holderID, false)
	}
	if !peer.Online {
		return mcp.NewToolError("node_offline", "文件所在存储节点当前离线", "", nil)
	}
	return b.srv.cluster.ProxyReleaseLease(peer, rec.ID, holderID, false)
}

func (b *serverMCPBackend) GetFileVersions(ctx context.Context, p auth.Principal, in mcp.FileLocatorInput) (mcp.FileVersionsResult, error) {
	rec, _, _, _, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return mcp.FileVersionsResult{}, statusErrToMCP(status, errMsg)
	}
	clean := b.srv.sanitizeRecordForPrincipal(rec, p)
	curVer := clean.Version
	if curVer <= 0 {
		curVer = 1
	}
	versions := make([]mcp.VersionInfo, 0, 1+len(clean.Versions))
	versions = append(versions, mcp.VersionInfo{
		Version:      curVer,
		Size:         clean.Size,
		Uploader:     clean.Uploader,
		UploadedAt:   clean.UploadedAt,
		DeviceName:   clean.DeviceName,
		SHA256:       clean.SHA256,
		Note:         clean.VersionNote,
		UpdateMethod: storage.DefaultUpdateMethod(clean.UpdateMethod, curVer),
	})
	for _, v := range clean.Versions {
		versions = append(versions, mcp.VersionInfo{
			Version:      v.Version,
			Size:         v.Size,
			Uploader:     v.Uploader,
			UploadedAt:   v.UploadedAt,
			DeviceName:   v.DeviceName,
			SHA256:       v.SHA256,
			Note:         v.Note,
			UpdateMethod: storage.DefaultUpdateMethod(v.UpdateMethod, v.Version),
		})
	}
	return mcp.FileVersionsResult{
		FileID:         clean.ID,
		Path:           storage.LogicalPathOf(clean.FolderPath, clean.Name),
		CurrentVersion: curVer,
		VersionCount:   len(versions),
		Versions:       versions,
	}, nil
}

func (b *serverMCPBackend) RestoreFileVersion(ctx context.Context, r *http.Request, p auth.Principal, in mcp.RestoreVersionInput) (mcp.FileSummary, error) {
	rec, _, peer, isLocal, status, errMsg := b.srv.resolveFileByIDOrPath(in.FileID, in.Path, p)
	if status != http.StatusOK {
		return mcp.FileSummary{}, statusErrToMCP(status, errMsg)
	}
	if p.IsAgent && !p.CanWriteFolder(rec.FolderPath) {
		return mcp.FileSummary{}, mcp.NewToolError("forbidden", "Agent 密钥无权回滚该目录文件版本", "", nil)
	}
	if in.ExpectedVersion != nil && *in.ExpectedVersion < 0 {
		return mcp.FileSummary{}, mcp.NewToolError("invalid_params", "expected_version 不能为负数", "", nil)
	}
	conc := concurrencyOptionsForPrincipal(p, in.ExpectedVersion, false)
	dev := extractDeviceInfoForPrincipal(r, p)
	note := strings.TrimSpace(in.Note)

	var (
		updated storage.FileRecord
		err     error
	)
	if isLocal {
		updated, err = b.srv.store.RestoreVersionWithConcurrency(rec.ID, in.Version, note, p.User.Username, dev, conc)
		if err != nil {
			return mcp.FileSummary{}, mapStorageErrForMCP(http.StatusBadRequest, err)
		}
		updated.NodeID = b.srv.cluster.NodeID()
		updated.NodeName = b.srv.cluster.NodeName()
		updated.NodeOS = runtime.GOOS
		updated.NodeOnline = true
	} else {
		if !peer.Online {
			return mcp.FileSummary{}, mcp.NewToolError("node_offline", "文件所在存储节点当前离线", "", nil)
		}
		updated, err = b.srv.cluster.ProxyRestoreVersionWithConcurrency(peer, rec.ID, in.Version, note, p.User.Username, dev, conc)
		if err != nil {
			return mcp.FileSummary{}, mapStorageErrForMCP(http.StatusBadGateway, err)
		}
	}

	if p.IsAgent {
		b.srv.logAudit(r, p.User.Username, "agent_file_version_restore", updated.Name, "ok", fmt.Sprintf("%s -> v%d (now v%d)", p.ActorLabel(), in.Version, updated.Version))
	}
	b.srv.publishRecordChangeWithActor("upload", p.User.Username, p.ActorLabel(), []storage.FileRecord{updated}, rec.AllowedUsers)
	return b.toFileSummary(updated, nil), nil
}

func eventVisibleForWait(ev FileChangeEvent, p auth.Principal, prefix string) bool {
	if len(ev.AllowedUsers) > 0 && !storage.CanUserAccess(storage.FileRecord{Uploader: ev.Uploader, AllowedUsers: ev.AllowedUsers}, p.User.Username, p.User.Role) {
		return false
	}
	if len(ev.FolderPaths) == 0 {
		return false
	}
	if p.IsAgent {
		for _, fp := range ev.FolderPaths {
			if !p.CanReadFolder(fp) {
				return false
			}
		}
	}
	if prefix != "" {
		matchedPrefix := false
		for _, fp := range ev.FolderPaths {
			if auth.FolderMatchesPrefix(fp, prefix) {
				matchedPrefix = true
				break
			}
		}
		if !matchedPrefix {
			return false
		}
	}
	return true
}

func toMCPChangeEvent(ev FileChangeEvent) mcp.ChangeEvent {
	return mcp.ChangeEvent{
		Version:     ev.Version,
		Kind:        ev.Kind,
		Uploader:    ev.Uploader,
		ActorName:   ev.ActorName,
		Count:       ev.Count,
		FileNames:   append([]string(nil), ev.FileNames...),
		FolderPaths: append([]string(nil), ev.FolderPaths...),
		BatchID:     ev.BatchID,
		NodeName:    ev.NodeName,
		Timestamp:   ev.Timestamp,
	}
}

func (b *serverMCPBackend) WaitForChanges(ctx context.Context, p auth.Principal, in mcp.WaitChangesInput) (mcp.WaitChangesResult, error) {
	prefix := storage.SanitizeFolderPath(in.FolderPrefix)
	if prefix != "" && p.IsAgent && !p.CanReadFolder(prefix) && !hasReadableSubpath(p, prefix) {
		return mcp.WaitChangesResult{}, mcp.NewToolError("forbidden", "Agent 密钥无权监听该目录变更", "", nil)
	}

	timeoutSec := in.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 15
	}
	if timeoutSec > 30 {
		timeoutSec = 30
	}

	// Subscribe BEFORE checking backlog so events published concurrently are never missed.
	ch, unsub := b.srv.events.Subscribe()
	defer unsub()

	if in.SinceVersion > 0 {
		backlog, curVer, gap := b.srv.events.EventsSince(in.SinceVersion)
		var out []mcp.ChangeEvent
		for _, ev := range backlog {
			if eventVisibleForWait(ev, p, prefix) {
				out = append(out, toMCPChangeEvent(ev))
			}
		}
		if len(out) > 0 || gap {
			return mcp.WaitChangesResult{
				CurrentVersion: curVer,
				Gap:            gap,
				Events:         out,
			}, nil
		}
	}

	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()
	doneCh := b.srv.events.Done()

	for {
		select {
		case <-ctx.Done():
			return mcp.WaitChangesResult{
				CurrentVersion: b.srv.events.Version(),
				TimedOut:       true,
			}, nil
		case <-doneCh:
			return mcp.WaitChangesResult{
				CurrentVersion: b.srv.events.Version(),
				TimedOut:       true,
			}, nil
		case <-timer.C:
			return mcp.WaitChangesResult{
				CurrentVersion: b.srv.events.Version(),
				TimedOut:       true,
			}, nil
		case ev := <-ch:
			if !eventVisibleForWait(ev, p, prefix) {
				continue
			}
			out := []mcp.ChangeEvent{toMCPChangeEvent(ev)}
			latestVer := ev.Version
			for {
				select {
				case nextEv := <-ch:
					if nextEv.Version > latestVer {
						latestVer = nextEv.Version
					}
					if eventVisibleForWait(nextEv, p, prefix) {
						out = append(out, toMCPChangeEvent(nextEv))
					}
				default:
					return mcp.WaitChangesResult{
						CurrentVersion: latestVer,
						Events:         out,
					}, nil
				}
			}
		}
	}
}

func statusErrToMCP(status int, msg string) error {
	switch status {
	case http.StatusForbidden:
		return mcp.NewToolError("forbidden", msg, "", nil)
	case http.StatusNotFound:
		return mcp.NewToolError("not_found", msg, "", nil)
	case http.StatusServiceUnavailable:
		return mcp.NewToolError("node_offline", msg, "", nil)
	case http.StatusInsufficientStorage:
		return mcp.NewToolError("insufficient_storage", msg, "", nil)
	default:
		return mcp.NewToolError("invalid_params", msg, "", nil)
	}
}

func mapStorageErrForMCP(status int, err error) error {
	var conflict *storage.ConflictError
	var leaseErr *storage.LeaseLockedError
	var patchErr *storage.PatchError
	if errors.As(err, &conflict) || errors.As(err, &leaseErr) || errors.As(err, &patchErr) {
		return err
	}
	if status == http.StatusForbidden {
		return mcp.NewToolError("forbidden", err.Error(), "", nil)
	}
	if status == http.StatusNotFound {
		return mcp.NewToolError("not_found", err.Error(), "", nil)
	}
	if status == http.StatusServiceUnavailable {
		return mcp.NewToolError("node_offline", err.Error(), "", nil)
	}
	if status == http.StatusInsufficientStorage {
		return mcp.NewToolError("insufficient_storage", err.Error(), "", nil)
	}
	return err
}

func (b *serverMCPBackend) IssueTransferTicket(ctx context.Context, r *http.Request, p auth.Principal, in mcp.IssueTicketInput) (mcp.IssueTicketResult, error) {
	req := IssueTicketRequest{
		Action:       in.Action,
		FileID:       in.FileID,
		Path:         in.Path,
		FolderPath:   in.FolderPath,
		FileName:     in.FileName,
		AllowedUsers: in.AllowedUsers,
		MaxBytes:     in.MaxBytes,
		TargetNode:   in.TargetNode,
		TTLSeconds:   in.TTLSeconds,
		PreferDirect: in.PreferDirect,
	}
	resp, status, err := b.srv.issueTransferTicketForPrincipal(r, p, req)
	if err != nil {
		return mcp.IssueTicketResult{}, statusErrToMCP(status, err.Error())
	}
	return mcp.IssueTicketResult{
		Action:         resp.Action,
		NodeID:         resp.NodeID,
		NodeName:       resp.NodeName,
		DirectURL:      resp.DirectURL,
		EntryURL:       resp.EntryURL,
		RecommendedURL: resp.RecommendedURL,
		ExpiresAt:      resp.ExpiresAt,
		MaxBytes:       resp.MaxBytes,
		SingleUse:      resp.SingleUse,
		Method:         resp.Method,
	}, nil
}

func (b *serverMCPBackend) TriggerWorkspaceVerify(ctx context.Context, p auth.Principal, in mcp.TriggerVerifyInput) (bridge.VerifyResult, error) {
	res, status, err := b.srv.triggerWorkspaceVerifyForPrincipal(ctx, p, in.MountID, in.PresetID)
	if err != nil {
		return bridge.VerifyResult{}, statusErrToMCP(status, err.Error())
	}
	return res, nil
}

func (b *serverMCPBackend) ListTasks(ctx context.Context, p auth.Principal, in bridge.TaskListFilter) (bridge.TaskListResult, error) {
	if strings.TrimSpace(in.AssignedAgent) != "" {
		if pub, ok := b.srv.auth.ResolveAgentKeyRef(in.AssignedAgent); ok {
			in.AssignedAgent = pub.ID
		}
	}
	return b.srv.getTasks().ListTasks(taskActorFromPrincipal(p), in), nil
}

func (b *serverMCPBackend) ClaimTask(ctx context.Context, p auth.Principal, in bridge.ClaimTaskInput) (bridge.AgentTask, error) {
	return b.srv.getTasks().ClaimTask(taskActorFromPrincipal(p), in)
}

func (b *serverMCPBackend) HeartbeatTask(ctx context.Context, p auth.Principal, in mcp.HeartbeatTaskInput) (bridge.AgentTask, error) {
	return b.srv.getTasks().HeartbeatTask(taskActorFromPrincipal(p), bridge.ClaimTaskInput{
		TaskID:       in.TaskID,
		SubtaskID:    in.SubtaskID,
		LeaseSeconds: in.LeaseSeconds,
	})
}

func (b *serverMCPBackend) SplitTask(ctx context.Context, p auth.Principal, in bridge.SplitTaskInput) (bridge.AgentTask, error) {
	for i := range in.Subtasks {
		if strings.TrimSpace(in.Subtasks[i].AssignedAgent) == "" {
			continue
		}
		id, lbl, err := b.srv.resolveExplicitAgentTarget(in.Subtasks[i].AssignedAgent)
		if err != nil {
			return bridge.AgentTask{}, mcp.NewToolError("invalid_params", err.Error(), "", nil)
		}
		in.Subtasks[i].AssignedAgent = id
		if in.Subtasks[i].AssignedAgentLabel == "" {
			in.Subtasks[i].AssignedAgentLabel = lbl
		}
	}
	return b.srv.getTasks().SplitTask(taskActorFromPrincipal(p), in)
}

func (b *serverMCPBackend) PostTaskComment(ctx context.Context, p auth.Principal, in bridge.PostCommentInput) (mcp.TaskCommentResult, error) {
	task, cmt, err := b.srv.getTasks().PostComment(taskActorFromPrincipal(p), in)
	if err != nil {
		return mcp.TaskCommentResult{}, err
	}
	return mcp.TaskCommentResult{Task: task, Comment: cmt}, nil
}

func (b *serverMCPBackend) CompleteTask(ctx context.Context, p auth.Principal, in bridge.CompleteTaskInput) (bridge.AgentTask, error) {
	return b.srv.getTasks().CompleteTask(taskActorFromPrincipal(p), in)
}

func (b *serverMCPBackend) ReleaseTask(ctx context.Context, p auth.Principal, in bridge.ReleaseTaskInput) (bridge.AgentTask, error) {
	if strings.TrimSpace(in.ReassignAgent) != "" {
		id, lbl, err := b.srv.resolveExplicitAgentTarget(in.ReassignAgent)
		if err != nil {
			return bridge.AgentTask{}, mcp.NewToolError("invalid_params", err.Error(), "", nil)
		}
		in.ReassignAgent = id
		if in.ReassignAgentLabel == "" {
			in.ReassignAgentLabel = lbl
		}
	}
	return b.srv.getTasks().ReleaseTask(taskActorFromPrincipal(p), in)
}

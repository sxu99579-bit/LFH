package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

func (s *Server) resolveFileByIDOrPath(fileID, logicalPath string, p auth.Principal) (storage.FileRecord, string, cluster.NodeStatus, bool, int, string) {
	id := strings.TrimSpace(fileID)
	if id != "" {
		return s.findAccessibleFileForPrincipal(id, p)
	}
	folderPath, fileName, err := storage.SplitLogicalPath(logicalPath)
	if err != nil {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusBadRequest, err.Error()
	}
	if p.IsAgent && !p.CanReadFolder(folderPath) {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusForbidden, "Agent 密钥无权访问该目录"
	}

	_, _, _, inMount := s.cluster.FindMountByFolderPath(folderPath)
	var matched *storage.FileRecord
	for _, rec := range s.cluster.AllClusterFiles() {
		if rec.FolderPath != folderPath || !strings.EqualFold(rec.Name, fileName) {
			continue
		}
		if !storage.CanUserAccess(rec, p.User.Username, p.User.Role) {
			continue
		}
		cp := rec
		if inMount && rec.StorageMode == storage.StorageModeMount {
			matched = &cp
			break
		}
		if matched == nil {
			matched = &cp
		}
	}
	if matched == nil {
		return storage.FileRecord{}, "", cluster.NodeStatus{}, false, http.StatusNotFound, "文件不存在: " + storage.LogicalPathOf(folderPath, fileName)
	}
	return s.findAccessibleFileForPrincipal(matched.ID, p)
}

func (s *Server) readFileBytesForPrincipal(rec storage.FileRecord, localPath string, peer cluster.NodeStatus, isLocal bool, maxBytes int64) ([]byte, error) {
	if isLocal {
		fi, err := os.Stat(localPath)
		if err != nil {
			s.store.RecordReadError()
			if isExternalSourceMissingErr(rec, err) {
				return nil, errors.New("源文件已移动或删除，外部引用已失效")
			}
			return nil, err
		}
		if maxBytes > 0 && fi.Size() > maxBytes {
			return nil, fmt.Errorf("文件大小超过文本读取上限 (%d 字节)", maxBytes)
		}
		f, err := os.Open(localPath)
		if err != nil {
			s.store.RecordReadError()
			return nil, err
		}
		defer f.Close()

		var data []byte
		if maxBytes > 0 {
			data, err = io.ReadAll(io.LimitReader(f, maxBytes+1))
			if err == nil && int64(len(data)) > maxBytes {
				return nil, fmt.Errorf("文件大小超过文本读取上限 (%d 字节)", maxBytes)
			}
		} else {
			data, err = io.ReadAll(f)
		}
		if err != nil {
			s.store.RecordReadError()
			return nil, err
		}
		if len(data) > 0 {
			s.store.AddDownloadBytes(int64(len(data)))
		}
		return data, nil
	}

	if !peer.Online {
		return nil, errors.New("文件所在存储节点当前离线")
	}
	return s.cluster.ReadPeerFileBytes(peer, rec.ID, maxBytes)
}

func (s *Server) patchFileForPrincipal(
	r *http.Request,
	p auth.Principal,
	id string,
	req storage.PatchRequest,
	method string,
	expectedVer *int,
	forceLease bool,
) (storage.FileRecord, int, error) {
	rec, _, peer, isLocal, status, errMsg := s.findAccessibleFileForPrincipal(id, p)
	if status != http.StatusOK {
		return storage.FileRecord{}, status, errors.New(errMsg)
	}
	if p.IsAgent && !p.CanWriteFolder(rec.FolderPath) {
		return storage.FileRecord{}, http.StatusForbidden, errors.New("Agent 密钥无权修改该目录文件")
	}
	if !p.User.EffectiveCanUpload() || !p.User.CanAccessFolder(rec.FolderPath) {
		return storage.FileRecord{}, http.StatusForbidden, errors.New("当前账号为只读权限或无权修改该目录文件")
	}

	effExpected := expectedVer
	if effExpected == nil {
		effExpected = req.ExpectedVersion
	}
	if effExpected != nil && *effExpected < 0 {
		return storage.FileRecord{}, http.StatusBadRequest, errors.New("expected_version 不能为负数")
	}
	conc := concurrencyOptionsForPrincipal(p, effExpected, forceLease)
	dev := extractDeviceInfoForPrincipal(r, p)

	var (
		updated storage.FileRecord
		err     error
	)
	if isLocal {
		if rec.StorageMode != storage.StorageModeMount {
			needBytes := rec.Size
			if needBytes < 4096 {
				needBytes = 4096
			}
			if capErr := s.cluster.CanAcceptLocal(needBytes); capErr != nil {
				return storage.FileRecord{}, http.StatusInsufficientStorage, capErr
			}
		}
		updated, err = s.store.PatchFileWithConcurrency(id, req, method, p.User.Username, dev, conc)
		if err != nil {
			return storage.FileRecord{}, http.StatusBadRequest, err
		}
		updated.NodeID = s.cluster.NodeID()
		updated.NodeName = s.cluster.NodeName()
		updated.NodeOS = runtime.GOOS
		updated.NodeOnline = true
	} else {
		if !peer.Online {
			return storage.FileRecord{}, http.StatusServiceUnavailable, errors.New("文件所在存储节点当前离线")
		}
		updated, err = s.cluster.ProxyPatchFileWithConcurrency(peer, id, req, method, p.User.Username, dev, conc)
		if err != nil {
			return storage.FileRecord{}, http.StatusBadGateway, err
		}
	}

	if p.IsAgent {
		s.logAudit(r, p.User.Username, "agent_file_patch", updated.Name, "ok", fmt.Sprintf("%s v%d", p.ActorLabel(), updated.Version))
	}
	s.publishRecordChangeWithActor("upload", p.User.Username, p.ActorLabel(), []storage.FileRecord{updated}, updated.AllowedUsers)
	return updated, http.StatusOK, nil
}

func (s *Server) batchCommitForPrincipal(
	r *http.Request,
	p auth.Principal,
	req storage.BatchCommitRequest,
	forceLease bool,
) (storage.BatchCommitResult, int, error) {
	if len(req.Operations) == 0 {
		return storage.BatchCommitResult{}, http.StatusBadRequest, errors.New("operations 不能为空")
	}
	if len(req.Operations) > storage.MaxBatchOps {
		return storage.BatchCommitResult{}, http.StatusBadRequest, fmt.Errorf("单次 batch_commit 最多允许 %d 个操作", storage.MaxBatchOps)
	}

	allFiles := s.cluster.AllClusterFiles()
	type indexedFiles struct {
		mountRec    *storage.FileRecord
		nonMountRec *storage.FileRecord
	}
	byPath := make(map[string]*indexedFiles, len(allFiles))
	for _, rec := range allFiles {
		key := strings.ToLower(storage.LogicalPathOf(rec.FolderPath, rec.Name))
		entry := byPath[key]
		if entry == nil {
			entry = &indexedFiles{}
			byPath[key] = entry
		}
		cp := rec
		if rec.StorageMode == storage.StorageModeMount {
			if entry.mountRec == nil {
				entry.mountRec = &cp
			}
		} else {
			if entry.nonMountRec == nil {
				entry.nonMountRec = &cp
			}
		}
	}

	selfID := s.cluster.NodeID()
	sanitizedOps := make([]storage.BatchOp, len(req.Operations))
	copy(sanitizedOps, req.Operations)

	var targetNodeID string
	var targetPeer cluster.NodeStatus
	targetIsSelf := true
	hasNewNonMountWrite := false
	var newNonMountBytes int64

	for i, op := range req.Operations {
		action := strings.ToLower(strings.TrimSpace(op.Action))
		folderPath, fileName, err := storage.SplitLogicalPath(op.Path)
		if err != nil {
			return storage.BatchCommitResult{}, http.StatusBadRequest, fmt.Errorf("operations[%d] 路径无效: %w", i, err)
		}
		switch action {
		case "write":
			if !p.User.EffectiveCanUpload() || !p.User.CanAccessFolder(folderPath) {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("当前账号为只读权限或无权写入目录: " + folderPath)
			}
			if p.IsAgent && !p.CanWriteFolder(folderPath) {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("Agent 密钥无权写入目录: " + folderPath)
			}
			if extErr := s.auth.CheckUploadAllowed(fileName); extErr != nil {
				return storage.BatchCommitResult{}, http.StatusBadRequest, extErr
			}
			candidates := op.AllowedUsers
			if p.IsAgent {
				resolved, rErr := p.ResolveAllowedUsers(candidates)
				if rErr != nil {
					return storage.BatchCommitResult{}, http.StatusForbidden, rErr
				}
				candidates = resolved
			}
			cleanUsers, uErr := s.auth.ValidateExistingUsers(candidates)
			if uErr != nil {
				return storage.BatchCommitResult{}, http.StatusBadRequest, uErr
			}
			sanitizedOps[i].AllowedUsers = cleanUsers
		case "patch":
			if !p.User.EffectiveCanUpload() || !p.User.CanAccessFolder(folderPath) {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("当前账号为只读权限或无权修改目录: " + folderPath)
			}
			if p.IsAgent && !p.CanWriteFolder(folderPath) {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("Agent 密钥无权修改目录文件: " + folderPath)
			}
		case "delete":
			if !p.User.EffectiveCanUpload() || !p.User.CanAccessFolder(folderPath) {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("当前账号为只读权限或无权删除目录: " + folderPath)
			}
			if p.IsAgent && !p.CanDeleteFolder(folderPath) {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("Agent 密钥无权删除目录文件: " + folderPath)
			}
		default:
			return storage.BatchCommitResult{}, http.StatusBadRequest, fmt.Errorf("operations[%d] 不支持的操作类型: %s", i, op.Action)
		}

		pathKey := strings.ToLower(storage.LogicalPathOf(folderPath, fileName))
		mountView, _, mountPeer, inMount := s.cluster.FindMountByFolderPath(folderPath)
		var opNodeID string
		var opIsSelf bool
		var opPeer cluster.NodeStatus

		if inMount {
			if !mountView.MountEntry.Writable {
				return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("该目录属于只读挂载目录，请先在挂载配置中开启可写工作区")
			}
			opNodeID = mountView.NodeID
			opIsSelf = mountView.IsSelf
			opPeer = mountPeer
			if !opIsSelf && !opPeer.Online {
				return storage.BatchCommitResult{}, http.StatusServiceUnavailable, errors.New("挂载所在存储节点当前离线")
			}
			if action == "delete" {
				if idx := byPath[pathKey]; idx != nil && idx.mountRec != nil {
					if p.User.Role != "admin" && idx.mountRec.Uploader != p.User.Username {
						return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("仅上传者或管理员可删除文件: " + op.Path)
					}
				} else {
					return storage.BatchCommitResult{}, http.StatusNotFound, errors.New("待删除文件不存在: " + op.Path)
				}
			}
		} else {
			var existing *storage.FileRecord
			if idx := byPath[pathKey]; idx != nil {
				existing = idx.nonMountRec
			}
			if existing != nil {
				if !storage.CanUserAccess(*existing, p.User.Username, p.User.Role) {
					return storage.BatchCommitResult{}, http.StatusNotFound, errors.New("文件不存在或无权访问: " + op.Path)
				}
				if action == "delete" && p.User.Role != "admin" && existing.Uploader != p.User.Username {
					return storage.BatchCommitResult{}, http.StatusForbidden, errors.New("仅上传者或管理员可删除文件: " + op.Path)
				}
				opNodeID = existing.NodeID
				if opNodeID == "" || opNodeID == selfID {
					opNodeID = selfID
					opIsSelf = true
				} else {
					if !existing.NodeOnline {
						return storage.BatchCommitResult{}, http.StatusServiceUnavailable, errors.New("文件所在存储节点当前离线")
					}
					_, peer, ok := s.cluster.LocateFile(existing.ID)
					if !ok || !peer.Online {
						return storage.BatchCommitResult{}, http.StatusServiceUnavailable, errors.New("文件所在存储节点当前离线")
					}
					opPeer = peer
				}
			} else if action == "write" {
				hasNewNonMountWrite = true
				newNonMountBytes += int64(len(op.Content))
				continue
			} else {
				return storage.BatchCommitResult{}, http.StatusNotFound, errors.New("文件不存在: " + op.Path)
			}
		}

		if targetNodeID == "" {
			targetNodeID = opNodeID
			targetIsSelf = opIsSelf
			targetPeer = opPeer
		} else if opNodeID != targetNodeID {
			return storage.BatchCommitResult{}, http.StatusBadRequest, errors.New("单次 batch_commit 暂不支持跨多个物理存储节点提交，请按挂载目录或节点拆分批次")
		}
	}

	if newNonMountBytes > 0 {
		if qStatus, qMsg := s.checkPrincipalQuota(p, newNonMountBytes); qStatus != http.StatusOK {
			return storage.BatchCommitResult{}, qStatus, errors.New(qMsg)
		}
	}
	if hasNewNonMountWrite && targetNodeID == "" {
		selected, selErr := s.cluster.SelectUploadTarget("auto", newNonMountBytes)
		if selErr != nil {
			return storage.BatchCommitResult{}, http.StatusInsufficientStorage, selErr
		}
		targetIsSelf = selected.IsSelf
		targetPeer = selected
		if targetIsSelf {
			targetNodeID = selfID
		} else {
			targetNodeID = selected.NodeID
		}
	}

	sanitizedReq := storage.BatchCommitRequest{
		Note:       req.Note,
		Operations: sanitizedOps,
	}
	conc := concurrencyOptionsForPrincipal(p, nil, forceLease)
	dev := extractDeviceInfoForPrincipal(r, p)

	var (
		res storage.BatchCommitResult
		err error
	)
	if targetIsSelf {
		hooks := storage.BatchPolicyHooks{
			CheckAccess: func(action, folderPath, fileName string, existing *storage.FileRecord) error {
				switch action {
				case "write":
					if p.IsAgent && !p.CanWriteFolder(folderPath) {
						return errors.New("安全策略/Agent 密钥无权写入目录: " + folderPath)
					}
					return s.auth.CheckUploadAllowed(fileName)
				case "patch":
					if p.IsAgent && !p.CanWriteFolder(folderPath) {
						return errors.New("安全策略/Agent 密钥无权修改目录: " + folderPath)
					}
					return nil
				case "delete":
					if p.IsAgent && !p.CanDeleteFolder(folderPath) {
						return errors.New("安全策略/Agent 密钥无权删除目录: " + folderPath)
					}
					if existing != nil && p.User.Role != "admin" && existing.Uploader != p.User.Username {
						return errors.New("安全策略/仅上传者或管理员可删除文件")
					}
					return nil
				default:
					return errors.New("不支持的操作")
				}
			},
			ResolveAllowedUsers: func(requested []string) ([]string, error) {
				candidates := requested
				if p.IsAgent {
					resolved, rErr := p.ResolveAllowedUsers(candidates)
					if rErr != nil {
						return nil, rErr
					}
					candidates = resolved
				}
				return s.auth.ValidateExistingUsers(candidates)
			},
		}
		res, err = s.store.BatchCommitWithConcurrency(sanitizedReq, p.User.Username, dev, conc, hooks)
		if err != nil {
			return storage.BatchCommitResult{}, http.StatusBadRequest, err
		}
		for i := range res.Results {
			if res.Results[i].Record != nil {
				res.Results[i].Record.NodeID = selfID
				res.Results[i].Record.NodeName = s.cluster.NodeName()
				res.Results[i].Record.NodeOS = runtime.GOOS
				res.Results[i].Record.NodeOnline = true
			}
		}
	} else {
		if !targetPeer.Online {
			return storage.BatchCommitResult{}, http.StatusServiceUnavailable, errors.New("目标存储节点当前离线")
		}
		res, err = s.cluster.ProxyBatchCommit(targetPeer, sanitizedReq, p.User.Username, dev, conc)
		if err != nil {
			return storage.BatchCommitResult{}, http.StatusBadGateway, err
		}
	}

	var eventRecs []storage.FileRecord
	var eventFolders []string
	var eventNames []string
	seenFolder := make(map[string]bool)
	var unionAllowed []string
	firstAllowed := true
	seenAllowed := make(map[string]bool)

	for _, item := range res.Results {
		folderPath, fileName, splitErr := storage.SplitLogicalPath(item.Path)
		if splitErr == nil {
			if !seenFolder[folderPath] {
				seenFolder[folderPath] = true
				eventFolders = append(eventFolders, folderPath)
			}
			if len(eventNames) < 3 {
				eventNames = append(eventNames, fileName)
			}
		}
		if item.Record != nil {
			eventRecs = append(eventRecs, *item.Record)
			if len(item.Record.AllowedUsers) == 0 {
				firstAllowed = false
				unionAllowed = nil
			} else if firstAllowed {
				for _, u := range item.Record.AllowedUsers {
					if !seenAllowed[u] {
						seenAllowed[u] = true
						unionAllowed = append(unionAllowed, u)
					}
				}
			}
		}
	}
	_ = eventRecs

	nodeName := s.cluster.NodeName()
	if !targetIsSelf && targetPeer.NodeName != "" {
		nodeName = targetPeer.NodeName
	}
	if p.IsAgent {
		s.logAudit(r, p.User.Username, "agent_batch_commit", res.BatchID, "ok", fmt.Sprintf("%s ops=%d", p.ActorLabel(), len(res.Results)))
	}
	s.events.Publish(FileChangeEvent{
		Kind:         "batch_commit",
		Uploader:     p.User.Username,
		ActorName:    p.ActorLabel(),
		Count:        len(res.Results),
		FileNames:    eventNames,
		FolderPaths:  eventFolders,
		BatchID:      res.BatchID,
		NodeName:     nodeName,
		AllowedUsers: unionAllowed,
	})
	return res, http.StatusOK, nil
}

func writeServiceStorageError(w http.ResponseWriter, status int, err error) {
	if writeConcurrencyError(w, err) {
		return
	}
	if isStoragePolicyForbidden(err) || status == http.StatusForbidden {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}
	if status == http.StatusNotFound || status == http.StatusServiceUnavailable || status == http.StatusInsufficientStorage {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeStorageActionError(w, err, status)
}

func (s *Server) handlePatchFile(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	id := r.PathValue("id")
	var req struct {
		Mode            string              `json:"mode"`
		Hunks           []storage.PatchHunk `json:"hunks"`
		UnifiedDiff     string              `json:"unified_diff"`
		ExpectedVersion *int                `json:"expected_version"`
		Note            string              `json:"note"`
		ForceLease      bool                `json:"force_lease"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析补丁请求失败"})
		return
	}
	expectedVer, verErr := resolveExpectedVersion(req.ExpectedVersion, r.URL.Query().Get("expected_version"), r)
	if verErr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": verErr.Error()})
		return
	}
	method := storage.MethodPatch
	if p.IsAgent {
		method = storage.MethodAgentPatch
	}
	updated, status, err := s.patchFileForPrincipal(r, p, id, storage.PatchRequest{
		Mode:            req.Mode,
		Hunks:           req.Hunks,
		UnifiedDiff:     req.UnifiedDiff,
		ExpectedVersion: expectedVer,
		Note:            req.Note,
	}, method, expectedVer, req.ForceLease)
	if err != nil {
		writeServiceStorageError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"file": s.sanitizeRecordForPrincipal(updated, p),
	})
}

func (s *Server) handleBatchCommit(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req struct {
		Note       string            `json:"note"`
		Operations []storage.BatchOp `json:"operations"`
		ForceLease bool              `json:"force_lease"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 24<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析批量提交请求失败"})
		return
	}
	res, status, err := s.batchCommitForPrincipal(r, p, storage.BatchCommitRequest{
		Note:       req.Note,
		Operations: req.Operations,
	}, req.ForceLease)
	if err != nil {
		writeServiceStorageError(w, status, err)
		return
	}
	for i := range res.Results {
		if res.Results[i].Record != nil {
			clean := s.sanitizeRecordForPrincipal(*res.Results[i].Record, p)
			res.Results[i].Record = &clean
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"batch_id": res.BatchID,
		"note":     res.Note,
		"results":  res.Results,
	})
}

func (s *Server) handleClusterPatchFile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Mode            string              `json:"mode"`
		Hunks           []storage.PatchHunk `json:"hunks"`
		UnifiedDiff     string              `json:"unified_diff"`
		Note            string              `json:"note"`
		Method          string              `json:"method"`
		Uploader        string              `json:"uploader"`
		DeviceFP        string              `json:"device_fp"`
		DeviceName      string              `json:"device_name"`
		DeviceIP        string              `json:"device_ip"`
		DeviceDetail    string              `json:"device_detail"`
		ExpectedVersion *int                `json:"expected_version"`
		ActorKeyID      string              `json:"actor_key_id"`
		ForceLease      bool                `json:"force_lease"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 12<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析补丁请求失败"})
		return
	}
	uploader := strings.TrimSpace(req.Uploader)
	if uploader == "" {
		uploader = "unknown"
	}
	dev := storage.DeviceInfo{
		FP:     req.DeviceFP,
		Name:   req.DeviceName,
		IP:     req.DeviceIP,
		Detail: req.DeviceDetail,
	}
	conc := storage.WriteConcurrencyOptions{
		ExpectedVersion: req.ExpectedVersion,
		ActorKeyID:      strings.TrimSpace(req.ActorKeyID),
		ForceLease:      req.ForceLease,
	}
	updated, err := s.store.PatchFileWithConcurrency(id, storage.PatchRequest{
		Mode:            req.Mode,
		Hunks:           req.Hunks,
		UnifiedDiff:     req.UnifiedDiff,
		ExpectedVersion: req.ExpectedVersion,
		Note:            req.Note,
	}, req.Method, uploader, dev, conc)
	if err != nil {
		if writeConcurrencyError(w, err) {
			return
		}
		if strings.Contains(err.Error(), "不存在") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeStorageActionError(w, err, http.StatusBadRequest)
		return
	}
	updated.NodeID = s.cluster.NodeID()
	updated.NodeName = s.cluster.NodeName()
	updated.NodeOS = runtime.GOOS
	updated.NodeOnline = true
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleClusterBatchCommit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Note         string            `json:"note"`
		Operations   []storage.BatchOp `json:"operations"`
		Uploader     string            `json:"uploader"`
		DeviceFP     string            `json:"device_fp"`
		DeviceName   string            `json:"device_name"`
		DeviceIP     string            `json:"device_ip"`
		DeviceDetail string            `json:"device_detail"`
		ActorKeyID   string            `json:"actor_key_id"`
		ForceLease   bool              `json:"force_lease"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 24<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析批量提交请求失败"})
		return
	}
	uploader := strings.TrimSpace(req.Uploader)
	if uploader == "" {
		uploader = "unknown"
	}
	dev := storage.DeviceInfo{
		FP:     req.DeviceFP,
		Name:   req.DeviceName,
		IP:     req.DeviceIP,
		Detail: req.DeviceDetail,
	}
	conc := storage.WriteConcurrencyOptions{
		ActorKeyID: strings.TrimSpace(req.ActorKeyID),
		ForceLease: req.ForceLease,
	}
	res, err := s.store.BatchCommitWithConcurrency(storage.BatchCommitRequest{
		Note:       req.Note,
		Operations: req.Operations,
	}, uploader, dev, conc, storage.BatchPolicyHooks{})
	if err != nil {
		writeStorageActionError(w, err, http.StatusBadRequest)
		return
	}
	selfID := s.cluster.NodeID()
	selfName := s.cluster.NodeName()
	for i := range res.Results {
		if res.Results[i].Record != nil {
			res.Results[i].Record.NodeID = selfID
			res.Results[i].Record.NodeName = selfName
			res.Results[i].Record.NodeOS = runtime.GOOS
			res.Results[i].Record.NodeOnline = true
		}
	}
	s.cluster.TriggerHeartbeat()
	writeJSON(w, http.StatusOK, res)
}

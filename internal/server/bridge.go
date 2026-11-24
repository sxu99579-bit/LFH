package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

// IssueTicketRequest describes an agent or human request for an HMAC transfer ticket.
type IssueTicketRequest struct {
	Action       string   `json:"action"` // "up" | "down"
	FileID       string   `json:"file_id,omitempty"`
	Path         string   `json:"path,omitempty"`
	FolderPath   string   `json:"folder_path,omitempty"`
	FileName     string   `json:"file_name,omitempty"`
	AllowedUsers []string `json:"allowed_users,omitempty"`
	MaxBytes     int64    `json:"max_bytes,omitempty"`
	TargetNode   string   `json:"target_node,omitempty"`
	TTLSeconds   int      `json:"ttl_seconds,omitempty"`
	PreferDirect *bool    `json:"prefer_direct,omitempty"`
}

// IssueTicketResponse contains signed /s/ticket URLs for Entry and optional Direct-to-Worker access.
type IssueTicketResponse struct {
	OK             bool      `json:"ok"`
	Action         string    `json:"action"`
	NodeID         string    `json:"node_id"`
	NodeName       string    `json:"node_name"`
	DirectURL      string    `json:"direct_url,omitempty"`
	EntryURL       string    `json:"entry_url"`
	RecommendedURL string    `json:"recommended_url"`
	ExpiresAt      time.Time `json:"expires_at"`
	MaxBytes       int64     `json:"max_bytes,omitempty"`
	SingleUse      bool      `json:"single_use"`
	Method         string    `json:"method"`
}

var errTicketPayloadTooLarge = errors.New("上传文件大小超出传输短链限制")

type boundedTicketReader struct {
	r        io.Reader
	limit    int64
	read     int64
	exceeded bool
}

func (b *boundedTicketReader) Read(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.r.Read(p)
	}
	remain := b.limit - b.read
	if remain <= 0 {
		var probe [1]byte
		n, err := b.r.Read(probe[:])
		if n > 0 {
			b.exceeded = true
			return 0, errTicketPayloadTooLarge
		}
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n, err := b.r.Read(p)
	b.read += int64(n)
	return n, err
}

func (s *Server) getWebhooks() *bridge.WebhookManager {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.webhooks == nil && s.store != nil {
		wh, err := bridge.NewWebhookManager(s.store.DataDir())
		if err != nil {
			log.Printf("[bridge] init webhook manager failed: %v", err)
			return nil
		}
		s.webhooks = wh
	}
	return s.webhooks
}

func (s *Server) getVerifyRunner() *bridge.VerifyRunner {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.verifyRunner == nil {
		s.verifyRunner = bridge.NewVerifyRunner()
	}
	return s.verifyRunner
}

func (s *Server) getTicketGuard() *bridge.TicketReplayGuard {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticketGuard == nil {
		s.ticketGuard = bridge.NewTicketReplayGuard()
	}
	return s.ticketGuard
}

func (s *Server) ticketSecret() string {
	if s.cluster != nil {
		if sec := strings.TrimSpace(s.cluster.Secret()); sec != "" {
			return "lfh-ticket:" + sec
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ticketFallbackSecret == "" {
		var raw [32]byte
		_, _ = rand.Read(raw[:])
		s.ticketFallbackSecret = "lfh-ticket-standalone:" + hex.EncodeToString(raw[:])
	}
	return s.ticketFallbackSecret
}

func requestBaseURL(r *http.Request, fallback string, port string) string {
	if r != nil && strings.TrimSpace(r.Host) != "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			scheme = "https"
		}
		return scheme + "://" + strings.TrimSpace(r.Host)
	}
	if clean := strings.TrimRight(strings.TrimSpace(fallback), "/"); clean != "" {
		return clean
	}
	cleanPort := strings.TrimPrefix(strings.TrimSpace(port), ":")
	if cleanPort == "" {
		cleanPort = "8080"
	}
	return "http://127.0.0.1:" + cleanPort
}

func isDirectTicketEligible(r *http.Request) bool {
	if r == nil {
		return true
	}
	if !bridge.IsPrivateOrTailscaleAddr(r.RemoteAddr) {
		return false
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if !bridge.IsPrivateOrTailscaleAddr(first) {
			return false
		}
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if !bridge.IsPrivateOrTailscaleAddr(xri) {
			return false
		}
	}
	return true
}

func (s *Server) issueTransferTicketForPrincipal(r *http.Request, p auth.Principal, req IssueTicketRequest) (IssueTicketResponse, int, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != bridge.TicketActionUp && action != bridge.TicketActionDown {
		return IssueTicketResponse{}, http.StatusBadRequest, errors.New("action 必须为 up 或 down")
	}
	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = bridge.DefaultTicketTTLSec
	}
	if ttl < bridge.MinTicketTTLSec {
		ttl = bridge.MinTicketTTLSec
	}
	if ttl > bridge.MaxTicketTTLSec {
		ttl = bridge.MaxTicketTTLSec
	}
	expAt := time.Now().Add(time.Duration(ttl) * time.Second).UTC()
	preferDirect := req.PreferDirect == nil || *req.PreferDirect

	var targetNode cluster.NodeStatus
	var ticket bridge.TransferTicket

	switch action {
	case bridge.TicketActionDown:
		if !p.HasScope(auth.ScopeRead) {
			return IssueTicketResponse{}, http.StatusForbidden, errors.New("当前凭证缺少 read 权限")
		}
		rec, _, peer, isLocal, status, errMsg := s.resolveFileByIDOrPath(req.FileID, req.Path, p)
		if status != http.StatusOK {
			return IssueTicketResponse{}, status, errors.New(errMsg)
		}
		if isLocal {
			targetNode = s.cluster.SelfStatus()
		} else {
			if !peer.Online || strings.TrimSpace(peer.HTTPURL) == "" {
				return IssueTicketResponse{}, http.StatusServiceUnavailable, errors.New("文件所在存储节点当前离线")
			}
			targetNode = peer
		}
		actorKeyID, _ := principalActorKeyID(p)
		ticket = bridge.TransferTicket{
			Action:       bridge.TicketActionDown,
			NodeID:       targetNode.NodeID,
			FileID:       rec.ID,
			AllowedUsers: append([]string(nil), rec.AllowedUsers...),
			ActorUser:    p.User.Username,
			ActorDevice:  p.ActorLabel(),
			ActorKeyID:   actorKeyID,
			Exp:          expAt.Unix(),
		}

	case bridge.TicketActionUp:
		if !p.HasScope(auth.ScopeWrite) {
			return IssueTicketResponse{}, http.StatusForbidden, errors.New("当前凭证缺少 write 权限")
		}
		var folderPath, fileName string
		if strings.TrimSpace(req.Path) != "" && strings.TrimSpace(req.FileName) == "" {
			var err error
			folderPath, fileName, err = storage.SplitLogicalPath(req.Path)
			if err != nil {
				return IssueTicketResponse{}, http.StatusBadRequest, err
			}
		} else {
			folderPath = storage.SanitizeFolderPath(req.FolderPath)
			fileName = strings.TrimSpace(filepath.Base(strings.ReplaceAll(strings.TrimSpace(req.FileName), "\\", "/")))
		}
		if fileName == "" || fileName == "." || fileName == ".." || strings.ContainsAny(fileName, "/\\") {
			return IssueTicketResponse{}, http.StatusBadRequest, errors.New("请提供有效的文件名 (file_name 或 path)")
		}
		if !p.CanWriteFolder(folderPath) {
			return IssueTicketResponse{}, http.StatusForbidden, errors.New("Agent 密钥无权写入该目录")
		}
		if s.auth != nil {
			if err := s.auth.CheckUploadAllowed(fileName); err != nil {
				return IssueTicketResponse{}, http.StatusBadRequest, err
			}
		}
		resolvedAllowed, err := p.ResolveAllowedUsers(req.AllowedUsers)
		if err != nil {
			return IssueTicketResponse{}, http.StatusForbidden, err
		}
		var cleanAllowed []string
		if s.auth != nil {
			cleanAllowed, err = s.auth.ValidateExistingUsers(resolvedAllowed)
			if err != nil {
				return IssueTicketResponse{}, http.StatusBadRequest, err
			}
		} else {
			cleanAllowed = storage.NormalizeAllowedUsers(resolvedAllowed)
		}
		maxBytes := req.MaxBytes
		if maxBytes == 0 {
			maxBytes = bridge.DefaultTicketMaxBytes
		}
		if maxBytes < 0 || maxBytes > bridge.DefaultTicketMaxBytes {
			return IssueTicketResponse{}, http.StatusBadRequest, fmt.Errorf("max_bytes 必须在 1 到 %d 之间", bridge.DefaultTicketMaxBytes)
		}

		mountView, _, mountPeer, inMount := s.cluster.FindMountByFolderPath(folderPath)
		if inMount {
			if !mountView.Writable {
				return IssueTicketResponse{}, http.StatusForbidden, errors.New("该挂载目录为只读模式")
			}
			if !mountVisibleToPrincipal(mountView.Alias, mountView.AddedBy, mountView.AllowedUsers, p) {
				return IssueTicketResponse{}, http.StatusForbidden, errors.New("无权写入该挂载目录")
			}
			if mountView.IsSelf {
				targetNode = s.cluster.SelfStatus()
			} else {
				if !mountPeer.Online || strings.TrimSpace(mountPeer.HTTPURL) == "" {
					return IssueTicketResponse{}, http.StatusServiceUnavailable, errors.New("挂载所在存储节点当前离线")
				}
				targetNode = mountPeer
			}
		} else {
			sizeHint := maxBytes
			if req.MaxBytes <= 0 {
				sizeHint = 0
			}
			targetNode, err = s.cluster.SelectUploadTarget(strings.TrimSpace(req.TargetNode), sizeHint)
			if err != nil {
				return IssueTicketResponse{}, http.StatusInsufficientStorage, err
			}
		}

		actorKeyID, _ := principalActorKeyID(p)
		ticket = bridge.TransferTicket{
			Action:       bridge.TicketActionUp,
			NodeID:       targetNode.NodeID,
			FolderPath:   folderPath,
			FileName:     fileName,
			AllowedUsers: cleanAllowed,
			ActorUser:    p.User.Username,
			ActorDevice:  p.ActorLabel(),
			ActorKeyID:   actorKeyID,
			MaxBytes:     maxBytes,
			Exp:          expAt.Unix(),
		}
	}

	payloadB64, sigHex, err := bridge.SignTicket(s.ticketSecret(), ticket)
	if err != nil {
		return IssueTicketResponse{}, http.StatusInternalServerError, err
	}
	qs := "p=" + url.QueryEscape(payloadB64) + "&s=" + url.QueryEscape(sigHex)
	selfURL := ""
	if s.cluster != nil {
		selfURL = s.cluster.SelfStatus().HTTPURL
	}
	entryBase := requestBaseURL(r, selfURL, s.port)
	entryURL := entryBase + "/s/ticket?" + qs

	directURL := ""
	recommendedURL := entryURL
	selfNodeID := ""
	if s.cluster != nil {
		selfNodeID = s.cluster.NodeID()
	}
	if preferDirect && strings.TrimSpace(targetNode.HTTPURL) != "" && isDirectTicketEligible(r) {
		if !targetNode.IsSelf && targetNode.NodeID != "" && targetNode.NodeID != selfNodeID {
			directURL = strings.TrimRight(targetNode.HTTPURL, "/") + "/s/ticket?" + qs
			recommendedURL = directURL
		} else {
			directURL = entryURL
		}
	}

	method := http.MethodGet
	if action == bridge.TicketActionUp {
		method = http.MethodPut
	}
	return IssueTicketResponse{
		OK:             true,
		Action:         action,
		NodeID:         targetNode.NodeID,
		NodeName:       targetNode.NodeName,
		DirectURL:      directURL,
		EntryURL:       entryURL,
		RecommendedURL: recommendedURL,
		ExpiresAt:      expAt,
		MaxBytes:       ticket.MaxBytes,
		SingleUse:      action == bridge.TicketActionUp,
		Method:         method,
	}, http.StatusOK, nil
}

func (s *Server) handleIssueTicket(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	var req IssueTicketRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	resp, status, err := s.issueTransferTicketForPrincipal(r, p, req)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, p.User.Username, "ticket_issue", req.Action, "ok", fmt.Sprintf("node=%s path=%s file=%s", resp.NodeName, req.Path, req.FileID))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleServeTicket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	payloadB64 := strings.TrimSpace(r.URL.Query().Get("p"))
	sigHex := strings.TrimSpace(r.URL.Query().Get("s"))
	ticket, err := bridge.VerifyTicket(s.ticketSecret(), payloadB64, sigHex, time.Now())
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, bridge.ErrTicketExpired) {
			status = http.StatusGone
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	selfID := ""
	role := cluster.RoleEntry
	if s.cluster != nil {
		selfID = s.cluster.NodeID()
		role = s.cluster.Role()
	}

	switch ticket.Action {
	case bridge.TicketActionDown:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "下载短链仅支持 GET/HEAD 请求"})
			return
		}
		if ticket.NodeID == "" || ticket.NodeID == selfID {
			s.serveFileAttachment(w, r, ticket.FileID)
			return
		}
		if role == cluster.RoleWorker {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "该传输短链不属于当前存储节点"})
			return
		}
		peer, err := s.cluster.ResolveNode(ticket.NodeID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		s.cluster.ProxyGet(w, r, peer, "/internal/cluster/files/"+url.PathEscape(ticket.FileID)+"/download")

	case bridge.TicketActionUp:
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			w.Header().Set("Allow", "PUT, POST")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "上传短链仅支持 PUT/POST 请求"})
			return
		}
		if role == cluster.RoleWorker && ticket.NodeID != selfID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "该传输短链不属于当前存储节点"})
			return
		}
		maxBytes := ticket.MaxBytes
		if maxBytes <= 0 || maxBytes > bridge.DefaultTicketMaxBytes {
			maxBytes = bridge.DefaultTicketMaxBytes
		}
		if r.ContentLength > maxBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": fmt.Sprintf("上传大小超出短链限制 (%d bytes)", maxBytes)})
			return
		}
		if !s.getTicketGuard().Consume(ticket.Nonce, ticket.Exp, time.Now()) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": bridge.ErrTicketReplayed.Error()})
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		var uploadReader io.Reader = r.Body
		var closer io.Closer
		if r.Method == http.MethodPost && strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type"))), "multipart/form-data") {
			if err := r.ParseMultipartForm(32 << 20); err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": errTicketPayloadTooLarge.Error()})
					return
				}
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "解析表单失败: " + err.Error()})
				return
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请通过 file 字段上传文件或使用 PUT 原始流"})
				return
			}
			closer = file
			uploadReader = file
		}
		if closer != nil {
			defer closer.Close()
		}

		tracker := &boundedTicketReader{r: uploadReader, limit: maxBytes}
		dev := extractDeviceInfo(r)
		if ticket.ActorDevice != "" {
			dev.Name = ticket.ActorDevice
		}
		dev.Detail = "ticket-upload"
		conc := storage.WriteConcurrencyOptions{ActorKeyID: ticket.ActorKeyID}

		var rec storage.FileRecord
		var saveErr error
		if ticket.NodeID == "" || ticket.NodeID == selfID {
			rec, saveErr = s.store.SaveUploadWithConcurrency(ticket.FileName, ticket.FolderPath, "ticket-upload", ticket.ActorUser, ticket.AllowedUsers, dev, tracker, conc)
			if saveErr != nil {
				var maxErr *http.MaxBytesError
				if tracker.exceeded || errors.Is(saveErr, errTicketPayloadTooLarge) || errors.As(saveErr, &maxErr) {
					writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": errTicketPayloadTooLarge.Error()})
					return
				}
				writeStorageActionError(w, saveErr, http.StatusInternalServerError)
				return
			}
			if s.cluster != nil {
				rec.NodeID = s.cluster.NodeID()
				rec.NodeName = s.cluster.NodeName()
			}
			rec.NodeOS = runtime.GOOS
			rec.NodeOnline = true

			if s.cluster != nil && role == cluster.RoleWorker {
				s.cluster.TriggerHeartbeat()
				s.notifyEntryBridgeEvent(FileChangeEvent{
					Kind:         "upload",
					Uploader:     ticket.ActorUser,
					ActorName:    ticket.ActorDevice,
					Count:        1,
					FileNames:    []string{rec.Name},
					FolderPaths:  collectEventFolderPaths([]storage.FileRecord{rec}),
					NodeName:     rec.NodeName,
					AllowedUsers: rec.AllowedUsers,
				}, &rec)
			} else {
				s.publishRecordChangeWithActor("upload", ticket.ActorUser, ticket.ActorDevice, []storage.FileRecord{rec}, rec.AllowedUsers)
			}
		} else {
			peer, err := s.cluster.ResolveNode(ticket.NodeID)
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
				return
			}
			rec, saveErr = s.cluster.ProxyUploadWithConcurrency(peer, ticket.FileName, ticket.FolderPath, tracker, ticket.ActorUser, ticket.AllowedUsers, dev, conc)
			if saveErr != nil {
				var maxErr *http.MaxBytesError
				if tracker.exceeded || errors.Is(saveErr, errTicketPayloadTooLarge) || errors.As(saveErr, &maxErr) {
					writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": errTicketPayloadTooLarge.Error()})
					return
				}
				writeStorageActionError(w, saveErr, http.StatusInternalServerError)
				return
			}
			s.publishRecordChangeWithActor("upload", ticket.ActorUser, ticket.ActorDevice, []storage.FileRecord{rec}, rec.AllowedUsers)
		}

		s.logAudit(r, ticket.ActorUser, "ticket_upload", rec.Name, "ok", fmt.Sprintf("node=%s folder=%s size=%d", rec.NodeName, rec.FolderPath, rec.Size))
		writeJSON(w, http.StatusCreated, map[string]any{
			"ok":   true,
			"file": rec,
		})

	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": bridge.ErrTicketInvalid.Error()})
	}
}

type clusterBridgeEventPayload struct {
	Event  FileChangeEvent     `json:"event"`
	Record *storage.FileRecord `json:"record,omitempty"`
}

func (s *Server) notifyEntryBridgeEvent(ev FileChangeEvent, rec *storage.FileRecord) {
	if s.cluster == nil {
		return
	}
	entryURL := strings.TrimRight(strings.TrimSpace(s.cluster.EntryURL()), "/")
	if entryURL == "" {
		return
	}
	raw, err := json.Marshal(clusterBridgeEventPayload{
		Event:  ev,
		Record: rec,
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, entryURL+"/internal/cluster/bridge-event", bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	cluster.SignRequest(req, s.cluster.Secret(), s.cluster.NodeID())
	resp, err := (&http.Client{Timeout: 1500 * time.Millisecond}).Do(req)
	if err != nil {
		log.Printf("[bridge] notify entry bridge-event failed: %v", err)
		return
	}
	_ = resp.Body.Close()
}

func (s *Server) handleClusterBridgeEvent(w http.ResponseWriter, r *http.Request) {
	var payload clusterBridgeEventPayload
	if err := json.NewDecoder(io.LimitReader(r.Body, 512<<10)).Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if payload.Record != nil && s.cluster != nil && payload.Record.NodeID != "" && payload.Record.NodeID != s.cluster.NodeID() {
		rec := *payload.Record
		rec.NodeOnline = true
		s.cluster.AddPeerFile(rec.NodeID, rec)
		if len(payload.Event.AllowedUsers) == 0 && len(rec.AllowedUsers) > 0 {
			payload.Event.AllowedUsers = append([]string(nil), rec.AllowedUsers...)
		}
	}
	if payload.Event.Kind != "" {
		s.PublishEvent(payload.Event)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func mapFileEventKindToWebhookEvent(kind string) string {
	switch kind {
	case "upload", "extract":
		return bridge.EventFileUploaded
	case "update", "edit", "patch":
		return bridge.EventFileUpdated
	case "batch_commit":
		return bridge.EventFileBatchCommit
	case "delete":
		return bridge.EventFileDeleted
	default:
		return ""
	}
}

func (s *Server) dispatchWebhookForFileEvent(ev FileChangeEvent) {
	wh := s.getWebhooks()
	if wh == nil {
		return
	}
	eventType := mapFileEventKindToWebhookEvent(ev.Kind)
	if eventType == "" {
		return
	}
	wEv := bridge.WebhookEvent{
		Event:       eventType,
		Version:     ev.Version,
		Uploader:    ev.Uploader,
		ActorName:   ev.ActorName,
		Count:       ev.Count,
		FileNames:   append([]string(nil), ev.FileNames...),
		FolderPaths: append([]string(nil), ev.FolderPaths...),
		BatchID:     ev.BatchID,
		NodeName:    ev.NodeName,
		Timestamp:   ev.Timestamp,
	}
	if eventType == bridge.EventFileUploaded && len(ev.FileNames) == 1 && len(ev.FolderPaths) <= 1 && s.cluster != nil {
		wantFolder := ""
		if len(ev.FolderPaths) == 1 {
			wantFolder = storage.SanitizeFolderPath(ev.FolderPaths[0])
		}
		var matched *storage.FileRecord
		for _, rec := range s.cluster.AllClusterFiles() {
			if rec.Name == ev.FileNames[0] && storage.SanitizeFolderPath(rec.FolderPath) == wantFolder {
				rCopy := rec
				matched = &rCopy
				break
			}
		}
		if matched != nil {
			t := bridge.TransferTicket{
				Action:       bridge.TicketActionDown,
				NodeID:       matched.NodeID,
				FileID:       matched.ID,
				AllowedUsers: append([]string(nil), matched.AllowedUsers...),
				ActorUser:    ev.Uploader,
				ActorDevice:  ev.ActorName,
				Exp:          time.Now().Add(bridge.DefaultTicketTTLSec * time.Second).Unix(),
			}
			if pB64, sigHex, err := bridge.SignTicket(s.ticketSecret(), t); err == nil {
				base := requestBaseURL(nil, s.cluster.SelfStatus().HTTPURL, s.port)
				wEv.DownloadTicketURL = base + "/s/ticket?p=" + url.QueryEscape(pB64) + "&s=" + url.QueryEscape(sigHex)
			}
		}
	}
	wh.Dispatch(wEv)
}

func (s *Server) handleAdminListWebhooks(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	wh := s.getWebhooks()
	if wh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Webhook 管理器未就绪"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"webhooks": wh.List(),
	})
}

func (s *Server) handleAdminCreateWebhook(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	wh := s.getWebhooks()
	if wh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Webhook 管理器未就绪"})
		return
	}
	var in bridge.CreateWebhookInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	created, err := wh.Create(in, u.Username)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "webhook_create", created.Name, "ok", fmt.Sprintf("prefix=%s target=%s", created.FolderPrefix, created.TargetURL))
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":      true,
		"webhook": created,
	})
}

func (s *Server) handleAdminUpdateWebhook(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	wh := s.getWebhooks()
	if wh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Webhook 管理器未就绪"})
		return
	}
	id := r.PathValue("id")
	var in bridge.UpdateWebhookInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	updated, err := wh.Update(id, in)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "webhook_update", updated.Name, "ok", fmt.Sprintf("enabled=%v", updated.Enabled))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"webhook": updated,
	})
}

func (s *Server) handleAdminDeleteWebhook(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	wh := s.getWebhooks()
	if wh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Webhook 管理器未就绪"})
		return
	}
	id := r.PathValue("id")
	if err := wh.Delete(id); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "webhook_delete", id, "ok", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminTestWebhook(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	wh := s.getWebhooks()
	if wh == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Webhook 管理器未就绪"})
		return
	}
	id := r.PathValue("id")
	res, err := wh.SendTest(r.Context(), id, u.Username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "webhook_test", id, "ok", fmt.Sprintf("ok=%v status=%d", res.OK, res.StatusCode))
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     true,
		"result": res,
	})
}

func (s *Server) triggerWorkspaceVerifyForPrincipal(ctx context.Context, p auth.Principal, mountID, presetID string) (bridge.VerifyResult, int, error) {
	if !p.HasScope(auth.ScopeVerify) {
		return bridge.VerifyResult{}, http.StatusForbidden, errors.New("当前凭证缺少 verify 权限")
	}
	cleanMountID := strings.TrimSpace(mountID)
	cleanPresetID := strings.TrimSpace(presetID)
	if cleanMountID == "" || cleanPresetID == "" {
		return bridge.VerifyResult{}, http.StatusBadRequest, errors.New("必须指定 mount_id 和 preset_id")
	}

	var found *cluster.ClusterMountView
	for _, mv := range s.cluster.ListAllMounts() {
		if mv.ID == cleanMountID {
			mvCopy := mv
			found = &mvCopy
			break
		}
	}
	if found == nil {
		return bridge.VerifyResult{}, http.StatusNotFound, errors.New("挂载目录不存在")
	}
	if !mountVisibleToPrincipal(found.Alias, found.AddedBy, found.AllowedUsers, p) {
		return bridge.VerifyResult{}, http.StatusForbidden, errors.New("无权对该挂载目录执行构建校验")
	}

	var preset *storage.VerifyPreset
	for _, pr := range found.VerifyPresets {
		if pr.ID == cleanPresetID {
			prCopy := pr
			preset = &prCopy
			break
		}
	}
	if preset == nil {
		return bridge.VerifyResult{}, http.StatusNotFound, errors.New("指定的构建校验预设不存在")
	}

	var res bridge.VerifyResult
	var runErr error
	if found.IsSelf || found.NodeID == s.cluster.NodeID() {
		entry, ok := s.store.GetMount(cleanMountID)
		if !ok {
			return bridge.VerifyResult{}, http.StatusNotFound, errors.New("本地挂载目录不存在")
		}
		res, runErr = s.getVerifyRunner().Run(ctx, entry, *preset)
	} else {
		peer, err := s.cluster.ResolveNode(found.NodeID)
		if err != nil {
			return bridge.VerifyResult{}, http.StatusServiceUnavailable, err
		}
		res, runErr = s.cluster.ProxyVerifyMount(peer, cleanMountID, cleanPresetID)
	}
	if runErr != nil {
		return bridge.VerifyResult{}, http.StatusBadRequest, runErr
	}

	if wh := s.getWebhooks(); wh != nil {
		var evVer uint64
		if s.events != nil {
			evVer = s.events.Version()
		}
		wh.Dispatch(bridge.WebhookEvent{
			Event:        bridge.EventVerifyCompleted,
			Version:      evVer,
			Uploader:     p.User.Username,
			ActorName:    p.ActorLabel(),
			Count:        1,
			FolderPaths:  []string{storage.SanitizeFolderPath(found.Alias)},
			NodeName:     found.NodeName,
			Timestamp:    time.Now().UnixMilli(),
			VerifyResult: &res,
		})
	}
	return res, http.StatusOK, nil
}

func (s *Server) handleTriggerVerify(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	mountID := r.PathValue("mountID")
	presetID := r.PathValue("presetID")
	res, status, err := s.triggerWorkspaceVerifyForPrincipal(r.Context(), p, mountID, presetID)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, p.User.Username, "workspace_verify", mountID+"/"+presetID, "ok", fmt.Sprintf("exit=%d duration_ms=%d coalesced=%v", res.ExitCode, res.DurationMS, res.Coalesced))
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleClusterTriggerVerify(w http.ResponseWriter, r *http.Request) {
	mountID := r.PathValue("mountID")
	presetID := r.PathValue("presetID")
	entry, ok := s.store.GetMount(mountID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "本地挂载目录不存在"})
		return
	}
	var preset *storage.VerifyPreset
	for _, p := range entry.VerifyPresets {
		if p.ID == presetID {
			pCopy := p
			preset = &pCopy
			break
		}
	}
	if preset == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "构建校验预设不存在"})
		return
	}
	res, err := s.getVerifyRunner().Run(r.Context(), entry, *preset)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

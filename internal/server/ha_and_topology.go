package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/cluster"
)

func (s *Server) installHAHooks() {
	if s.cluster == nil {
		return
	}
	s.cluster.SetHAHooks(
		func() []string {
			_, activeAddrs, _ := s.allConfiguredAddrs()
			out := make([]string, 0, len(activeAddrs))
			for _, a := range activeAddrs {
				if strings.TrimSpace(a.URL) != "" {
					out = append(out, strings.TrimRight(strings.TrimSpace(a.URL), "/"))
				}
			}
			return out
		},
		func() (string, []byte, string, []byte) {
			var authHash string
			var authSnap []byte
			if s.auth != nil {
				if raw, err := s.auth.ExportSnapshot(); err == nil {
					authSnap = raw
					sum := sha256.Sum256(raw)
					authHash = hex.EncodeToString(sum[:8])
				}
			}
			var tasksHash string
			var tasksSnap []byte
			if tm := s.getTasks(); tm != nil {
				if raw, err := tm.ExportSnapshot(); err == nil {
					tasksSnap = raw
					sum := sha256.Sum256(raw)
					tasksHash = hex.EncodeToString(sum[:8])
				}
			}
			return authHash, authSnap, tasksHash, tasksSnap
		},
		func(authSnap []byte, tasksSnap []byte) error {
			if len(authSnap) > 0 && s.auth != nil {
				if err := s.auth.ImportSnapshot(authSnap); err != nil {
					return err
				}
			}
			if len(tasksSnap) > 0 {
				if tm := s.getTasks(); tm != nil {
					if err := tm.ImportSnapshot(tasksSnap); err != nil {
						return err
					}
				}
			}
			return nil
		},
		func(kind, reason, newEntryURL string, entryURLs []string, epoch uint64, topo *cluster.ClientTopology) {
			if s.events == nil {
				return
			}
			s.events.Publish(FileChangeEvent{
				Kind:      kind,
				Reason:    reason,
				NodeName:  s.cluster.NodeName(),
				EntryURL:  newEntryURL,
				EntryURLs: entryURLs,
				Epoch:     epoch,
				Topology:  topo,
			})
		},
	)
}

func (s *Server) handleClientTopology(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cluster.BuildClientTopology())
}

func (s *Server) handleClientUpdateCheck(w http.ResponseWriter, r *http.Request) {
	upd := s.getUpdater()
	if upd == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "更新服务尚未初始化"})
		return
	}
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	curVer := strings.TrimSpace(r.URL.Query().Get("current_version"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := upd.CheckClientUpdate(ctx, platform, curVer)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleClientUpdateDownload(w http.ResponseWriter, r *http.Request) {
	upd := s.getUpdater()
	if upd == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "更新服务尚未初始化"})
		return
	}
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
	defer cancel()
	localPath, info, err := upd.DownloadCachedClientAsset(ctx, platform)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	fileName := filepath.Base(localPath)
	if info.LatestVersion != "" {
		w.Header().Set("X-QuailNest-Version", info.LatestVersion)
	}
	if info.SHA256 != "" {
		w.Header().Set("X-QuailNest-SHA256", info.SHA256)
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	if strings.HasSuffix(strings.ToLower(fileName), ".apk") {
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	}
	http.ServeFile(w, r, localPath)
}

func (s *Server) handleAdminClusterHA(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var req struct {
		AutoFailover           *bool   `json:"auto_failover"`
		PreferredStandbyNodeID *string `json:"preferred_standby_node_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	autoEnabled := s.cluster.AutoFailoverEnabled()
	if req.AutoFailover != nil {
		autoEnabled = *req.AutoFailover
	}
	preferred := s.cluster.PreferredStandbyNodeID()
	if req.PreferredStandbyNodeID != nil {
		preferred = strings.TrimSpace(*req.PreferredStandbyNodeID)
	}
	if err := s.cluster.SetHAConfig(autoEnabled, preferred); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.logAudit(r, u.Username, "cluster_ha_config", preferred, "ok", fmt.Sprintf("auto_failover=%v", autoEnabled))
	topo := s.cluster.BuildClientTopology()
	s.PublishEvent(FileChangeEvent{
		Kind:      "topology_updated",
		NodeName:  s.cluster.NodeName(),
		EntryURL:  s.cluster.EntryURL(),
		EntryURLs: topo.ActiveEntryURLs,
		Epoch:     topo.Epoch,
		Topology:  &topo,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"topology": topo,
		"pool":     s.cluster.PoolSummaryForRole(true),
	})
}

func (s *Server) handleClusterHAStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cluster.HAStatus())
}

func (s *Server) handleClusterStateSnapshot(w http.ResponseWriter, r *http.Request) {
	var authHash string
	var authSnap []byte
	if s.auth != nil {
		raw, err := s.auth.ExportSnapshot()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		authSnap = raw
		sum := sha256.Sum256(raw)
		authHash = hex.EncodeToString(sum[:8])
	}

	var tasksHash string
	var tasksSnap []byte
	if tm := s.getTasks(); tm != nil {
		raw, err := tm.ExportSnapshot()
		if err == nil {
			tasksSnap = raw
			sum := sha256.Sum256(raw)
			tasksHash = hex.EncodeToString(sum[:8])
		}
	}

	writeJSON(w, http.StatusOK, cluster.StateSnapshotPayload{
		OK:            true,
		AuthHash:      authHash,
		TasksHash:     tasksHash,
		AuthSnapshot:  authSnap,
		TasksSnapshot: tasksSnap,
	})
}

func (s *Server) handleClusterFailbackPrepare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClusterSecret string `json:"cluster_secret"`
		NewEpoch      uint64 `json:"new_epoch"`
		AuthSnapshot  []byte `json:"auth_snapshot,omitempty"`
		TasksSnapshot []byte `json:"tasks_snapshot,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	entryURL, entryURLs, err := s.cluster.AcceptFailback(req.ClusterSecret, req.NewEpoch, req.AuthSnapshot, req.TasksSnapshot)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	topo := s.cluster.BuildClientTopology()
	s.PublishEvent(FileChangeEvent{
		Kind:      "topology_updated",
		Reason:    "primary_restored",
		NodeName:  s.cluster.NodeName(),
		EntryURL:  entryURL,
		EntryURLs: entryURLs,
		Epoch:     topo.Epoch,
		Topology:  &topo,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"entry_url":  entryURL,
		"entry_urls": entryURLs,
		"epoch":      topo.Epoch,
	})
}

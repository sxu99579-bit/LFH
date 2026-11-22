package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager manages installed plugins, marketplace definitions, concurrency slots, and local execution.
type Manager struct {
	mu        sync.RWMutex
	rootDir   string
	installed map[string]Manifest
	busyJobs  map[string]int
}

// NewManager initializes the plugin manager from <dataDir>/plugins.
func NewManager(dataDir string) (*Manager, error) {
	rootDir := filepath.Join(dataDir, "plugins")
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, err
	}
	m := &Manager{
		rootDir:   rootDir,
		installed: make(map[string]Manifest),
		busyJobs:  make(map[string]int),
	}
	m.loadInstalledLocked()
	return m, nil
}

func isValidPluginID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (m *Manager) loadInstalledLocked() {
	entries, err := os.ReadDir(m.rootDir)
	if err != nil {
		return
	}
	for _, ent := range entries {
		if !ent.IsDir() || !isValidPluginID(ent.Name()) {
			continue
		}
		manifestPath := filepath.Join(m.rootDir, ent.Name(), "plugin.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			continue
		}
		var mf Manifest
		if json.Unmarshal(raw, &mf) == nil && isValidPluginID(mf.ID) {
			if mf.MaxConcurrency <= 0 {
				mf.MaxConcurrency = 2
			}
			m.installed[mf.ID] = mf
		}
	}
}

func (m *Manager) saveManifestLocked(mf Manifest) error {
	pDir := filepath.Join(m.rootDir, mf.ID)
	if err := os.MkdirAll(pDir, 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pDir, "plugin.json"), raw, 0644)
}

// Marketplace returns all catalog entries available for installation.
func (m *Manager) Marketplace() []MarketplaceItem {
	return builtinMarketplaceCatalog()
}

// ListInstalled returns sorted plugin manifests installed on this node.
func (m *Manager) ListInstalled() []Manifest {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Manifest, 0, len(m.installed))
	for _, mf := range m.installed {
		out = append(out, mf)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// Capabilities returns active capability summaries suitable for cluster heartbeats.
func (m *Manager) Capabilities() []CapabilitySummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CapabilitySummary, 0, len(m.installed))
	for _, mf := range m.installed {
		out = append(out, CapabilitySummary{
			PluginID:       mf.ID,
			Name:           mf.Name,
			Version:        mf.Version,
			Capabilities:   append([]string(nil), mf.Capabilities...),
			SupportedExts:  append([]string(nil), mf.SupportedExts...),
			TriggerModes:   append([]string(nil), mf.TriggerModes...),
			Enabled:        mf.Enabled,
			MaxConcurrency: mf.MaxConcurrency,
			BusyJobs:       m.busyJobs[mf.ID],
			HasGPU:         mf.HasGPU,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PluginID < out[j].PluginID
	})
	return out
}

// InstallFromMarketplace installs or upgrades a catalog plugin on this node and enables it.
func (m *Manager) InstallFromMarketplace(pluginID string) (Manifest, error) {
	id := strings.TrimSpace(strings.ToLower(pluginID))
	var chosen *MarketplaceItem
	for _, item := range builtinMarketplaceCatalog() {
		if item.ID == id {
			cp := item
			chosen = &cp
			break
		}
	}
	if chosen == nil {
		return Manifest{}, fmt.Errorf("插件市场中未找到插件 %q", pluginID)
	}
	mf := Manifest{
		ID:             chosen.ID,
		Name:           chosen.Name,
		Version:        chosen.Version,
		Author:         chosen.Author,
		Description:    chosen.Description,
		Capabilities:   append([]string(nil), chosen.Capabilities...),
		SupportedExts:  append([]string(nil), chosen.SupportedExts...),
		TriggerModes:   append([]string(nil), chosen.TriggerModes...),
		Runtime:        chosen.Runtime,
		MaxConcurrency: chosen.MaxConcurrency,
		Enabled:        true,
		InstalledAt:    time.Now(),
	}
	return m.InstallCustom(mf)
}

// InstallCustom validates, saves, and registers a plugin manifest.
func (m *Manager) InstallCustom(mf Manifest) (Manifest, error) {
	mf.ID = strings.TrimSpace(strings.ToLower(mf.ID))
	if !isValidPluginID(mf.ID) {
		return Manifest{}, errors.New("插件 ID 仅支持小写字母、数字、连字符和下划线")
	}
	if len(mf.Capabilities) == 0 {
		return Manifest{}, errors.New("插件必须至少声明一个能力标识 (capabilities)")
	}
	if mf.Runtime == "" {
		mf.Runtime = RuntimeBuiltin
	}
	if mf.MaxConcurrency <= 0 {
		mf.MaxConcurrency = 2
	}
	if mf.InstalledAt.IsZero() {
		mf.InstalledAt = time.Now()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.saveManifestLocked(mf); err != nil {
		return Manifest{}, err
	}
	m.installed[mf.ID] = mf
	return mf, nil
}

// SetEnabled toggles an installed plugin on or off.
func (m *Manager) SetEnabled(pluginID string, enabled bool) (Manifest, error) {
	id := strings.TrimSpace(strings.ToLower(pluginID))
	m.mu.Lock()
	defer m.mu.Unlock()
	mf, ok := m.installed[id]
	if !ok {
		return Manifest{}, fmt.Errorf("插件 %q 尚未在本节点安装", pluginID)
	}
	mf.Enabled = enabled
	if err := m.saveManifestLocked(mf); err != nil {
		return Manifest{}, err
	}
	m.installed[id] = mf
	return mf, nil
}

// Uninstall removes an installed plugin from disk and memory.
func (m *Manager) Uninstall(pluginID string) error {
	id := strings.TrimSpace(strings.ToLower(pluginID))
	if !isValidPluginID(id) {
		return errors.New("无效的插件 ID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.installed[id]; !ok {
		return fmt.Errorf("插件 %q 尚未安装", pluginID)
	}
	if m.busyJobs[id] > 0 {
		return fmt.Errorf("插件 %q 正在处理 %d 个任务，请稍后卸载", id, m.busyJobs[id])
	}
	if err := os.RemoveAll(filepath.Join(m.rootDir, id)); err != nil {
		return err
	}
	delete(m.installed, id)
	return nil
}

// MatchExtension reports whether supportedExts accepts ext (or wildcard "*").
func MatchExtension(supportedExts []string, fileName string) bool {
	if len(supportedExts) == 0 {
		return true
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	for _, s := range supportedExts {
		norm := strings.ToLower(strings.TrimSpace(s))
		if norm == "*" || norm == ".*" {
			return true
		}
		if !strings.HasPrefix(norm, ".") {
			norm = "." + norm
		}
		if norm == ext {
			return true
		}
	}
	return false
}

// Execute runs a matching enabled plugin locally inside an isolated temporary work directory if WorkDir is omitted.
func (m *Manager) Execute(ctx context.Context, req ExecRequest) (ExecResponse, Manifest, error) {
	m.mu.Lock()
	var chosen *Manifest
	reqID := strings.TrimSpace(strings.ToLower(req.PluginID))
	reqCap := strings.TrimSpace(req.Capability)
	if reqID != "" {
		if mf, ok := m.installed[reqID]; ok && mf.Enabled {
			cp := mf
			chosen = &cp
		}
	} else if reqCap != "" {
		var keys []string
		for k := range m.installed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			mf := m.installed[k]
			if !mf.Enabled {
				continue
			}
			for _, c := range mf.Capabilities {
				if c == reqCap {
					cp := mf
					chosen = &cp
					break
				}
			}
			if chosen != nil {
				break
			}
		}
	}
	if chosen == nil {
		m.mu.Unlock()
		return ExecResponse{}, Manifest{}, errors.New("当前节点未安装或未启用匹配的算力插件")
	}
	if !MatchExtension(chosen.SupportedExts, req.FileName) {
		m.mu.Unlock()
		return ExecResponse{}, *chosen, fmt.Errorf("插件 %s 不支持处理格式 %s", chosen.Name, filepath.Ext(req.FileName))
	}
	if m.busyJobs[chosen.ID] >= chosen.MaxConcurrency {
		m.mu.Unlock()
		return ExecResponse{}, *chosen, fmt.Errorf("插件 %s 并发槽位已满 (%d/%d)", chosen.Name, m.busyJobs[chosen.ID], chosen.MaxConcurrency)
	}
	m.busyJobs[chosen.ID]++
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		if m.busyJobs[chosen.ID] > 0 {
			m.busyJobs[chosen.ID]--
		}
		m.mu.Unlock()
	}()

	if req.WorkDir == "" {
		return ExecResponse{}, *chosen, errors.New("必须指定插件工作目录")
	}
	if err := os.MkdirAll(req.WorkDir, 0755); err != nil {
		return ExecResponse{}, *chosen, err
	}
	req.PluginID = chosen.ID

	var res ExecResponse
	var execErr error
	switch chosen.Runtime {
	case RuntimeExec:
		res, execErr = runExecPlugin(ctx, *chosen, req)
	case RuntimeBuiltin, "":
		res, execErr = runBuiltinPlugin(ctx, *chosen, req)
	default:
		execErr = fmt.Errorf("不支持的插件运行时 %s (%s)", chosen.Runtime, runtime.GOOS)
	}
	return res, *chosen, execErr
}

package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	ScopeRead   = "read"
	ScopeWrite  = "write"
	ScopeShare  = "share"
	ScopeVerify = "verify"
	ScopeDelete = "delete"

	AgentTokenPrefix            = "lfh_ag_"
	DefaultAgentRateLimitPerMin = 180
	MaxAgentRateLimitPerMin     = 6000
)

var (
	ErrUnauthenticated = errors.New("未登录或凭据无效")
	ErrRateLimited     = errors.New("Agent 密钥请求频率超出限制，请稍后重试")
)

// AgentKey stores scoped non-human credentials in users.json using only SHA-256 token digests.
type AgentKey struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	OwnerUsername       string     `json:"owner_username"`
	TokenHash           string     `json:"token_hash"`
	TokenPrefix         string     `json:"token_prefix"`
	Scopes              []string   `json:"scopes"`
	ReadFolders         []string   `json:"read_folders,omitempty"`
	WriteFolders        []string   `json:"write_folders,omitempty"`
	DefaultAllowedUsers []string   `json:"default_allowed_users,omitempty"`
	AllowSetVisibility  bool       `json:"allow_set_visibility"`
	RateLimitPerMin     int        `json:"rate_limit_per_min"`
	Disabled            bool       `json:"disabled,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP          string     `json:"last_used_ip,omitempty"`
}

// PublicAgentKey is the safe view of an AgentKey with TokenHash omitted.
type PublicAgentKey struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	OwnerUsername       string     `json:"owner_username"`
	TokenPrefix         string     `json:"token_prefix"`
	Scopes              []string   `json:"scopes"`
	ReadFolders         []string   `json:"read_folders,omitempty"`
	WriteFolders        []string   `json:"write_folders,omitempty"`
	DefaultAllowedUsers []string   `json:"default_allowed_users,omitempty"`
	AllowSetVisibility  bool       `json:"allow_set_visibility"`
	RateLimitPerMin     int        `json:"rate_limit_per_min"`
	Disabled            bool       `json:"disabled,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	LastUsedAt          *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP          string     `json:"last_used_ip,omitempty"`
}

type CreateAgentKeyInput struct {
	Name                string     `json:"name"`
	OwnerUsername       string     `json:"owner_username"`
	Scopes              []string   `json:"scopes"`
	ReadFolders         []string   `json:"read_folders"`
	WriteFolders        []string   `json:"write_folders"`
	DefaultAllowedUsers []string   `json:"default_allowed_users"`
	AllowSetVisibility  bool       `json:"allow_set_visibility"`
	RateLimitPerMin     int        `json:"rate_limit_per_min"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
}

type UpdateAgentKeyInput struct {
	Name                *string    `json:"name,omitempty"`
	Scopes              *[]string  `json:"scopes,omitempty"`
	ReadFolders         *[]string  `json:"read_folders,omitempty"`
	WriteFolders        *[]string  `json:"write_folders,omitempty"`
	DefaultAllowedUsers *[]string  `json:"default_allowed_users,omitempty"`
	AllowSetVisibility  *bool      `json:"allow_set_visibility,omitempty"`
	RateLimitPerMin     *int       `json:"rate_limit_per_min,omitempty"`
	Disabled            *bool      `json:"disabled,omitempty"`
	ExpiresAt           *time.Time `json:"expires_at,omitempty"`
	ClearExpiresAt      bool       `json:"clear_expires_at,omitempty"`
}

// Principal identifies the authenticated caller (human session/personal token or scoped AgentKey).
type Principal struct {
	User     PublicUser
	IsAgent  bool
	AgentKey *PublicAgentKey
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := *t
	return &v
}

func cloneStrings(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	out := make([]string, len(src))
	copy(out, src)
	return out
}

func toPublicAgentKey(k AgentKey) PublicAgentKey {
	return PublicAgentKey{
		ID:                  k.ID,
		Name:                k.Name,
		OwnerUsername:       k.OwnerUsername,
		TokenPrefix:         k.TokenPrefix,
		Scopes:              cloneStrings(k.Scopes),
		ReadFolders:         cloneStrings(k.ReadFolders),
		WriteFolders:        cloneStrings(k.WriteFolders),
		DefaultAllowedUsers: cloneStrings(k.DefaultAllowedUsers),
		AllowSetVisibility:  k.AllowSetVisibility,
		RateLimitPerMin:     k.RateLimitPerMin,
		Disabled:            k.Disabled,
		ExpiresAt:           cloneTimePtr(k.ExpiresAt),
		CreatedAt:           k.CreatedAt,
		LastUsedAt:          cloneTimePtr(k.LastUsedAt),
		LastUsedIP:          k.LastUsedIP,
	}
}

func hashAgentToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func defaultAgentScopes() []string {
	return []string{ScopeRead, ScopeWrite, ScopeShare}
}

func isValidAgentScope(s string) bool {
	switch s {
	case ScopeRead, ScopeWrite, ScopeShare, ScopeVerify, ScopeDelete:
		return true
	default:
		return false
	}
}

// ValidateScopes normalizes, deduplicates, and validates AgentKey scopes.
func ValidateScopes(raw []string, allowDefault bool) ([]string, error) {
	if len(raw) == 0 {
		if allowDefault {
			return defaultAgentScopes(), nil
		}
		return nil, errors.New("Agent 密钥至少需要包含一项权限")
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s := strings.ToLower(strings.TrimSpace(item))
		if s == "" {
			continue
		}
		if !isValidAgentScope(s) {
			return nil, errors.New("无效的 Agent 权限标识: " + s)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		if allowDefault {
			return defaultAgentScopes(), nil
		}
		return nil, errors.New("Agent 密钥至少需要包含一项权限")
	}
	return out, nil
}

func normalizeSingleFolderRule(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	trimmed = strings.Trim(trimmed, "/")
	if trimmed == "*" {
		return "*", nil
	}
	if trimmed == "" {
		return "", nil
	}
	rawParts := strings.Split(trimmed, "/")
	parts := make([]string, 0, len(rawParts))
	for _, seg := range rawParts {
		seg = strings.TrimSpace(seg)
		if seg == ".." {
			return "", errors.New("目录规则不能包含上级路径 ..")
		}
		if seg == "" || seg == "." {
			continue
		}
		if seg == "*" {
			return "", errors.New("通配符仅支持单独填写 *")
		}
		parts = append(parts, seg)
	}
	return strings.Join(parts, "/"), nil
}

// ValidateFolderRules normalizes and case-insensitively deduplicates folder allowlist rules.
func ValidateFolderRules(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		norm, err := normalizeSingleFolderRule(item)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(norm)
		if !seen[key] {
			seen[key] = true
			out = append(out, norm)
		}
	}
	return out, nil
}

// FolderMatchesPrefix enforces segment-boundary matching so "shopapp/back" never matches "shopapp/backend".
func FolderMatchesPrefix(targetFolder, allowedPrefix string) bool {
	target, err := normalizeSingleFolderRule(targetFolder)
	if err != nil || target == "*" {
		return false
	}
	trimmedPrefix := strings.TrimSpace(strings.ReplaceAll(allowedPrefix, "\\", "/"))
	trimmedPrefix = strings.Trim(trimmedPrefix, "/")
	if trimmedPrefix == "*" {
		return true
	}
	prefix, err := normalizeSingleFolderRule(trimmedPrefix)
	if err != nil {
		return false
	}
	if prefix == "" {
		return target == ""
	}
	tLow := strings.ToLower(target)
	pLow := strings.ToLower(prefix)
	return tLow == pLow || strings.HasPrefix(tLow, pLow+"/")
}

// HasScope reports whether the Principal grants the given capability scope.
func (p Principal) HasScope(scope string) bool {
	if !p.IsAgent {
		return true
	}
	if p.AgentKey == nil {
		return false
	}
	for _, s := range p.AgentKey.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// CanReadFolder checks read permission and folder allowlist (WriteFolders implicitly grants read).
func (p Principal) CanReadFolder(folderPath string) bool {
	if !p.IsAgent {
		return true
	}
	if !p.HasScope(ScopeRead) {
		return false
	}
	for _, prefix := range p.AgentKey.ReadFolders {
		if FolderMatchesPrefix(folderPath, prefix) {
			return true
		}
	}
	for _, prefix := range p.AgentKey.WriteFolders {
		if FolderMatchesPrefix(folderPath, prefix) {
			return true
		}
	}
	return false
}

// CanWriteFolder checks write permission against WriteFolders.
func (p Principal) CanWriteFolder(folderPath string) bool {
	if !p.IsAgent {
		return true
	}
	if !p.HasScope(ScopeWrite) {
		return false
	}
	for _, prefix := range p.AgentKey.WriteFolders {
		if FolderMatchesPrefix(folderPath, prefix) {
			return true
		}
	}
	return false
}

// CanDeleteFolder checks explicit delete scope against WriteFolders.
func (p Principal) CanDeleteFolder(folderPath string) bool {
	if !p.IsAgent {
		return true
	}
	if !p.HasScope(ScopeDelete) {
		return false
	}
	for _, prefix := range p.AgentKey.WriteFolders {
		if FolderMatchesPrefix(folderPath, prefix) {
			return true
		}
	}
	return false
}

// CanShareFolder checks share scope, AllowSetVisibility, and folder scope.
func (p Principal) CanShareFolder(folderPath string) bool {
	if !p.IsAgent {
		return true
	}
	if !p.HasScope(ScopeShare) || !p.AgentKey.AllowSetVisibility {
		return false
	}
	for _, prefix := range p.AgentKey.WriteFolders {
		if FolderMatchesPrefix(folderPath, prefix) {
			return true
		}
	}
	return false
}

// ResolveAllowedUsers computes effective allowed_users for uploads initiated by this Principal.
func (p Principal) ResolveAllowedUsers(requested []string) ([]string, error) {
	if !p.IsAgent {
		return cloneStrings(requested), nil
	}
	if p.AgentKey == nil {
		return nil, ErrUnauthenticated
	}
	cleanedReq := normalizeUserList(requested)
	if !p.AgentKey.AllowSetVisibility && len(cleanedReq) > 0 {
		return nil, errors.New("当前 Agent 密钥禁止自定义文件可见成员")
	}

	var chosen []string
	if len(cleanedReq) > 0 {
		chosen = cleanedReq
	} else {
		chosen = normalizeUserList(p.AgentKey.DefaultAllowedUsers)
	}
	if len(chosen) == 0 {
		return nil, nil
	}

	// Always include key owner when visibility is restricted so the owner never loses access.
	seen := make(map[string]bool, len(chosen)+1)
	out := make([]string, 0, len(chosen)+1)
	for _, u := range chosen {
		if !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if p.User.Username != "" && !seen[p.User.Username] {
		out = append(out, p.User.Username)
	}
	return out, nil
}

func (p Principal) ActorLabel() string {
	if p.IsAgent && p.AgentKey != nil {
		return "Agent · " + p.AgentKey.Name
	}
	return ""
}

func (p Principal) ActorFingerprint() string {
	if p.IsAgent && p.AgentKey != nil {
		return "AGENT-" + strings.ToUpper(p.AgentKey.ID)
	}
	return ""
}

func normalizeUserList(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		u := strings.TrimSpace(item)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func normalizeRateLimit(v int) int {
	if v <= 0 {
		return DefaultAgentRateLimitPerMin
	}
	if v > MaxAgentRateLimitPerMin {
		return MaxAgentRateLimitPerMin
	}
	return v
}

func (m *Manager) resetAgentRates() {
	m.agentRateMu.Lock()
	m.agentRates = make(map[string][]time.Time)
	m.agentRateMu.Unlock()
}

func (m *Manager) clearAgentRateBucket(id string) {
	m.agentRateMu.Lock()
	delete(m.agentRates, id)
	m.agentRateMu.Unlock()
}

func (m *Manager) checkAgentRateLimit(keyID string, limitPerMin int, now time.Time) bool {
	if limitPerMin <= 0 {
		limitPerMin = DefaultAgentRateLimitPerMin
	}
	cutoff := now.Add(-time.Minute)
	m.agentRateMu.Lock()
	defer m.agentRateMu.Unlock()

	hist := m.agentRates[keyID]
	kept := hist[:0]
	for _, ts := range hist {
		if ts.After(cutoff) {
			kept = append(kept, ts)
		}
	}
	if len(kept) >= limitPerMin {
		m.agentRates[keyID] = kept
		return false
	}
	kept = append(kept, now)
	m.agentRates[keyID] = kept
	return true
}

func (m *Manager) validateUsersLocked(raw []string) ([]string, error) {
	cleaned := normalizeUserList(raw)
	if len(cleaned) == 0 {
		return nil, nil
	}
	for _, u := range cleaned {
		if _, ok := m.users[u]; !ok {
			return nil, errors.New("指定的用户不存在: " + u)
		}
	}
	sort.Strings(cleaned)
	return cleaned, nil
}

// CreateAgentKey creates a scoped AgentKey and returns the plaintext token once alongside the public view.
func (m *Manager) CreateAgentKey(in CreateAgentKeyInput) (string, PublicAgentKey, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return "", PublicAgentKey{}, errors.New("Agent 密钥名称不能为空")
	}
	if len([]rune(name)) > 80 {
		name = string([]rune(name)[:80])
	}
	ownerName := strings.TrimSpace(in.OwnerUsername)
	if ownerName == "" {
		return "", PublicAgentKey{}, errors.New("归属用户不能为空")
	}
	scopes, err := ValidateScopes(in.Scopes, true)
	if err != nil {
		return "", PublicAgentKey{}, err
	}
	readFolders, err := ValidateFolderRules(in.ReadFolders)
	if err != nil {
		return "", PublicAgentKey{}, err
	}
	writeFolders, err := ValidateFolderRules(in.WriteFolders)
	if err != nil {
		return "", PublicAgentKey{}, err
	}

	rawTokenBytes := make([]byte, 24)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return "", PublicAgentKey{}, err
	}
	plainToken := AgentTokenPrefix + hex.EncodeToString(rawTokenBytes)
	tokenHash := hashAgentToken(plainToken)
	tokenPrefix := plainToken[:15] + "..."

	m.mu.Lock()
	defer m.mu.Unlock()

	owner, ok := m.users[ownerName]
	if !ok {
		return "", PublicAgentKey{}, errors.New("归属用户不存在")
	}
	if owner.Status != "" && owner.Status != StatusActive {
		return "", PublicAgentKey{}, errors.New("仅可为正常状态的账号创建 Agent 密钥")
	}
	defaultAllowed, err := m.validateUsersLocked(in.DefaultAllowedUsers)
	if err != nil {
		return "", PublicAgentKey{}, err
	}

	var keyID string
	for attempt := 0; attempt < 8; attempt++ {
		idHex, idErr := randomHex(8)
		if idErr != nil {
			return "", PublicAgentKey{}, idErr
		}
		candidate := "agk_" + idHex
		if _, exists := m.agentKeys[candidate]; !exists {
			keyID = candidate
			break
		}
	}
	if keyID == "" {
		return "", PublicAgentKey{}, errors.New("生成 Agent 密钥 ID 失败")
	}

	ak := AgentKey{
		ID:                  keyID,
		Name:                name,
		OwnerUsername:       owner.Username,
		TokenHash:           tokenHash,
		TokenPrefix:         tokenPrefix,
		Scopes:              scopes,
		ReadFolders:         readFolders,
		WriteFolders:        writeFolders,
		DefaultAllowedUsers: defaultAllowed,
		AllowSetVisibility:  in.AllowSetVisibility,
		RateLimitPerMin:     normalizeRateLimit(in.RateLimitPerMin),
		ExpiresAt:           cloneTimePtr(in.ExpiresAt),
		CreatedAt:           time.Now(),
	}
	m.agentKeys[keyID] = ak
	if err := m.saveLocked(); err != nil {
		delete(m.agentKeys, keyID)
		return "", PublicAgentKey{}, err
	}
	return plainToken, toPublicAgentKey(ak), nil
}

// ListAgentKeys returns public AgentKey views ordered newest first, optionally filtered by owner.
func (m *Manager) ListAgentKeys(ownerFilter string) []PublicAgentKey {
	filter := strings.TrimSpace(ownerFilter)
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]PublicAgentKey, 0, len(m.agentKeys))
	for _, ak := range m.agentKeys {
		if filter != "" && ak.OwnerUsername != filter {
			continue
		}
		out = append(out, toPublicAgentKey(ak))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Manager) GetAgentKey(id string) (PublicAgentKey, bool) {
	cleanID := strings.TrimSpace(id)
	m.mu.RLock()
	defer m.mu.RUnlock()
	ak, ok := m.agentKeys[cleanID]
	if !ok {
		return PublicAgentKey{}, false
	}
	return toPublicAgentKey(ak), true
}

func canManageAgentKey(ak AgentKey, actorUsername, actorRole string) bool {
	return actorRole == "admin" || ak.OwnerUsername == strings.TrimSpace(actorUsername)
}

func (m *Manager) UpdateAgentKey(id, actorUsername, actorRole string, in UpdateAgentKeyInput) (PublicAgentKey, error) {
	cleanID := strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()

	ak, ok := m.agentKeys[cleanID]
	if !ok {
		return PublicAgentKey{}, errors.New("Agent 密钥不存在")
	}
	if !canManageAgentKey(ak, actorUsername, actorRole) {
		return PublicAgentKey{}, errors.New("无权修改该 Agent 密钥")
	}

	next := ak
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return PublicAgentKey{}, errors.New("Agent 密钥名称不能为空")
		}
		if len([]rune(name)) > 80 {
			name = string([]rune(name)[:80])
		}
		next.Name = name
	}
	if in.Scopes != nil {
		scopes, err := ValidateScopes(*in.Scopes, false)
		if err != nil {
			return PublicAgentKey{}, err
		}
		next.Scopes = scopes
	}
	if in.ReadFolders != nil {
		rf, err := ValidateFolderRules(*in.ReadFolders)
		if err != nil {
			return PublicAgentKey{}, err
		}
		next.ReadFolders = rf
	}
	if in.WriteFolders != nil {
		wf, err := ValidateFolderRules(*in.WriteFolders)
		if err != nil {
			return PublicAgentKey{}, err
		}
		next.WriteFolders = wf
	}
	if in.DefaultAllowedUsers != nil {
		au, err := m.validateUsersLocked(*in.DefaultAllowedUsers)
		if err != nil {
			return PublicAgentKey{}, err
		}
		next.DefaultAllowedUsers = au
	}
	if in.AllowSetVisibility != nil {
		next.AllowSetVisibility = *in.AllowSetVisibility
	}
	if in.RateLimitPerMin != nil {
		next.RateLimitPerMin = normalizeRateLimit(*in.RateLimitPerMin)
	}
	if in.Disabled != nil {
		next.Disabled = *in.Disabled
	}
	if in.ClearExpiresAt {
		next.ExpiresAt = nil
	} else if in.ExpiresAt != nil {
		next.ExpiresAt = cloneTimePtr(in.ExpiresAt)
	}

	m.agentKeys[cleanID] = next
	if err := m.saveLocked(); err != nil {
		m.agentKeys[cleanID] = ak
		return PublicAgentKey{}, err
	}
	return toPublicAgentKey(next), nil
}

func (m *Manager) SetAgentKeyDisabled(id, actorUsername, actorRole string, disabled bool) (PublicAgentKey, error) {
	return m.UpdateAgentKey(id, actorUsername, actorRole, UpdateAgentKeyInput{Disabled: &disabled})
}

func (m *Manager) DeleteAgentKey(id, actorUsername, actorRole string) error {
	cleanID := strings.TrimSpace(id)
	m.mu.Lock()
	defer m.mu.Unlock()

	ak, ok := m.agentKeys[cleanID]
	if !ok {
		return errors.New("Agent 密钥不存在")
	}
	if !canManageAgentKey(ak, actorUsername, actorRole) {
		return errors.New("无权删除该 Agent 密钥")
	}
	delete(m.agentKeys, cleanID)
	if err := m.saveLocked(); err != nil {
		m.agentKeys[cleanID] = ak
		return err
	}
	m.clearAgentRateBucket(cleanID)
	return nil
}

// AuthenticateAgentToken validates an lfh_ag_ token, enforces status/expiry/rate-limit,
// and updates LastUsedAt/LastUsedIP in memory (without disk write amplification on hot path).
func (m *Manager) AuthenticateAgentToken(token, clientIP string) (Principal, error) {
	clean := strings.TrimSpace(token)
	if !strings.HasPrefix(clean, AgentTokenPrefix) || len(clean) <= len(AgentTokenPrefix) {
		return Principal{}, ErrUnauthenticated
	}
	digest := hashAgentToken(clean)

	m.mu.RLock()
	var matched AgentKey
	var owner User
	found := false
	for _, ak := range m.agentKeys {
		if len(ak.TokenHash) == len(digest) && subtle.ConstantTimeCompare([]byte(ak.TokenHash), []byte(digest)) == 1 {
			matched = ak
			var ok bool
			owner, ok = m.users[ak.OwnerUsername]
			found = ok
			break
		}
	}
	m.mu.RUnlock()

	if !found || matched.Disabled {
		return Principal{}, ErrUnauthenticated
	}
	if owner.Status != "" && owner.Status != StatusActive {
		return Principal{}, ErrUnauthenticated
	}
	now := time.Now()
	if matched.ExpiresAt != nil && !now.Before(*matched.ExpiresAt) {
		return Principal{}, ErrUnauthenticated
	}
	if !m.checkAgentRateLimit(matched.ID, matched.RateLimitPerMin, now) {
		return Principal{}, ErrRateLimited
	}

	cleanIP := strings.TrimSpace(clientIP)
	needTouch := matched.LastUsedAt == nil || now.Sub(*matched.LastUsedAt) >= 5*time.Second || (cleanIP != "" && matched.LastUsedIP != cleanIP)
	if needTouch {
		m.mu.Lock()
		if cur, ok := m.agentKeys[matched.ID]; ok {
			if cur.LastUsedAt == nil || now.Sub(*cur.LastUsedAt) >= 5*time.Second || (cleanIP != "" && cur.LastUsedIP != cleanIP) {
				used := now
				cur.LastUsedAt = &used
				if cleanIP != "" {
					cur.LastUsedIP = cleanIP
				}
				m.agentKeys[matched.ID] = cur
			}
			matched = m.agentKeys[matched.ID]
		}
		m.mu.Unlock()
	}

	pub := toPublicAgentKey(matched)
	return Principal{
		User:     toPublicUser(owner),
		IsAgent:  true,
		AgentKey: &pub,
	}, nil
}

func extractClientIPFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
		return normalizeClientIP(xr)
	}
	if xf := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xf != "" {
		first := strings.TrimSpace(strings.Split(xf, ",")[0])
		if first != "" {
			return normalizeClientIP(first)
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		return normalizeClientIP(strings.TrimSpace(r.RemoteAddr))
	}
	return normalizeClientIP(host)
}

func normalizeClientIP(ip string) string {
	if ip == "::1" {
		return "127.0.0.1"
	}
	return strings.TrimPrefix(ip, "::ffff:")
}

// CurrentPrincipal resolves either a human session/personal token or a scoped AgentKey.
func (m *Manager) CurrentPrincipal(r *http.Request) (Principal, error) {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		parts := strings.Split(c.Value, ".")
		if len(parts) == 2 {
			expectedSig := m.sign(parts[0])
			if hmac.Equal([]byte(parts[1]), []byte(expectedSig)) {
				if raw, decErr := base64.RawURLEncoding.DecodeString(parts[0]); decErr == nil {
					var payload sessionPayload
					if err := json.Unmarshal(raw, &payload); err == nil && time.Now().Unix() <= payload.Exp {
						m.mu.RLock()
						u, ok := m.users[payload.Username]
						m.mu.RUnlock()
						if ok && (u.Status == "" || u.Status == StatusActive) {
							return Principal{User: toPublicUser(u)}, nil
						}
					}
				}
			}
		}
	}

	tok := extractRequestToken(r)
	if tok == "" {
		return Principal{}, ErrUnauthenticated
	}
	if strings.HasPrefix(tok, AgentTokenPrefix) {
		return m.AuthenticateAgentToken(tok, extractClientIPFromRequest(r))
	}
	u, ok := m.AuthenticateToken(tok)
	if !ok {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{User: u}, nil
}

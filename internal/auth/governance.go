package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type AdminCreateUserInput struct {
	Username           string     `json:"username"`
	Password           string     `json:"password"`
	Role               string     `json:"role"` // admin | user | readonly
	DisplayName        string     `json:"display_name"`
	Note               string     `json:"note"`
	QuotaBytes         int64      `json:"quota_bytes"`
	AllowedFolders     []string   `json:"allowed_folders"`
	CanUpload          *bool      `json:"can_upload,omitempty"`
	CanManageFolders   *bool      `json:"can_manage_folders,omitempty"`
	CanUseAgents       *bool      `json:"can_use_agents,omitempty"`
	MustChangePassword bool       `json:"must_change_password"`
	ExpireHours        int        `json:"expire_hours"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
}

type AdminUpdateUserInput struct {
	DisplayName        *string    `json:"display_name,omitempty"`
	Note               *string    `json:"note,omitempty"`
	Role               *string    `json:"role,omitempty"`
	QuotaBytes         *int64     `json:"quota_bytes,omitempty"`
	AllowedFolders     *[]string  `json:"allowed_folders,omitempty"`
	CanUpload          *bool      `json:"can_upload,omitempty"`
	CanManageFolders   *bool      `json:"can_manage_folders,omitempty"`
	CanUseAgents       *bool      `json:"can_use_agents,omitempty"`
	MustChangePassword *bool      `json:"must_change_password,omitempty"`
	ExpireHours        *int       `json:"expire_hours,omitempty"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	ClearExpiresAt     bool       `json:"clear_expires_at,omitempty"`
}

type UserBatchResult struct {
	Action  string   `json:"action"`
	Updated []string `json:"updated"`
	Skipped []string `json:"skipped,omitempty"`
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}

func cleanBoundedText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return s
}

func sanitizeUserFolderAllowlist(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	filtered := make([]string, 0, len(raw))
	for _, item := range raw {
		t := strings.TrimSpace(item)
		if t == "" {
			continue
		}
		filtered = append(filtered, t)
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	return ValidateFolderRules(filtered)
}

func validateNewUsername(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	runes := []rune(name)
	if len(runes) < 2 || len(runes) > 32 {
		return "", errors.New("用户名长度需为 2-32 个字符")
	}
	for _, ch := range runes {
		if unicode.IsSpace(ch) || ch == '/' || ch == '\\' || ch == ':' {
			return "", errors.New("用户名不能包含空格、斜杠或冒号")
		}
	}
	return name, nil
}

// GetUser returns the PublicUser view for username if present.
func (m *Manager) GetUser(username string) (PublicUser, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[strings.TrimSpace(username)]
	if !ok {
		return PublicUser{}, false
	}
	return toPublicUser(u), true
}

// RecordLogin stamps the latest human login timestamp and IP for governance auditing.
func (m *Manager) RecordLogin(username, ip string) {
	key := strings.TrimSpace(username)
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[key]
	if !ok {
		return
	}
	now := time.Now()
	u.LastLoginAt = &now
	if cleanIP := normalizeClientIP(strings.TrimSpace(ip)); cleanIP != "" {
		u.LastLoginIP = cleanIP
	}
	m.users[key] = u
	_ = m.saveLocked()
}

// AdminCreateUser creates a member account directly from the governance panel without invite codes.
func (m *Manager) AdminCreateUser(in AdminCreateUserInput) (PublicUser, string, error) {
	username, err := validateNewUsername(in.Username)
	if err != nil {
		return PublicUser{}, "", err
	}
	role := strings.ToLower(strings.TrimSpace(in.Role))
	if role == "" {
		role = RoleUser
	}
	if role != RoleAdmin && role != RoleUser && role != RoleReadonly {
		return PublicUser{}, "", errors.New("无效的角色类型（支持 admin / user / readonly）")
	}
	if in.QuotaBytes < 0 {
		return PublicUser{}, "", errors.New("个人配额不能为负数")
	}
	allowedFolders, err := sanitizeUserFolderAllowlist(in.AllowedFolders)
	if err != nil {
		return PublicUser{}, "", err
	}

	plainPass := in.Password
	if plainPass == "" {
		gen, genErr := randomStringFromAlphabet(12, adminPassAlphabet)
		if genErr != nil {
			return PublicUser{}, "", genErr
		}
		plainPass = gen
	} else if len(plainPass) < 4 {
		return PublicUser{}, "", errors.New("密码至少需要 4 位")
	}

	salt, err := randomHex(16)
	if err != nil {
		return PublicUser{}, "", err
	}

	var expPtr *time.Time
	if in.ExpiresAt != nil {
		expPtr = cloneTimePtr(in.ExpiresAt)
	} else if in.ExpireHours > 0 {
		t := time.Now().Add(time.Duration(in.ExpireHours) * time.Hour)
		expPtr = &t
	}

	var canUploadPtr, canFoldersPtr, canAgentsPtr *bool
	if role == RoleReadonly {
		f := false
		canUploadPtr = &f
		canFoldersPtr = &f
		canAgentsPtr = &f
	} else if role == RoleUser {
		canUploadPtr = cloneBoolPtr(in.CanUpload)
		canFoldersPtr = cloneBoolPtr(in.CanManageFolders)
		canAgentsPtr = cloneBoolPtr(in.CanUseAgents)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[username]; exists {
		return PublicUser{}, "", errors.New("用户名已存在")
	}

	u := User{
		Username:           username,
		PasswordHash:       hashPassword(plainPass, salt),
		Salt:               salt,
		Role:               role,
		Status:             StatusActive,
		DisplayName:        cleanBoundedText(in.DisplayName, 40),
		Note:               cleanBoundedText(in.Note, 200),
		QuotaBytes:         in.QuotaBytes,
		AllowedFolders:     allowedFolders,
		CanUpload:          canUploadPtr,
		CanManageFolders:   canFoldersPtr,
		CanUseAgents:       canAgentsPtr,
		MustChangePassword: in.MustChangePassword,
		SessionVersion:     1,
		ExpiresAt:          expPtr,
		CreatedAt:          time.Now(),
	}
	m.users[username] = u
	if err := m.saveLocked(); err != nil {
		delete(m.users, username)
		return PublicUser{}, "", err
	}
	return toPublicUser(u), plainPass, nil
}

// AdminUpdateUser modifies a member's profile, role, capabilities, storage quota, folder scope, or expiry.
func (m *Manager) AdminUpdateUser(actorUsername, targetUsername string, in AdminUpdateUserInput) (PublicUser, error) {
	actor := strings.TrimSpace(actorUsername)
	target := strings.TrimSpace(targetUsername)
	if target == "" {
		return PublicUser{}, errors.New("目标用户名不能为空")
	}

	var cleanFolders []string
	hasFolders := in.AllowedFolders != nil
	if hasFolders {
		var err error
		cleanFolders, err = sanitizeUserFolderAllowlist(*in.AllowedFolders)
		if err != nil {
			return PublicUser{}, err
		}
	}
	if in.QuotaBytes != nil && *in.QuotaBytes < 0 {
		return PublicUser{}, errors.New("个人配额不能为负数")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[target]
	if !ok {
		return PublicUser{}, errors.New("用户不存在")
	}
	prev := u

	if in.Role != nil {
		nextRole := strings.ToLower(strings.TrimSpace(*in.Role))
		if nextRole != RoleAdmin && nextRole != RoleUser && nextRole != RoleReadonly {
			return PublicUser{}, errors.New("无效的角色类型")
		}
		if nextRole != u.Role {
			if target == actor {
				return PublicUser{}, errors.New("不能修改当前登录账号的角色")
			}
			if nextRole == RoleAdmin && u.Status != "" && u.Status != StatusActive {
				return PublicUser{}, errors.New("仅可为正常状态的账号授予管理员权限")
			}
			if u.Role == RoleAdmin && nextRole != RoleAdmin {
				activeAdmins := 0
				for _, item := range m.users {
					if item.Role == RoleAdmin && (item.Status == "" || item.Status == StatusActive) {
						activeAdmins++
					}
				}
				if activeAdmins <= 1 {
					return PublicUser{}, errors.New("系统至少需保留一名有效管理员")
				}
			}
			u.Role = nextRole
			if nextRole == RoleReadonly {
				f := false
				u.CanUpload = &f
				u.CanManageFolders = &f
				u.CanUseAgents = &f
			} else {
				u.CanUpload = nil
				u.CanManageFolders = nil
				u.CanUseAgents = nil
			}
		}
	}

	if in.DisplayName != nil {
		u.DisplayName = cleanBoundedText(*in.DisplayName, 40)
	}
	if in.Note != nil {
		u.Note = cleanBoundedText(*in.Note, 200)
	}
	if in.QuotaBytes != nil {
		u.QuotaBytes = *in.QuotaBytes
	}
	if hasFolders {
		u.AllowedFolders = cleanFolders
	}
	if u.Role == RoleUser {
		if in.CanUpload != nil {
			u.CanUpload = cloneBoolPtr(in.CanUpload)
		}
		if in.CanManageFolders != nil {
			u.CanManageFolders = cloneBoolPtr(in.CanManageFolders)
		}
		if in.CanUseAgents != nil {
			u.CanUseAgents = cloneBoolPtr(in.CanUseAgents)
		}
	}
	if in.MustChangePassword != nil {
		u.MustChangePassword = *in.MustChangePassword
	}
	if in.ClearExpiresAt {
		u.ExpiresAt = nil
	} else if in.ExpiresAt != nil {
		if target == actor {
			return PublicUser{}, errors.New("不能为当前登录管理员设置过期时间")
		}
		u.ExpiresAt = cloneTimePtr(in.ExpiresAt)
	} else if in.ExpireHours != nil {
		if *in.ExpireHours <= 0 {
			u.ExpiresAt = nil
		} else {
			if target == actor {
				return PublicUser{}, errors.New("不能为当前登录管理员设置过期时间")
			}
			t := time.Now().Add(time.Duration(*in.ExpireHours) * time.Hour)
			u.ExpiresAt = &t
		}
	}

	m.users[target] = u
	if err := m.saveLocked(); err != nil {
		m.users[target] = prev
		return PublicUser{}, err
	}
	return toPublicUser(u), nil
}

// AdminResetPassword assigns a new (or auto-generated) password, revokes existing sessions/API tokens, and optionally enforces change on next login.
func (m *Manager) AdminResetPassword(actorUsername, targetUsername, newPassword string, mustChange bool) (PublicUser, string, error) {
	target := strings.TrimSpace(targetUsername)
	if target == "" {
		return PublicUser{}, "", errors.New("目标用户名不能为空")
	}
	if target == strings.TrimSpace(actorUsername) {
		return PublicUser{}, "", errors.New("当前登录账号请在「外观与账号」中修改密码")
	}
	plain := newPassword
	if plain == "" {
		gen, err := randomStringFromAlphabet(12, adminPassAlphabet)
		if err != nil {
			return PublicUser{}, "", err
		}
		plain = gen
	} else if len(plain) < 4 {
		return PublicUser{}, "", errors.New("密码至少需要 4 位")
	}

	salt, err := randomHex(16)
	if err != nil {
		return PublicUser{}, "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[target]
	if !ok {
		return PublicUser{}, "", errors.New("用户不存在")
	}
	prev := u
	u.Salt = salt
	u.PasswordHash = hashPassword(plain, salt)
	u.SessionVersion++
	u.APIToken = ""
	u.MustChangePassword = mustChange
	m.users[target] = u
	if err := m.saveLocked(); err != nil {
		m.users[target] = prev
		return PublicUser{}, "", err
	}
	return toPublicUser(u), plain, nil
}

// ForceLogoutUser invalidates all browser cookies (via SessionVersion) and personal API tokens for targetUsername.
func (m *Manager) ForceLogoutUser(actorUsername, targetUsername string) (PublicUser, error) {
	target := strings.TrimSpace(targetUsername)
	if target == "" {
		return PublicUser{}, errors.New("目标用户名不能为空")
	}
	if target == strings.TrimSpace(actorUsername) {
		return PublicUser{}, errors.New("不能对当前登录管理员执行强制下线")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[target]
	if !ok {
		return PublicUser{}, errors.New("用户不存在")
	}
	prev := u
	u.SessionVersion++
	u.APIToken = ""
	m.users[target] = u
	if err := m.saveLocked(); err != nil {
		m.users[target] = prev
		return PublicUser{}, err
	}
	return toPublicUser(u), nil
}

// SetOwnerAgentKeysDisabled freezes or unfreezes all AI Agent keys owned by ownerUsername and returns all affected key IDs.
func (m *Manager) SetOwnerAgentKeysDisabled(ownerUsername string, disabled bool) ([]string, error) {
	owner := strings.TrimSpace(ownerUsername)
	if owner == "" {
		return nil, errors.New("用户名不能为空")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.users[owner]; !ok {
		return nil, errors.New("用户不存在")
	}
	affected := make([]string, 0)
	prevMap := make(map[string]AgentKey)
	for id, ak := range m.agentKeys {
		if ak.OwnerUsername != owner {
			continue
		}
		if ak.Disabled != disabled {
			prevMap[id] = ak
			ak.Disabled = disabled
			m.agentKeys[id] = ak
			affected = append(affected, id)
		}
	}
	if len(affected) > 0 {
		if err := m.saveLocked(); err != nil {
			for id, oldAK := range prevMap {
				m.agentKeys[id] = oldAK
			}
			return nil, err
		}
		if disabled {
			for _, id := range affected {
				m.clearAgentRateBucket(id)
			}
		}
	}
	return affected, nil
}

// CountAgentKeysByOwner returns [total, active] AgentKey counts keyed by owner username.
func (m *Manager) CountAgentKeysByOwner() map[string][2]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	out := make(map[string][2]int)
	for _, ak := range m.agentKeys {
		counts := out[ak.OwnerUsername]
		counts[0]++
		expired := ak.ExpiresAt != nil && !now.Before(*ak.ExpiresAt)
		if !ak.Disabled && !expired {
			counts[1]++
		}
		out[ak.OwnerUsername] = counts
	}
	return out
}

// BatchUsersAction executes a bulk governance action across usernames safely in one persistence pass.
func (m *Manager) BatchUsersAction(actorUsername string, usernames []string, action string, quotaBytes *int64) (UserBatchResult, error) {
	act := strings.ToLower(strings.TrimSpace(action))
	switch act {
	case "approve", "ban", "unban", "force_logout", "set_quota":
	default:
		return UserBatchResult{}, fmt.Errorf("不支持的批量操作: %s", action)
	}
	if act == "set_quota" && (quotaBytes == nil || *quotaBytes < 0) {
		return UserBatchResult{}, errors.New("请提供有效的非负个人配额")
	}

	actor := strings.TrimSpace(actorUsername)
	seen := make(map[string]bool, len(usernames))
	res := UserBatchResult{Action: act}

	m.mu.Lock()
	defer m.mu.Unlock()
	prevUsers := make(map[string]User)

	for _, raw := range usernames {
		target := strings.TrimSpace(raw)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		u, ok := m.users[target]
		if !ok {
			res.Skipped = append(res.Skipped, target)
			continue
		}
		switch act {
		case "approve":
			if u.Status == StatusActive {
				res.Skipped = append(res.Skipped, target)
				continue
			}
			prevUsers[target] = u
			u.Status = StatusActive
		case "ban":
			if target == actor || u.Role == RoleAdmin {
				res.Skipped = append(res.Skipped, target)
				continue
			}
			prevUsers[target] = u
			u.Status = StatusBanned
			u.SessionVersion++
			u.APIToken = ""
		case "unban":
			if u.Status != StatusBanned {
				res.Skipped = append(res.Skipped, target)
				continue
			}
			prevUsers[target] = u
			u.Status = StatusActive
		case "force_logout":
			if target == actor {
				res.Skipped = append(res.Skipped, target)
				continue
			}
			prevUsers[target] = u
			u.SessionVersion++
			u.APIToken = ""
		case "set_quota":
			prevUsers[target] = u
			u.QuotaBytes = *quotaBytes
		}
		m.users[target] = u
		res.Updated = append(res.Updated, target)
	}

	if len(res.Updated) > 0 {
		if err := m.saveLocked(); err != nil {
			for k, oldU := range prevUsers {
				m.users[k] = oldU
			}
			return UserBatchResult{}, err
		}
	}
	return res, nil
}

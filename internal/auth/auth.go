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
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const sessionCookieName = "lan_filehub_session"

const (
	inviteAlphabet    = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	adminUserAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	adminPassAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
)

const (
	StatusActive  = "active"
	StatusPending = "pending"
	StatusBanned  = "banned"
)

type User struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Salt         string    `json:"salt"`
	APIToken     string    `json:"api_token,omitempty"`
	Role         string    `json:"role"`   // "admin" or "user"
	Status       string    `json:"status"` // "active", "pending", "banned"
	CreatedAt    time.Time `json:"created_at"`
}

type PublicUser struct {
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type AuthSettings struct {
	InviteRequired bool     `json:"invite_required"`
	AllowedExts    []string `json:"allowed_exts"`
	BlockedExts    []string `json:"blocked_exts"`
}

type InviteCode struct {
	Code      string     `json:"code"`
	Note      string     `json:"note,omitempty"`
	MaxUses   int        `json:"max_uses"` // 0 = unlimited, >0 = max allowed registrations
	UsedCount int        `json:"used_count"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
}

type storeData struct {
	Secret         string                `json:"secret"`
	InviteRequired *bool                 `json:"invite_required,omitempty"`
	AllowedExts    []string              `json:"allowed_exts,omitempty"`
	BlockedExts    []string              `json:"blocked_exts,omitempty"`
	Users          map[string]User       `json:"users"`
	Invites        map[string]InviteCode `json:"invites,omitempty"`
}

type InitOptions struct {
	AdminUser string
	AdminPass string
}

type Manager struct {
	mu                  sync.RWMutex
	dataDir             string
	filePath            string
	secret              []byte
	inviteRequired      bool
	allowedExts         []string
	blockedExts         []string
	users               map[string]User
	invites             map[string]InviteCode
	initialAdminUser    string
	initialAdminPass    string
	createdInitialAdmin bool
}

func NewManager(dataDir string) (*Manager, error) {
	return NewManagerWithOptions(dataDir, InitOptions{})
}

func NewManagerWithOptions(dataDir string, opts InitOptions) (*Manager, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	fp := filepath.Join(dataDir, "users.json")
	m := &Manager{
		dataDir:        dataDir,
		filePath:       fp,
		inviteRequired: true,
		allowedExts:    []string{},
		blockedExts:    []string{},
		users:          make(map[string]User),
		invites:        make(map[string]InviteCode),
	}
	if err := m.loadOrInit(opts); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) InitialAdminCredentials() (username, password string, created bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.initialAdminUser, m.initialAdminPass, m.createdInitialAdmin
}

func (m *Manager) loadOrInit(opts InitOptions) error {
	raw, err := os.ReadFile(m.filePath)
	if err == nil {
		var data storeData
		if err := json.Unmarshal(raw, &data); err == nil && data.Secret != "" {
			sec, decErr := hex.DecodeString(data.Secret)
			if decErr == nil && len(sec) >= 16 {
				m.secret = sec
				if data.InviteRequired != nil {
					m.inviteRequired = *data.InviteRequired
				} else {
					m.inviteRequired = true
				}
				m.allowedExts = NormalizeExts(data.AllowedExts)
				m.blockedExts = NormalizeExts(data.BlockedExts)
				if data.Users != nil {
					for k, u := range data.Users {
						if u.Status == "" {
							u.Status = StatusActive
						}
						m.users[k] = u
					}
				}
				if data.Invites != nil {
					m.invites = data.Invites
				}
				return nil
			}
		}
	}

	sec := make([]byte, 32)
	if _, err := rand.Read(sec); err != nil {
		return err
	}
	m.secret = sec
	m.inviteRequired = true
	m.allowedExts = []string{}
	m.blockedExts = []string{}

	initUser := strings.TrimSpace(opts.AdminUser)
	if initUser == "" {
		suffix, err := randomStringFromAlphabet(6, adminUserAlphabet)
		if err != nil {
			return err
		}
		initUser = "lfh_" + suffix
	}
	initPass := opts.AdminPass
	if len(initPass) < 4 {
		generatedPass, err := randomStringFromAlphabet(12, adminPassAlphabet)
		if err != nil {
			return err
		}
		initPass = generatedPass
	}

	if err := m.createUserLocked(initUser, initPass, "admin", StatusActive); err != nil {
		return err
	}
	if err := m.saveLocked(); err != nil {
		return err
	}
	m.initialAdminUser = initUser
	m.initialAdminPass = initPass
	m.createdInitialAdmin = true

	credDoc := fmt.Sprintf(
		"LFH 首次部署随机管理员账号\n==============================\n用户名: %s\n密码:   %s\n生成时间: %s\n\n提示: 请妥善保存以上凭据，首次登录后可在右上角「设置 -> 外观与账号」修改密码，并删除此文件。\n",
		initUser,
		initPass,
		time.Now().Format("2006-01-02 15:04:05"),
	)
	_ = os.WriteFile(filepath.Join(m.dataDir, "initial-admin.txt"), []byte(credDoc), 0600)
	return nil
}

func (m *Manager) saveLocked() error {
	invReq := m.inviteRequired
	data := storeData{
		Secret:         hex.EncodeToString(m.secret),
		InviteRequired: &invReq,
		AllowedExts:    m.allowedExts,
		BlockedExts:    m.blockedExts,
		Users:          m.users,
		Invites:        m.invites,
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, raw, 0600)
}

// ExportSnapshot serializes the full auth state (secret, policies, users, invites) for entry-node handoff.
func (m *Manager) ExportSnapshot() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	invReq := m.inviteRequired
	data := storeData{
		Secret:         hex.EncodeToString(m.secret),
		InviteRequired: &invReq,
		AllowedExts:    m.allowedExts,
		BlockedExts:    m.blockedExts,
		Users:          m.users,
		Invites:        m.invites,
	}
	return json.Marshal(data)
}

// ImportSnapshot atomically replaces auth state from an entry-node snapshot and persists users.json.
func (m *Manager) ImportSnapshot(raw []byte) error {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return errors.New("账号快照内容为空")
	}
	var data storeData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("解析账号快照失败: %w", err)
	}
	secret, err := hex.DecodeString(strings.TrimSpace(data.Secret))
	if err != nil || len(secret) < 16 {
		return errors.New("账号快照签名密钥无效")
	}
	if len(data.Users) == 0 {
		return errors.New("账号快照缺少有效用户")
	}

	users := make(map[string]User, len(data.Users))
	hasActiveAdmin := false
	for key, u := range data.Users {
		username := strings.TrimSpace(u.Username)
		if username == "" {
			username = strings.TrimSpace(key)
		}
		if username == "" {
			continue
		}
		u.Username = username
		u.Role = strings.ToLower(strings.TrimSpace(u.Role))
		if u.Role != "admin" {
			u.Role = "user"
		}
		u.Status = strings.ToLower(strings.TrimSpace(u.Status))
		if u.Status == "" {
			u.Status = StatusActive
		}
		if u.Role == "admin" && u.Status == StatusActive {
			hasActiveAdmin = true
		}
		users[username] = u
	}
	if len(users) == 0 || !hasActiveAdmin {
		return errors.New("账号快照必须包含至少一个可用管理员")
	}

	invites := make(map[string]InviteCode, len(data.Invites))
	for k, inv := range data.Invites {
		code := strings.TrimSpace(inv.Code)
		if code == "" {
			code = strings.TrimSpace(k)
		}
		if code == "" {
			continue
		}
		inv.Code = code
		invites[code] = inv
	}

	allowed := NormalizeExts(data.AllowedExts)
	blocked := NormalizeExts(data.BlockedExts)

	m.mu.Lock()
	defer m.mu.Unlock()

	invReq := m.inviteRequired
	if data.InviteRequired != nil {
		invReq = *data.InviteRequired
	}

	oldSecret := m.secret
	oldInviteReq := m.inviteRequired
	oldAllowed := m.allowedExts
	oldBlocked := m.blockedExts
	oldUsers := m.users
	oldInvites := m.invites

	m.secret = secret
	m.inviteRequired = invReq
	m.allowedExts = allowed
	m.blockedExts = blocked
	m.users = users
	m.invites = invites

	if err := m.saveLocked(); err != nil {
		m.secret = oldSecret
		m.inviteRequired = oldInviteReq
		m.allowedExts = oldAllowed
		m.blockedExts = oldBlocked
		m.users = oldUsers
		m.invites = oldInvites
		return err
	}
	return nil
}

func hashPassword(password, salt string) string {
	h := sha256.New()
	h.Write([]byte(salt + ":" + password))
	return hex.EncodeToString(h.Sum(nil))
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomStringFromAlphabet(n int, alphabet string) (string, error) {
	var sb strings.Builder
	sb.Grow(n)
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(alphabet[idx.Int64()])
	}
	return sb.String(), nil
}

func generateInviteCode() (string, error) {
	p1, err := randomStringFromAlphabet(4, inviteAlphabet)
	if err != nil {
		return "", err
	}
	p2, err := randomStringFromAlphabet(4, inviteAlphabet)
	if err != nil {
		return "", err
	}
	return "LFH-" + p1 + "-" + p2, nil
}

func isValidCustomInviteCode(code string) bool {
	if len(code) < 4 || len(code) > 32 {
		return false
	}
	for _, ch := range code {
		isUpper := ch >= 'A' && ch <= 'Z'
		isDigit := ch >= '0' && ch <= '9'
		if !isUpper && !isDigit && ch != '-' && ch != '_' {
			return false
		}
	}
	return true
}

func (m *Manager) createUserLocked(username, password, role, status string) error {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 4 {
		return errors.New("用户名不能为空且密码至少 4 位")
	}
	if _, exists := m.users[username]; exists {
		return errors.New("用户名已存在")
	}
	if status == "" {
		status = StatusActive
	}
	salt, err := randomHex(16)
	if err != nil {
		return err
	}
	m.users[username] = User{
		Username:     username,
		PasswordHash: hashPassword(password, salt),
		Salt:         salt,
		Role:         role,
		Status:       status,
		CreatedAt:    time.Now(),
	}
	return nil
}

func toPublicUser(u User) PublicUser {
	st := u.Status
	if st == "" {
		st = StatusActive
	}
	return PublicUser{
		Username:  u.Username,
		Role:      u.Role,
		Status:    st,
		CreatedAt: u.CreatedAt,
	}
}

func (m *Manager) AuthenticateWithReason(username, password string) (PublicUser, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[strings.TrimSpace(username)]
	if !ok {
		return PublicUser{}, http.StatusUnauthorized, errors.New("用户名或密码错误")
	}
	if !hmac.Equal([]byte(hashPassword(password, u.Salt)), []byte(u.PasswordHash)) {
		return PublicUser{}, http.StatusUnauthorized, errors.New("用户名或密码错误")
	}
	st := u.Status
	if st == "" {
		st = StatusActive
	}
	if st == StatusPending {
		return PublicUser{}, http.StatusForbidden, errors.New("该账号正在等待管理员审核同意，请通过后再登录")
	}
	if st == StatusBanned {
		return PublicUser{}, http.StatusForbidden, errors.New("该账号已被管理员封禁，无法登录")
	}
	return toPublicUser(u), http.StatusOK, nil
}

func (m *Manager) Authenticate(username, password string) (PublicUser, bool) {
	u, _, err := m.AuthenticateWithReason(username, password)
	return u, err == nil
}

func NormalizeExts(list []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(list))
	for _, raw := range list {
		tokens := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '，' || r == '、' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
		})
		for _, tok := range tokens {
			ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tok), "."))
			if ext == "" || seen[ext] {
				continue
			}
			seen[ext] = true
			out = append(out, ext)
		}
	}
	return out
}

func (m *Manager) Settings() AuthSettings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	allowed := append([]string{}, m.allowedExts...)
	blocked := append([]string{}, m.blockedExts...)
	return AuthSettings{
		InviteRequired: m.inviteRequired,
		AllowedExts:    allowed,
		BlockedExts:    blocked,
	}
}

func (m *Manager) SetInviteRequired(required bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inviteRequired = required
	return m.saveLocked()
}

func (m *Manager) UpdateSettings(inviteRequired *bool, allowedExts *[]string, blockedExts *[]string) (AuthSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inviteRequired != nil {
		m.inviteRequired = *inviteRequired
	}
	if allowedExts != nil {
		m.allowedExts = NormalizeExts(*allowedExts)
	}
	if blockedExts != nil {
		m.blockedExts = NormalizeExts(*blockedExts)
	}
	if err := m.saveLocked(); err != nil {
		return AuthSettings{}, err
	}
	return AuthSettings{
		InviteRequired: m.inviteRequired,
		AllowedExts:    append([]string{}, m.allowedExts...),
		BlockedExts:    append([]string{}, m.blockedExts...),
	}, nil
}

func extractExtCandidates(filename string) ([]string, string) {
	low := strings.ToLower(strings.TrimSpace(filepath.Base(filename)))
	ext := strings.TrimPrefix(filepath.Ext(low), ".")
	if ext == "" {
		return nil, ""
	}
	candidates := []string{ext}
	if strings.HasSuffix(low, ".tar."+ext) {
		candidates = []string{"tar." + ext, ext}
		return candidates, "tar." + ext
	}
	return candidates, ext
}

func (m *Manager) CheckUploadAllowed(filename string) error {
	m.mu.RLock()
	allowed := m.allowedExts
	blocked := m.blockedExts
	m.mu.RUnlock()

	candidates, primary := extractExtCandidates(filename)

	for _, b := range blocked {
		for _, c := range candidates {
			if c == b {
				return errors.New("管理员已禁止上传 ." + c + " 类型文件")
			}
		}
	}

	if len(allowed) > 0 {
		if primary == "" {
			return errors.New("管理员仅允许上传指定类型文件，当前不允许无扩展名文件")
		}
		matched := false
		for _, a := range allowed {
			for _, c := range candidates {
				if c == a {
					matched = true
					break
				}
			}
			if matched {
				break
			}
		}
		if !matched {
			return errors.New("管理员仅允许上传指定类型文件，当前不允许 ." + primary)
		}
	}

	return nil
}

func (m *Manager) PendingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cnt := 0
	for _, u := range m.users {
		if u.Status == StatusPending {
			cnt++
		}
	}
	return cnt
}

func (m *Manager) Register(username, password, inviteCode string) (PublicUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.inviteRequired {
		if err := m.createUserLocked(username, password, "user", StatusPending); err != nil {
			return PublicUser{}, err
		}
		if err := m.saveLocked(); err != nil {
			return PublicUser{}, err
		}
		return toPublicUser(m.users[strings.TrimSpace(username)]), nil
	}

	code := strings.ToUpper(strings.TrimSpace(inviteCode))
	if code == "" {
		return PublicUser{}, errors.New("请输入注册邀请码")
	}
	inv, ok := m.invites[code]
	if !ok {
		return PublicUser{}, errors.New("邀请码无效")
	}
	if inv.ExpiresAt != nil && time.Now().After(*inv.ExpiresAt) {
		return PublicUser{}, errors.New("邀请码已过期")
	}
	if inv.MaxUses > 0 && inv.UsedCount >= inv.MaxUses {
		return PublicUser{}, errors.New("邀请码使用次数已用完")
	}

	if err := m.createUserLocked(username, password, "user", StatusActive); err != nil {
		return PublicUser{}, err
	}
	inv.UsedCount++
	m.invites[code] = inv

	if err := m.saveLocked(); err != nil {
		return PublicUser{}, err
	}
	return toPublicUser(m.users[strings.TrimSpace(username)]), nil
}

func (m *Manager) ApproveUser(username string) (PublicUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.TrimSpace(username)
	u, ok := m.users[key]
	if !ok {
		return PublicUser{}, errors.New("用户不存在")
	}
	u.Status = StatusActive
	m.users[key] = u
	if err := m.saveLocked(); err != nil {
		return PublicUser{}, err
	}
	return toPublicUser(u), nil
}

func (m *Manager) SetUserBanned(actorUsername, targetUsername string, banned bool) (PublicUser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target := strings.TrimSpace(targetUsername)
	if target == strings.TrimSpace(actorUsername) {
		return PublicUser{}, errors.New("不能操作当前登录账号")
	}
	u, ok := m.users[target]
	if !ok {
		return PublicUser{}, errors.New("用户不存在")
	}
	if u.Role == "admin" {
		return PublicUser{}, errors.New("不能封禁管理员账号")
	}
	if banned {
		u.Status = StatusBanned
	} else {
		u.Status = StatusActive
	}
	m.users[target] = u
	if err := m.saveLocked(); err != nil {
		return PublicUser{}, err
	}
	return toPublicUser(u), nil
}

func (m *Manager) DeleteUser(actorUsername, targetUsername string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	target := strings.TrimSpace(targetUsername)
	if target == strings.TrimSpace(actorUsername) {
		return errors.New("不能删除当前登录账号")
	}
	u, ok := m.users[target]
	if !ok {
		return errors.New("用户不存在")
	}
	if u.Role == "admin" {
		return errors.New("不能删除管理员账号")
	}
	delete(m.users, target)
	return m.saveLocked()
}

// SetUserRole promotes an active user to admin or revokes admin privileges back to regular user.
func (m *Manager) SetUserRole(actorUsername, targetUsername, role string) (PublicUser, error) {
	actor := strings.TrimSpace(actorUsername)
	target := strings.TrimSpace(targetUsername)
	nextRole := strings.ToLower(strings.TrimSpace(role))
	if nextRole != "admin" && nextRole != "user" {
		return PublicUser{}, errors.New("无效的角色类型")
	}
	if target == "" {
		return PublicUser{}, errors.New("目标用户名不能为空")
	}
	if target == actor {
		return PublicUser{}, errors.New("不能修改当前登录账号的管理员权限")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	u, ok := m.users[target]
	if !ok {
		return PublicUser{}, errors.New("用户不存在")
	}
	if nextRole == "admin" && u.Status != "" && u.Status != StatusActive {
		return PublicUser{}, errors.New("仅可为正常状态的账号授予管理员权限")
	}
	if u.Role == "admin" && nextRole != "admin" {
		activeAdmins := 0
		for _, item := range m.users {
			if item.Role == "admin" && (item.Status == "" || item.Status == StatusActive) {
				activeAdmins++
			}
		}
		if activeAdmins <= 1 {
			return PublicUser{}, errors.New("系统至少需保留一名有效管理员")
		}
	}

	u.Role = nextRole
	m.users[target] = u
	if err := m.saveLocked(); err != nil {
		return PublicUser{}, err
	}
	return toPublicUser(u), nil
}

// ListActiveUsers returns all active accounts sorted by username for file access selection.
func (m *Manager) ListActiveUsers() []PublicUser {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PublicUser, 0, len(m.users))
	for _, u := range m.users {
		if u.Status != "" && u.Status != StatusActive {
			continue
		}
		out = append(out, toPublicUser(u))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Username < out[j].Username
	})
	return out
}

// ValidateExistingUsers normalizes, deduplicates, and verifies that each username exists.
func (m *Manager) ValidateExistingUsers(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		if _, ok := m.users[name]; !ok {
			return nil, errors.New("指定的用户不存在: " + name)
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func (m *Manager) CreateInvite(createdBy, customCode, note string, maxUses int, expireHours int) (InviteCode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if maxUses < 0 {
		maxUses = 1
	}

	code := strings.ToUpper(strings.TrimSpace(customCode))
	if code != "" {
		if !isValidCustomInviteCode(code) {
			return InviteCode{}, errors.New("自定义邀请码长度需为 4-32 位，仅支持字母、数字、-、_")
		}
		if _, exists := m.invites[code]; exists {
			return InviteCode{}, errors.New("邀请码已存在")
		}
	} else {
		for attempt := 0; attempt < 16; attempt++ {
			candidate, err := generateInviteCode()
			if err != nil {
				return InviteCode{}, err
			}
			if _, exists := m.invites[candidate]; !exists {
				code = candidate
				break
			}
		}
		if code == "" {
			return InviteCode{}, errors.New("生成邀请码失败，请重试")
		}
	}

	inv := InviteCode{
		Code:      code,
		Note:      strings.TrimSpace(note),
		MaxUses:   maxUses,
		UsedCount: 0,
		CreatedBy: createdBy,
		CreatedAt: time.Now(),
	}
	if expireHours > 0 {
		exp := time.Now().Add(time.Duration(expireHours) * time.Hour)
		inv.ExpiresAt = &exp
	}

	m.invites[code] = inv
	if err := m.saveLocked(); err != nil {
		return InviteCode{}, err
	}
	return inv, nil
}

func (m *Manager) ListInvites() []InviteCode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]InviteCode, 0, len(m.invites))
	for _, inv := range m.invites {
		out = append(out, inv)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (m *Manager) DeleteInvite(code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := strings.ToUpper(strings.TrimSpace(code))
	if _, ok := m.invites[key]; !ok {
		return errors.New("邀请码不存在")
	}
	delete(m.invites, key)
	return m.saveLocked()
}

func (m *Manager) ChangePassword(username, oldPwd, newPwd string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[username]
	if !ok {
		return errors.New("用户不存在")
	}
	if !hmac.Equal([]byte(hashPassword(oldPwd, u.Salt)), []byte(u.PasswordHash)) {
		return errors.New("原密码错误")
	}
	if len(newPwd) < 4 {
		return errors.New("新密码至少 4 位")
	}
	salt, err := randomHex(16)
	if err != nil {
		return err
	}
	u.Salt = salt
	u.PasswordHash = hashPassword(newPwd, salt)
	m.users[username] = u
	return m.saveLocked()
}

func userSortRank(u PublicUser) int {
	if u.Status == StatusPending {
		return 0
	}
	if u.Role == "admin" {
		return 1
	}
	if u.Status == StatusActive {
		return 2
	}
	return 3
}

func (m *Manager) ListUsers() []PublicUser {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PublicUser, 0, len(m.users))
	for _, u := range m.users {
		out = append(out, toPublicUser(u))
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := userSortRank(out[i]), userSortRank(out[j])
		if ri != rj {
			return ri < rj
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Username < out[j].Username
	})
	return out
}

type sessionPayload struct {
	Username string `json:"u"`
	Role     string `json:"r"`
	Exp      int64  `json:"e"`
}

func (m *Manager) IssueCookie(w http.ResponseWriter, u PublicUser) error {
	payload := sessionPayload{
		Username: u.Username,
		Role:     u.Role,
		Exp:      time.Now().Add(7 * 24 * time.Hour).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	b64 := base64.RawURLEncoding.EncodeToString(raw)
	sig := m.sign(b64)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    b64 + "." + sig,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 3600,
	})
	return nil
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (m *Manager) sign(data string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func extractRequestToken(r *http.Request) string {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "" {
		if len(authz) > 7 && strings.EqualFold(authz[:7], "Bearer ") {
			return strings.TrimSpace(authz[7:])
		}
		return authz
	}
	if tok := strings.TrimSpace(r.Header.Get("X-API-Token")); tok != "" {
		return tok
	}
	if q := r.URL.Query(); q != nil {
		if tok := strings.TrimSpace(q.Get("token")); tok != "" {
			return tok
		}
	}
	return ""
}

func (m *Manager) GetAPIToken(username string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[username]
	if !ok {
		return ""
	}
	return u.APIToken
}

func (m *Manager) RegenerateAPIToken(username string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := "lfh_tok_" + hex.EncodeToString(raw)

	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[username]
	if !ok {
		return "", errors.New("用户不存在")
	}
	u.APIToken = tok
	m.users[username] = u
	if err := m.saveLocked(); err != nil {
		return "", err
	}
	return tok, nil
}

func (m *Manager) RevokeAPIToken(username string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[username]
	if !ok {
		return errors.New("用户不存在")
	}
	u.APIToken = ""
	m.users[username] = u
	return m.saveLocked()
}

func (m *Manager) AuthenticateToken(token string) (PublicUser, bool) {
	clean := strings.TrimSpace(token)
	if clean == "" {
		return PublicUser{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.users {
		if u.APIToken == "" {
			continue
		}
		if len(u.APIToken) == len(clean) && subtle.ConstantTimeCompare([]byte(u.APIToken), []byte(clean)) == 1 {
			if u.Status != "" && u.Status != StatusActive {
				return PublicUser{}, false
			}
			return toPublicUser(u), true
		}
	}
	return PublicUser{}, false
}

func (m *Manager) CurrentUser(r *http.Request) (PublicUser, bool) {
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
							return toPublicUser(u), true
						}
					}
				}
			}
		}
	}

	if tok := extractRequestToken(r); tok != "" {
		return m.AuthenticateToken(tok)
	}
	return PublicUser{}, false
}

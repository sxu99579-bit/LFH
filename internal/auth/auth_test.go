package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultAdminAndInviteRegistration(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	initUser, initPass, created := mgr.InitialAdminCredentials()
	if !created || !strings.HasPrefix(initUser, "lfh_") || initUser == "admin" || len(initPass) < 12 || initPass == "admin123" {
		t.Fatalf("expected randomized initial admin credentials, got user=%q passLen=%d created=%v", initUser, len(initPass), created)
	}
	credRaw, err := os.ReadFile(filepath.Join(dir, "initial-admin.txt"))
	if err != nil || !strings.Contains(string(credRaw), initUser) || !strings.Contains(string(credRaw), initPass) {
		t.Fatalf("expected initial-admin.txt to record credentials, err=%v content=%q", err, string(credRaw))
	}

	mgr2, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager 2: %v", err)
	}
	u2, p2, created2 := mgr2.InitialAdminCredentials()
	if !created2 || u2 == initUser || p2 == initPass {
		t.Fatalf("expected distinct random credentials across deployments, got (%q,%q) vs (%q,%q)", initUser, initPass, u2, p2)
	}

	admin, ok := mgr.Authenticate(initUser, initPass)
	if !ok || admin.Role != "admin" || admin.Status != StatusActive {
		t.Fatalf("expected initial admin login, got ok=%v user=%+v", ok, admin)
	}

	if _, err := mgr.Register("alice", "pass1234", ""); err == nil || !strings.Contains(err.Error(), "邀请码") {
		t.Fatalf("expected error for empty invite code, got %v", err)
	}
	if _, err := mgr.Register("alice", "pass1234", "NO-SUCH-CODE"); err == nil {
		t.Fatalf("expected error for invalid invite code")
	}

	inv, err := mgr.CreateInvite(initUser, "", "给手机用", 1, 24)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if !strings.HasPrefix(inv.Code, "LFH-") || inv.MaxUses != 1 || inv.UsedCount != 0 {
		t.Fatalf("unexpected invite: %+v", inv)
	}

	// Failed registration due to short password must not consume invite.
	if _, err := mgr.Register("alice", "12", inv.Code); err == nil {
		t.Fatalf("expected short password failure")
	}
	if invites := mgr.ListInvites(); len(invites) != 1 || invites[0].UsedCount != 0 {
		t.Fatalf("invite should not be consumed on failed registration: %+v", invites)
	}

	// Lowercase + whitespace input should work.
	u, err := mgr.Register("alice", "pass1234", " "+strings.ToLower(inv.Code)+" ")
	if err != nil {
		t.Fatalf("Register with valid invite: %v", err)
	}
	if u.Username != "alice" || u.Role != "user" || u.Status != StatusActive {
		t.Fatalf("unexpected user: %+v", u)
	}

	// Second use of single-use invite must fail.
	if _, err := mgr.Register("bob", "pass1234", inv.Code); err == nil || !strings.Contains(err.Error(), "用完") {
		t.Fatalf("expected exhausted invite error, got %v", err)
	}

	// Expired invite test.
	expInv, err := mgr.CreateInvite(initUser, "EXP-TEST-1", "expired", 5, 1)
	if err != nil {
		t.Fatalf("CreateInvite custom: %v", err)
	}
	mgr.mu.Lock()
	past := time.Now().Add(-time.Minute)
	stored := mgr.invites[expInv.Code]
	stored.ExpiresAt = &past
	mgr.invites[expInv.Code] = stored
	if err := mgr.saveLocked(); err != nil {
		mgr.mu.Unlock()
		t.Fatalf("saveLocked: %v", err)
	}
	mgr.mu.Unlock()

	if _, err := mgr.Register("charlie", "pass1234", expInv.Code); err == nil || !strings.Contains(err.Error(), "过期") {
		t.Fatalf("expected expired invite error, got %v", err)
	}

	if err := mgr.DeleteInvite(expInv.Code); err != nil {
		t.Fatalf("DeleteInvite: %v", err)
	}

	// Reload persistence check.
	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	if _, _, recreated := reloaded.InitialAdminCredentials(); recreated {
		t.Fatalf("expected existing users.json reload to report created=false")
	}
	if _, ok := reloaded.Authenticate("alice", "pass1234"); !ok {
		t.Fatalf("expected alice persisted across reload")
	}
	users := reloaded.ListUsers()
	if len(users) != 2 {
		t.Fatalf("expected 2 users after reload, got %+v", users)
	}
	if len(reloaded.ListInvites()) != 1 {
		t.Fatalf("expected 1 remaining invite after reload")
	}
}

func TestApprovalFlowBanAndDeleteUser(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	initAdmin, _, _ := mgr.InitialAdminCredentials()

	if !mgr.Settings().InviteRequired {
		t.Fatalf("expected invite_required=true by default")
	}

	// Turn off invite_required.
	if err := mgr.SetInviteRequired(false); err != nil {
		t.Fatalf("SetInviteRequired: %v", err)
	}
	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	if reloaded.Settings().InviteRequired {
		t.Fatalf("expected invite_required=false persisted across reload")
	}

	// Register without invite -> pending user.
	u, err := reloaded.Register("pending_user", "pass1234", "")
	if err != nil {
		t.Fatalf("Register without invite: %v", err)
	}
	if u.Status != StatusPending || reloaded.PendingCount() != 1 {
		t.Fatalf("expected pending user, got %+v count=%d", u, reloaded.PendingCount())
	}

	// Pending user cannot login.
	if _, code, err := reloaded.AuthenticateWithReason("pending_user", "pass1234"); err == nil || code != http.StatusForbidden {
		t.Fatalf("expected 403 for pending user login, got code=%d err=%v", code, err)
	}

	// Approve user -> can login and get valid session.
	approved, err := reloaded.ApproveUser("pending_user")
	if err != nil {
		t.Fatalf("ApproveUser: %v", err)
	}
	if approved.Status != StatusActive || reloaded.PendingCount() != 0 {
		t.Fatalf("expected active user after approval, got %+v", approved)
	}

	rec := httptest.NewRecorder()
	if err := reloaded.IssueCookie(rec, approved); err != nil {
		t.Fatalf("IssueCookie: %v", err)
	}
	reqWithCookie := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	for _, c := range rec.Result().Cookies() {
		reqWithCookie.AddCookie(c)
	}
	if cur, ok := reloaded.CurrentUser(reqWithCookie); !ok || cur.Username != "pending_user" {
		t.Fatalf("expected valid session before ban, got ok=%v user=%+v", ok, cur)
	}

	// Ban user -> blocks login and immediately invalidates CurrentUser.
	if _, err := reloaded.SetUserBanned(initAdmin, initAdmin, true); err == nil {
		t.Fatalf("expected error banning self")
	}
	banned, err := reloaded.SetUserBanned(initAdmin, "pending_user", true)
	if err != nil || banned.Status != StatusBanned {
		t.Fatalf("SetUserBanned(true): err=%v user=%+v", err, banned)
	}
	if _, ok := reloaded.CurrentUser(reqWithCookie); ok {
		t.Fatalf("expected banned user's active session cookie to be rejected")
	}
	if _, code, err := reloaded.AuthenticateWithReason("pending_user", "pass1234"); err == nil || code != http.StatusForbidden {
		t.Fatalf("expected 403 for banned user login, got code=%d err=%v", code, err)
	}

	// Unban restores login.
	unbanned, err := reloaded.SetUserBanned(initAdmin, "pending_user", false)
	if err != nil || unbanned.Status != StatusActive {
		t.Fatalf("SetUserBanned(false): err=%v user=%+v", err, unbanned)
	}
	if _, ok := reloaded.Authenticate("pending_user", "pass1234"); !ok {
		t.Fatalf("expected login restored after unban")
	}

	// Delete user (cannot delete self/admin).
	if err := reloaded.DeleteUser(initAdmin, initAdmin); err == nil {
		t.Fatalf("expected error deleting self")
	}
	if err := reloaded.DeleteUser(initAdmin, "pending_user"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, ok := reloaded.Authenticate("pending_user", "pass1234"); ok {
		t.Fatalf("expected deleted user login to fail")
	}
}

func TestLegacyUsersJSONWithoutInvitesOrStatus(t *testing.T) {
	dir := t.TempDir()
	legacyJSON := `{
  "secret": "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff",
  "users": {
    "admin": {
      "username": "admin",
      "password_hash": "abc",
      "salt": "def",
      "role": "admin",
      "created_at": "2026-11-21T14:00:00Z"
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "users.json"), []byte(legacyJSON), 0600); err != nil {
		t.Fatalf("write legacy json: %v", err)
	}
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager legacy: %v", err)
	}
	users := mgr.ListUsers()
	if len(users) != 1 || users[0].Status != StatusActive || !mgr.Settings().InviteRequired {
		t.Fatalf("unexpected legacy migration: users=%+v settings=%+v", users, mgr.Settings())
	}
	if _, err := mgr.CreateInvite("admin", "", "test", 0, 0); err != nil {
		t.Fatalf("CreateInvite on legacy store: %v", err)
	}
}

func TestUploadPolicySettings(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Default allows everything.
	if err := mgr.CheckUploadAllowed("test.exe"); err != nil {
		t.Fatalf("expected default policy to allow everything, got %v", err)
	}

	// Partial update: set allowed & blocked exts without changing invite_required.
	allowed := []string{".PDF, md", "tar.gz", "MP4"}
	blocked := []string{"exe", ".bat"}
	st, err := mgr.UpdateSettings(nil, &allowed, &blocked)
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if !st.InviteRequired || len(st.AllowedExts) != 4 || len(st.BlockedExts) != 2 {
		t.Fatalf("unexpected updated settings: %+v", st)
	}

	// Partial update: toggle invite_required without clearing allowed/blocked exts.
	invFalse := false
	st2, err := mgr.UpdateSettings(&invFalse, nil, nil)
	if err != nil {
		t.Fatalf("UpdateSettings invite only: %v", err)
	}
	if st2.InviteRequired || len(st2.AllowedExts) != 4 || len(st2.BlockedExts) != 2 {
		t.Fatalf("expected exts preserved after toggling invite_required: %+v", st2)
	}

	// Reload persistence check.
	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := reloaded.CheckUploadAllowed("report.pdf"); err != nil {
		t.Fatalf("expected pdf allowed: %v", err)
	}
	if err := reloaded.CheckUploadAllowed("archive.tar.gz"); err != nil {
		t.Fatalf("expected tar.gz allowed: %v", err)
	}
	if err := reloaded.CheckUploadAllowed("malware.EXE"); err == nil {
		t.Fatalf("expected EXE blocked")
	}
	if err := reloaded.CheckUploadAllowed("image.png"); err == nil {
		t.Fatalf("expected png rejected when whitelist active")
	}
	if err := reloaded.CheckUploadAllowed("Makefile"); err == nil {
		t.Fatalf("expected extensionless file rejected when whitelist active")
	}
}

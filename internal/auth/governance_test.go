package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGovernanceCreateQuotaRolesSessionVersionAndExpiry(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	adminUser, _, _ := mgr.InitialAdminCredentials()

	// 1. Admin creates readonly account with folder allowlist, quota, note, and must_change_password.
	ro, genPwd, err := mgr.AdminCreateUser(AdminCreateUserInput{
		Username:           "guest_ro",
		Role:               RoleReadonly,
		DisplayName:        "Guest Reader",
		Note:               "Temp friend access",
		QuotaBytes:         1024,
		AllowedFolders:     []string{"docs", "shared/photos"},
		MustChangePassword: true,
	})
	if err != nil {
		t.Fatalf("AdminCreateUser readonly: %v", err)
	}
	if len(genPwd) != 12 || ro.Role != RoleReadonly || ro.EffectiveCanUpload() || ro.EffectiveCanManageFolders() || ro.EffectiveCanUseAgents() {
		t.Fatalf("unexpected readonly user flags: pwdLen=%d user=%+v", len(genPwd), ro)
	}
	if !ro.MustChangePassword || ro.QuotaBytes != 1024 || ro.DisplayName != "Guest Reader" {
		t.Fatalf("expected metadata persisted: %+v", ro)
	}
	if !ro.CanAccessFolder("docs/report") || !ro.CanAccessFolder("shared/photos") || ro.CanAccessFolder("") || ro.CanAccessFolder("private") {
		t.Fatalf("unexpected folder access rules on %+v", ro.AllowedFolders)
	}

	// Cannot create Agent key for user without CanUseAgents.
	fBool := false
	userNoAgent, _, err := mgr.AdminCreateUser(AdminCreateUserInput{
		Username:     "dev_bob",
		Password:     "pass1234",
		Role:         RoleUser,
		CanUseAgents: &fBool,
	})
	if err != nil {
		t.Fatalf("AdminCreateUser bob: %v", err)
	}
	if userNoAgent.EffectiveCanUseAgents() || !userNoAgent.EffectiveCanUpload() {
		t.Fatalf("unexpected bob capability flags: %+v", userNoAgent)
	}

	// Enable Agents temporarily, create key, then disable via AdminUpdateUser -> AuthenticateAgentToken must fail.
	tBool := true
	if _, err := mgr.AdminUpdateUser(adminUser, "dev_bob", AdminUpdateUserInput{CanUseAgents: &tBool}); err != nil {
		t.Fatalf("enable agents: %v", err)
	}
	plainAgentTok, ak, err := mgr.CreateAgentKey(CreateAgentKeyInput{
		Name:          "bob-cli",
		OwnerUsername: "dev_bob",
		Scopes:        []string{ScopeRead, ScopeWrite},
		ReadFolders:   []string{"*"},
	})
	if err != nil {
		t.Fatalf("CreateAgentKey: %v", err)
	}
	if _, err := mgr.AuthenticateAgentToken(plainAgentTok, "127.0.0.1"); err != nil {
		t.Fatalf("expected agent auth success while allowed: %v", err)
	}
	disabledIDs, err := mgr.SetOwnerAgentKeysDisabled("dev_bob", true)
	if err != nil || len(disabledIDs) != 1 || disabledIDs[0] != ak.ID {
		t.Fatalf("SetOwnerAgentKeysDisabled freeze failed: ids=%v err=%v", disabledIDs, err)
	}
	if _, err := mgr.AuthenticateAgentToken(plainAgentTok, "127.0.0.1"); err == nil {
		t.Fatal("expected agent token rejected while frozen")
	}
	if _, err := mgr.SetOwnerAgentKeysDisabled("dev_bob", false); err != nil {
		t.Fatalf("unfreeze agent keys: %v", err)
	}
	if _, err := mgr.AdminUpdateUser(adminUser, "dev_bob", AdminUpdateUserInput{CanUseAgents: &fBool}); err != nil {
		t.Fatalf("disable CanUseAgents: %v", err)
	}
	if _, err := mgr.AuthenticateAgentToken(plainAgentTok, "127.0.0.1"); err == nil {
		t.Fatal("expected agent token rejected when owner CanUseAgents=false")
	}

	// 2. Cookie session version + APIToken revocation via ForceLogoutUser and AdminResetPassword.
	bobTok, err := mgr.RegenerateAPIToken("dev_bob")
	if err != nil || bobTok == "" {
		t.Fatalf("RegenerateAPIToken: %v", err)
	}
	rec := httptest.NewRecorder()
	if err := mgr.IssueCookie(rec, userNoAgent); err != nil {
		t.Fatalf("IssueCookie: %v", err)
	}
	cookie := rec.Result().Cookies()[0]
	reqWithCookie := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(cookie)
		return r
	}
	if gotU, ok := mgr.CurrentUser(reqWithCookie()); !ok || gotU.Username != "dev_bob" {
		t.Fatalf("expected valid cookie before force logout, ok=%v", ok)
	}

	if _, err := mgr.ForceLogoutUser(adminUser, "dev_bob"); err != nil {
		t.Fatalf("ForceLogoutUser: %v", err)
	}
	if _, ok := mgr.CurrentUser(reqWithCookie()); ok {
		t.Fatal("expected stale cookie rejected after ForceLogoutUser")
	}
	if _, ok := mgr.AuthenticateToken(bobTok); ok {
		t.Fatal("expected personal API token cleared by ForceLogoutUser")
	}

	// AdminResetPassword + RecordLogin + ChangePassword clears MustChangePassword.
	mgr.RecordLogin("dev_bob", "192.168.1.55")
	if gotU, ok := mgr.GetUser("dev_bob"); !ok || gotU.LastLoginIP != "192.168.1.55" || gotU.LastLoginAt == nil {
		t.Fatalf("expected RecordLogin recorded IP/timestamp, got %+v", gotU)
	}
	_, tempPwd, err := mgr.AdminResetPassword(adminUser, "dev_bob", "", true)
	if err != nil {
		t.Fatalf("AdminResetPassword: %v", err)
	}
	uAuth, code, err := mgr.AuthenticateWithReason("dev_bob", tempPwd)
	if err != nil || code != http.StatusOK || !uAuth.MustChangePassword {
		t.Fatalf("expected login with temp password and MustChangePassword=true, code=%d err=%v u=%+v", code, err, uAuth)
	}
	if err := mgr.ChangePassword("dev_bob", tempPwd, "newSecret88"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if gotU, _ := mgr.GetUser("dev_bob"); gotU.MustChangePassword {
		t.Fatal("expected MustChangePassword cleared after ChangePassword")
	}

	// 3. Expiration enforcement across login, cookie, and tokens.
	past := time.Now().Add(-10 * time.Minute)
	if _, err := mgr.AdminUpdateUser(adminUser, "dev_bob", AdminUpdateUserInput{ExpiresAt: &past}); err != nil {
		t.Fatalf("set expired: %v", err)
	}
	if _, status, err := mgr.AuthenticateWithReason("dev_bob", "newSecret88"); err == nil || status != http.StatusForbidden {
		t.Fatalf("expected 403 for expired user, got status=%d err=%v", status, err)
	}
	freshRec := httptest.NewRecorder()
	freshU, _ := mgr.GetUser("dev_bob")
	_ = mgr.IssueCookie(freshRec, freshU)
	rExp := httptest.NewRequest(http.MethodGet, "/", nil)
	rExp.AddCookie(freshRec.Result().Cookies()[0])
	if _, ok := mgr.CurrentUser(rExp); ok {
		t.Fatal("expected CurrentUser rejected for expired user")
	}
	if _, err := mgr.AdminUpdateUser(adminUser, "dev_bob", AdminUpdateUserInput{ClearExpiresAt: true}); err != nil {
		t.Fatalf("clear expires: %v", err)
	}

	// 4. Batch actions (set_quota, ban, unban).
	var q50 int64 = 50 * 1024 * 1024
	batchRes, err := mgr.BatchUsersAction(adminUser, []string{"dev_bob", adminUser}, "set_quota", &q50)
	if err != nil || len(batchRes.Updated) != 2 {
		t.Fatalf("BatchUsersAction set_quota failed: res=%+v err=%v", batchRes, err)
	}
	if gotU, _ := mgr.GetUser("dev_bob"); gotU.QuotaBytes != q50 {
		t.Fatalf("expected quota %d, got %d", q50, gotU.QuotaBytes)
	}
	banRes, err := mgr.BatchUsersAction(adminUser, []string{"dev_bob", adminUser}, "ban", nil)
	if err != nil || len(banRes.Updated) != 1 || len(banRes.Skipped) != 1 {
		t.Fatalf("expected ban dev_bob and skip admin self, got %+v err=%v", banRes, err)
	}
	counts := mgr.CountAgentKeysByOwner()
	if counts["dev_bob"][0] != 1 {
		t.Fatalf("expected CountAgentKeysByOwner[dev_bob][0]==1, got %+v", counts)
	}
}

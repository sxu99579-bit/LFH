package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentKeyLifecycleAndNoPlaintextOnDisk(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithOptions(dir, InitOptions{AdminUser: "rootadmin", AdminPass: "pass1234"})
	if err != nil {
		t.Fatalf("NewManagerWithOptions: %v", err)
	}
	inv, err := mgr.CreateInvite("rootadmin", "INV-AGK1", "agent test", 5, 24)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := mgr.Register("alice", "pass1234", inv.Code); err != nil {
		t.Fatalf("Register alice: %v", err)
	}

	plainToken, pub, err := mgr.CreateAgentKey(CreateAgentKeyInput{
		Name:                "B电脑-后端Agent",
		OwnerUsername:       "alice",
		ReadFolders:         []string{"shopapp/docs"},
		WriteFolders:        []string{"shopapp\\backend/"},
		DefaultAllowedUsers: []string{"rootadmin"},
		AllowSetVisibility:  false,
	})
	if err != nil {
		t.Fatalf("CreateAgentKey: %v", err)
	}
	if !strings.HasPrefix(plainToken, AgentTokenPrefix) {
		t.Fatalf("expected prefix %q, got %q", AgentTokenPrefix, plainToken)
	}
	if pub.TokenPrefix != plainToken[:15]+"..." {
		t.Fatalf("unexpected token prefix: got %q want %q", pub.TokenPrefix, plainToken[:15]+"...")
	}
	if strings.Join(pub.Scopes, ",") != "read,write,share" {
		t.Fatalf("expected default scopes read,write,share without delete, got %+v", pub.Scopes)
	}
	if len(pub.WriteFolders) != 1 || pub.WriteFolders[0] != "shopapp/backend" {
		t.Fatalf("expected normalized WriteFolders, got %+v", pub.WriteFolders)
	}

	diskRaw, err := os.ReadFile(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatalf("ReadFile users.json: %v", err)
	}
	if strings.Contains(string(diskRaw), plainToken) {
		t.Fatalf("users.json must never store plaintext agent token")
	}

	// Reload from disk.
	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatalf("reload NewManager: %v", err)
	}
	p, err := reloaded.AuthenticateAgentToken(plainToken, "192.168.3.53")
	if err != nil {
		t.Fatalf("AuthenticateAgentToken after reload: %v", err)
	}
	if !p.IsAgent || p.AgentKey == nil || p.User.Username != "alice" || p.AgentKey.ID != pub.ID {
		t.Fatalf("unexpected principal after reload: %+v", p)
	}
	if p.AgentKey.LastUsedAt == nil || p.AgentKey.LastUsedIP != "192.168.3.53" {
		t.Fatalf("expected LastUsedAt and LastUsedIP populated, got %+v", p.AgentKey)
	}

	// Verify cluster snapshot preserves AgentKeys across nodes.
	snap, err := reloaded.ExportSnapshot()
	if err != nil {
		t.Fatalf("ExportSnapshot: %v", err)
	}
	dstDir := t.TempDir()
	dst, err := NewManagerWithOptions(dstDir, InitOptions{AdminUser: "workeradmin", AdminPass: "pass1234"})
	if err != nil {
		t.Fatalf("dst NewManagerWithOptions: %v", err)
	}
	if err := dst.ImportSnapshot(snap); err != nil {
		t.Fatalf("ImportSnapshot: %v", err)
	}
	pDst, err := dst.AuthenticateAgentToken(plainToken, "10.0.0.2")
	if err != nil || !pDst.IsAgent || pDst.AgentKey == nil || pDst.AgentKey.Name != pub.Name {
		t.Fatalf("expected agent key usable after snapshot import: p=%+v err=%v", pDst, err)
	}

	// CurrentUser must continue rejecting Agent keys (only cookie and personal lfh_tok_).
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+plainToken)
	if _, ok := reloaded.CurrentUser(req); ok {
		t.Fatalf("CurrentUser must reject AgentKey tokens")
	}
	pReq, err := reloaded.CurrentPrincipal(req)
	if err != nil || !pReq.IsAgent || pReq.AgentKey.ID != pub.ID {
		t.Fatalf("CurrentPrincipal via Bearer failed: p=%+v err=%v", pReq, err)
	}
}

func TestAgentKeyFolderBoundaryAndPermissions(t *testing.T) {
	if _, err := ValidateFolderRules([]string{"../etc"}); err == nil {
		t.Fatalf("expected error for path traversal in folder rules")
	}
	if _, err := ValidateFolderRules([]string{"shopapp/../secret"}); err == nil {
		t.Fatalf("expected error for nested .. segment in folder rules")
	}
	if _, err := ValidateFolderRules([]string{"shopapp/*/api"}); err == nil {
		t.Fatalf("expected error for wildcard mixed inside path")
	}
	norm, err := ValidateFolderRules([]string{"/", "ShopApp\\Backend/", "shopapp/./backend//", "*"})
	if err != nil {
		t.Fatalf("ValidateFolderRules: %v", err)
	}
	if len(norm) != 3 || norm[0] != "" || norm[1] != "ShopApp/Backend" || norm[2] != "*" {
		t.Fatalf("unexpected normalized rules: %+v", norm)
	}

	cases := []struct {
		target string
		prefix string
		want   bool
	}{
		{"shopapp/backend", "shopapp/backend", true},
		{"shopapp/backend/handlers", "shopapp/backend", true},
		{"ShopApp\\Backend\\v1", "shopapp/backend", true},
		{"shopapp/backend2", "shopapp/backend", false},
		{"shopapp/back", "shopapp/backend", false},
		{"shopapp", "shopapp/backend", false},
		{"", "shopapp/backend", false},
		{"", "", true},
		{"shopapp", "", false},
		{"any/folder", "*", true},
		{"", "*", true},
		{"../escape", "*", false},
	}
	for _, tc := range cases {
		if got := FolderMatchesPrefix(tc.target, tc.prefix); got != tc.want {
			t.Fatalf("FolderMatchesPrefix(%q, %q) = %v, want %v", tc.target, tc.prefix, got, tc.want)
		}
	}

	denyAll := Principal{
		User:    PublicUser{Username: "alice", Role: "user"},
		IsAgent: true,
		AgentKey: &PublicAgentKey{
			ID:     "agk_empty",
			Name:   "empty",
			Scopes: []string{ScopeRead, ScopeWrite, ScopeShare, ScopeDelete},
		},
	}
	if denyAll.CanReadFolder("") || denyAll.CanReadFolder("docs") || denyAll.CanWriteFolder("") {
		t.Fatalf("empty ReadFolders and WriteFolders must deny by default")
	}

	scoped := Principal{
		User:    PublicUser{Username: "alice", Role: "user"},
		IsAgent: true,
		AgentKey: &PublicAgentKey{
			ID:           "agk_scoped",
			Name:         "backend-bot",
			Scopes:       []string{ScopeRead, ScopeWrite},
			ReadFolders:  []string{"shopapp/docs"},
			WriteFolders: []string{"shopapp/backend"},
		},
	}
	if !scoped.CanReadFolder("shopapp/docs/api") || !scoped.CanReadFolder("shopapp/backend/main") {
		t.Fatalf("expected read access on docs and implicit read on WriteFolders")
	}
	if scoped.CanReadFolder("shopapp/backend2") || scoped.CanReadFolder("") {
		t.Fatalf("unexpected read access outside allowed boundaries")
	}
	if !scoped.CanWriteFolder("shopapp/backend") || !scoped.CanWriteFolder("shopapp/backend/sub") {
		t.Fatalf("expected write access on shopapp/backend")
	}
	if scoped.CanWriteFolder("shopapp/docs") || scoped.CanWriteFolder("shopapp/backend2") {
		t.Fatalf("unexpected write access to read-only or sibling directory")
	}
	if scoped.CanDeleteFolder("shopapp/backend") || scoped.CanShareFolder("shopapp/backend") {
		t.Fatalf("expected delete and share denied when scope/flag missing")
	}
}

func TestAgentKeyDisableExpireBanDeleteAndRateLimit(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManagerWithOptions(dir, InitOptions{AdminUser: "rootadmin", AdminPass: "pass1234"})
	if err != nil {
		t.Fatalf("NewManagerWithOptions: %v", err)
	}
	inv, err := mgr.CreateInvite("rootadmin", "INV-AGK2", "lifecycle", 5, 24)
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if _, err := mgr.Register("alice", "pass1234", inv.Code); err != nil {
		t.Fatalf("Register alice: %v", err)
	}
	if _, err := mgr.Register("bob", "pass1234", inv.Code); err != nil {
		t.Fatalf("Register bob: %v", err)
	}

	plainToken, pub, err := mgr.CreateAgentKey(CreateAgentKeyInput{
		Name:            "rate-key",
		OwnerUsername:   "alice",
		WriteFolders:    []string{"*"},
		RateLimitPerMin: 3,
	})
	if err != nil {
		t.Fatalf("CreateAgentKey: %v", err)
	}

	// Non-owner non-admin cannot update or delete.
	newName := "hijacked"
	if _, err := mgr.UpdateAgentKey(pub.ID, "bob", "user", UpdateAgentKeyInput{Name: &newName}); err == nil {
		t.Fatalf("expected bob forbidden from updating alice key")
	}
	if err := mgr.DeleteAgentKey(pub.ID, "bob", "user"); err == nil {
		t.Fatalf("expected bob forbidden from deleting alice key")
	}

	// Empty scopes update rejected.
	emptyScopes := []string{}
	if _, err := mgr.UpdateAgentKey(pub.ID, "alice", "user", UpdateAgentKeyInput{Scopes: &emptyScopes}); err == nil {
		t.Fatalf("expected empty scopes rejected on update")
	}

	// Disable and re-enable.
	if _, err := mgr.SetAgentKeyDisabled(pub.ID, "alice", "user", true); err != nil {
		t.Fatalf("SetAgentKeyDisabled true: %v", err)
	}
	if _, err := mgr.AuthenticateAgentToken(plainToken, "127.0.0.1"); err == nil {
		t.Fatalf("expected disabled key rejected")
	}
	if _, err := mgr.SetAgentKeyDisabled(pub.ID, "alice", "user", false); err != nil {
		t.Fatalf("SetAgentKeyDisabled false: %v", err)
	}

	// Expired key rejected, then ClearExpiresAt restores access.
	past := time.Now().Add(-time.Minute)
	if _, err := mgr.UpdateAgentKey(pub.ID, "rootadmin", "admin", UpdateAgentKeyInput{ExpiresAt: &past}); err != nil {
		t.Fatalf("UpdateAgentKey expires: %v", err)
	}
	if _, err := mgr.AuthenticateAgentToken(plainToken, "127.0.0.1"); err == nil {
		t.Fatalf("expected expired key rejected")
	}
	if _, err := mgr.UpdateAgentKey(pub.ID, "rootadmin", "admin", UpdateAgentKeyInput{ClearExpiresAt: true}); err != nil {
		t.Fatalf("ClearExpiresAt: %v", err)
	}

	// Banned owner rejects agent key.
	if _, err := mgr.SetUserBanned("rootadmin", "alice", true); err != nil {
		t.Fatalf("SetUserBanned: %v", err)
	}
	if _, err := mgr.AuthenticateAgentToken(plainToken, "127.0.0.1"); err == nil {
		t.Fatalf("expected key rejected while owner is banned")
	}
	if _, err := mgr.SetUserBanned("rootadmin", "alice", false); err != nil {
		t.Fatalf("unban alice: %v", err)
	}

	// Rate limit (3/min allowed, 4th blocked with ErrRateLimited).
	for i := 0; i < 3; i++ {
		if _, err := mgr.AuthenticateAgentToken(plainToken, "127.0.0.1"); err != nil {
			t.Fatalf("attempt %d failed unexpectedly: %v", i+1, err)
		}
	}
	if _, err := mgr.AuthenticateAgentToken(plainToken, "127.0.0.1"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited on 4th request, got %v", err)
	}

	// Deleting owner deletes associated AgentKeys too.
	if err := mgr.DeleteUser("rootadmin", "alice"); err != nil {
		t.Fatalf("DeleteUser alice: %v", err)
	}
	if _, ok := mgr.GetAgentKey(pub.ID); ok {
		t.Fatalf("expected agent key removed when owner deleted")
	}
}

func TestPrincipalResolveAllowedUsers(t *testing.T) {
	human := Principal{User: PublicUser{Username: "alice", Role: "user"}}
	if got, err := human.ResolveAllowedUsers([]string{"bob"}); err != nil || len(got) != 1 || got[0] != "bob" {
		t.Fatalf("unexpected human ResolveAllowedUsers: got=%+v err=%v", got, err)
	}

	lockedVis := Principal{
		User:    PublicUser{Username: "alice", Role: "user"},
		IsAgent: true,
		AgentKey: &PublicAgentKey{
			ID:                  "agk_locked",
			Name:                "locked-vis",
			DefaultAllowedUsers: []string{"bob"},
			AllowSetVisibility:  false,
		},
	}
	if _, err := lockedVis.ResolveAllowedUsers([]string{"charlie"}); err == nil {
		t.Fatalf("expected error overriding visibility when AllowSetVisibility is false")
	}
	gotDefault, err := lockedVis.ResolveAllowedUsers([]string{"  "})
	if err != nil {
		t.Fatalf("ResolveAllowedUsers default fallback: %v", err)
	}
	if strings.Join(gotDefault, ",") != "bob,alice" {
		t.Fatalf("expected [bob alice] (auto-including owner), got %+v", gotDefault)
	}

	flexVis := Principal{
		User:    PublicUser{Username: "alice", Role: "user"},
		IsAgent: true,
		AgentKey: &PublicAgentKey{
			ID:                  "agk_flex",
			Name:                "flex-vis",
			DefaultAllowedUsers: []string{"bob"},
			AllowSetVisibility:  true,
		},
	}
	gotCustom, err := flexVis.ResolveAllowedUsers([]string{"charlie"})
	if err != nil || strings.Join(gotCustom, ",") != "charlie,alice" {
		t.Fatalf("expected custom + owner, got=%+v err=%v", gotCustom, err)
	}
}

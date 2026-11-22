package security

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginLimiterLockoutAndReset(t *testing.T) {
	lim := NewLoginLimiter(8, 5*time.Minute, 5*time.Minute)
	ip := "192.168.1.42"

	for i := 0; i < 7; i++ {
		if ok, _ := lim.Allow(ip); !ok {
			t.Fatalf("attempt %d unexpectedly blocked", i+1)
		}
		lim.RecordFailure(ip)
	}
	if ok, _ := lim.Allow(ip); !ok {
		t.Fatal("7 failures should still allow 8th attempt")
	}
	lim.RecordFailure(ip)

	if ok, retry := lim.Allow(ip); ok || retry <= 0 {
		t.Fatalf("expected IP locked after 8 failures, got ok=%v retry=%v", ok, retry)
	}
	if ok, _ := lim.Allow("192.168.1.99"); !ok {
		t.Fatal("different IP must remain unaffected")
	}

	lim.Reset(ip)
	if ok, _ := lim.Allow(ip); !ok {
		t.Fatal("expected IP unlocked after Reset")
	}
}

func TestAuditLoggerPersistenceAndRingCap(t *testing.T) {
	dir := t.TempDir()
	al, err := newAuditLoggerWithCap(dir, 5)
	if err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 7; i++ {
		al.Log("admin", "test_action", fmt.Sprintf("item-%d", i), "127.0.0.1", "ok", "detail")
	}
	got := al.List(10)
	if len(got) != 5 {
		t.Fatalf("expected 5 capped entries, got %d", len(got))
	}
	if got[0].Target != "item-7" || got[4].Target != "item-3" {
		t.Fatalf("unexpected newest-first ring order: first=%s last=%s", got[0].Target, got[4].Target)
	}

	reloaded, err := newAuditLoggerWithCap(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	got2 := reloaded.List(3)
	if len(got2) != 3 || got2[0].Target != "item-7" || got2[2].Target != "item-5" {
		t.Fatalf("unexpected reloaded entries: %+v", got2)
	}
}

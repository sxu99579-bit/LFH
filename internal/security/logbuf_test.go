package security

import (
	"fmt"
	"testing"
)

func TestLogBufferCaptureFilterAndCap(t *testing.T) {
	buf := NewLogBuffer(4)
	_, _ = buf.Write([]byte("2026/11/22 15:20:00 [http] listening on :8080\n"))
	_, _ = buf.Write([]byte("2026/11/22 15:20:01 [cluster] heartbeat timeout warning\n"))
	_, _ = buf.Write([]byte("2026/11/22 15:20:02 [updater] download failed: network error\n"))
	_, _ = buf.Write([]byte("2026/11/22 15:20:03 [system] ready\n"))
	_, _ = buf.Write([]byte("2026/11/22 15:20:04 [clipboard] captured screenshot\n"))

	entries, total, counts := buf.List(10, "", "")
	if total != 4 || len(entries) != 4 {
		t.Fatalf("expected 4 capped entries, total=%d len=%d", total, len(entries))
	}
	// Newest first: clipboard, system, updater, cluster (http evicted by cap=4).
	if entries[0].Source != "clipboard" || entries[3].Source != "cluster" {
		t.Fatalf("unexpected ordering/eviction: %+v", entries)
	}
	if counts["ERROR"] != 1 || counts["WARN"] != 1 || counts["INFO"] != 2 {
		t.Fatalf("unexpected level counts: %+v", counts)
	}

	errOnly, _, _ := buf.List(10, "error", "updater")
	if len(errOnly) != 1 || errOnly[0].Level != "ERROR" || errOnly[0].Source != "updater" {
		t.Fatalf("unexpected filtered error entries: %+v", errOnly)
	}

	// Verify partial chunk buffering works too.
	_, _ = fmt.Fprint(buf, "[http] part1 ")
	_, _ = fmt.Fprintln(buf, "part2")
	latest, _, _ := buf.List(1, "", "")
	if len(latest) != 1 || latest[0].Message != "part1 part2" {
		t.Fatalf("expected joined partial chunk line, got %+v", latest)
	}

	buf.Clear()
	afterClear, totalAfter, _ := buf.List(10, "", "")
	if len(afterClear) != 0 || totalAfter != 0 {
		t.Fatalf("expected empty buffer after Clear, got len=%d total=%d", len(afterClear), totalAfter)
	}
}

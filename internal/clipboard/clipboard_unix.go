//go:build !windows

package clipboard

import (
	"os/exec"
	"strings"
	"time"
)

const defaultPollInterval = 1500 * time.Millisecond

func clipboardSequence() uint32 {
	return 0
}

func readClipboard() (Item, bool) {
	candidates := [][]string{
		{"pbpaste"},
		{"wl-paste", "--no-newline"},
		{"xclip", "-o", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--output"},
	}
	for _, cmdArgs := range candidates {
		bin, err := exec.LookPath(cmdArgs[0])
		if err != nil {
			continue
		}
		out, err := exec.Command(bin, cmdArgs[1:]...).Output()
		if err != nil || len(out) == 0 || len(out) > MaxCaptureBytes {
			continue
		}
		if strings.TrimSpace(string(out)) == "" {
			continue
		}
		return Item{
			Kind:      "text",
			Ext:       ".txt",
			Data:      out,
			Hash:      hashBytesHex(out),
			CreatedAt: time.Now(),
		}, true
	}
	return Item{}, false
}

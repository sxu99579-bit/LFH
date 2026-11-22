//go:build windows

package cluster

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type hostHardwareInfo struct {
	CPUModel    string
	GPUName     string
	HasGPU      bool
	LANLinkMbps int64
}

var (
	hwMu     sync.RWMutex
	hwCached hostHardwareInfo
	hwOnce   sync.Once
)

func getCachedHostHardware() hostHardwareInfo {
	hwOnce.Do(func() {
		hwMu.Lock()
		if envCPU := strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER")); envCPU != "" {
			hwCached.CPUModel = envCPU
		}
		hwMu.Unlock()
		go refreshHostHardwareAsync()
	})
	hwMu.RLock()
	defer hwMu.RUnlock()
	return hwCached
}

func refreshHostHardwareAsync() {
	// Step 1 (fast <30ms): Query friendly CPU model name directly from registry.
	regCtx, regCancel := context.WithTimeout(context.Background(), 3*time.Second)
	if regOut, err := exec.CommandContext(regCtx, "reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "/v", "ProcessorNameString").Output(); err == nil {
		for _, line := range strings.Split(string(regOut), "\n") {
			if idx := strings.Index(line, "REG_SZ"); idx >= 0 {
				model := strings.Join(strings.Fields(strings.TrimSpace(line[idx+len("REG_SZ"):])), " ")
				if model != "" {
					hwMu.Lock()
					hwCached.CPUModel = model
					hwMu.Unlock()
					break
				}
			}
		}
	}
	regCancel()

	// Step 2 (fast <80ms): Query NVIDIA GPU directly via nvidia-smi if available.
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		gpuCtx, gpuCancel := context.WithTimeout(context.Background(), 3*time.Second)
		if nOut, nErr := exec.CommandContext(gpuCtx, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output(); nErr == nil {
			first := strings.TrimSpace(strings.Split(strings.TrimSpace(string(nOut)), "\n")[0])
			if first != "" {
				hwMu.Lock()
				hwCached.GPUName = first
				hwCached.HasGPU = true
				hwMu.Unlock()
			}
		}
		gpuCancel()
	}

	// Step 3 (background ~12s cold PowerShell): Query active physical NIC link speed and WMI GPU fallback.
	psCtx, psCancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer psCancel()
	psScript := `$ErrorActionPreference='SilentlyContinue'; ` +
		`$gpus=Get-CimInstance Win32_VideoController | Select-Object -ExpandProperty Name; ` +
		`$nic=(Get-NetAdapter | Where-Object { $_.Status -eq 'Up' -and $_.InterfaceDescription -notmatch 'Loopback|Virtual|Hyper-V|VMware|TAP|Tailscale|ZeroTier' } | Sort-Object ReceiveLinkSpeed -Descending | Select-Object -First 1 -ExpandProperty ReceiveLinkSpeed); ` +
		`"GPUS=" + (($gpus | Where-Object { $_ }) -join ';'); "NIC=$nic"`

	if out, err := exec.CommandContext(psCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", psScript).Output(); err == nil {
		hwMu.Lock()
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "GPUS="):
				if !hwCached.HasGPU {
					rawGPUs := strings.TrimSpace(strings.TrimPrefix(line, "GPUS="))
					pickBestGPU(&hwCached, strings.Split(rawGPUs, ";"))
				}
			case strings.HasPrefix(line, "NIC="):
				if bps, pErr := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "NIC=")), 10, 64); pErr == nil && bps > 0 {
					hwCached.LANLinkMbps = bps / 1_000_000
				}
			}
		}
		hwMu.Unlock()
	}
}

func pickBestGPU(info *hostHardwareInfo, names []string) {
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		low := strings.ToLower(name)
		if strings.Contains(low, "microsoft basic") || strings.Contains(low, "remote") || strings.Contains(low, "oray") {
			continue
		}
		isDiscrete := strings.Contains(low, "nvidia") || strings.Contains(low, "geforce") || strings.Contains(low, "rtx") ||
			strings.Contains(low, "gtx") || strings.Contains(low, "quadro") || strings.Contains(low, "radeon rx") ||
			strings.Contains(low, "intel arc")
		if isDiscrete {
			info.GPUName = name
			info.HasGPU = true
			return
		}
		if info.GPUName == "" {
			info.GPUName = name
		}
	}
}

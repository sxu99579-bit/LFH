package cluster

import (
	"net"
	"strings"
)

type nicCandidate struct {
	Name          string
	Description   string
	InterfaceType string
	SpeedBps      int64
	IPv4s         []string
}

var physicalInterfaceTypes = map[string]bool{
	"ethernet":        true,
	"gigabitethernet": true,
	"fastethernett":   true,
	"fastethernetfx":  true,
	"wireless80211":   true,
	"6":               true, // IF_TYPE_ETHERNET_CSMACD
	"62":              true, // IF_TYPE_FASTETHER
	"69":              true, // IF_TYPE_FASTETHER_FX
	"71":              true, // IF_TYPE_IEEE80211
	"117":             true, // IF_TYPE_GIGABITETHERNET
}

var virtualAdapterKeywords = []string{
	"loopback",
	"virtual",
	"hyper-v",
	"vethernet",
	"vmware",
	"vmnet",
	"virtualbox",
	"vbox",
	"tap",
	"tun",
	"tunnel",
	"tailscale",
	"zerotier",
	"wintun",
	"wireguard",
	"mihomo",
	"clash",
	"meta",
	"sing-box",
	"radmin",
	"hamachi",
	"bluetooth",
	"wsl",
	"ndiswan",
	"ras",
	"ppp",
	"vpn",
	"docker",
	"npcap",
	"bridge",
	"veth",
	"virbr",
	"utun",
	"wan miniport",
	"teredo",
	"6to4",
	"ip-https",
	"isatap",
}

func isPhysicalAdapterCandidate(c nicCandidate) bool {
	if c.SpeedBps <= 0 {
		return false
	}
	if typ := strings.ToLower(strings.TrimSpace(c.InterfaceType)); typ != "" {
		if !physicalInterfaceTypes[typ] {
			return false
		}
	}
	combined := strings.ToLower(c.Name + " " + c.Description)
	for _, kw := range virtualAdapterKeywords {
		if strings.Contains(combined, kw) {
			return false
		}
	}
	return true
}

func isRealPrivateLANIPv4(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if v4.IsLoopback() || v4.IsLinkLocalUnicast() {
		return false
	}
	// Exclude CGNAT 100.64.0.0/10 (Tailscale) and benchmark/Fake-IP 198.18.0.0/15 (Clash/Mihomo).
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	if v4[0] == 198 && (v4[1] == 18 || v4[1] == 19) {
		return false
	}
	return v4.IsPrivate()
}

// selectBestLANLinkMbps filters out virtual/VPN tunnel adapters (e.g. Mihomo/Tailscale/Hyper-V/VMware)
// and picks the real physical NIC link speed in Mbps, preferring adapters holding a real private LAN IPv4.
func selectBestLANLinkMbps(candidates []nicCandidate) int64 {
	var bestLANBps int64
	var bestFallbackBps int64

	for _, c := range candidates {
		if !isPhysicalAdapterCandidate(c) {
			continue
		}
		hasLANIP := false
		for _, ip := range c.IPv4s {
			if isRealPrivateLANIPv4(ip) {
				hasLANIP = true
				break
			}
		}
		if hasLANIP && c.SpeedBps > bestLANBps {
			bestLANBps = c.SpeedBps
		}
		if c.SpeedBps > bestFallbackBps {
			bestFallbackBps = c.SpeedBps
		}
	}

	chosen := bestLANBps
	if chosen <= 0 {
		chosen = bestFallbackBps
	}
	if chosen <= 0 {
		return 0
	}
	return (chosen + 500_000) / 1_000_000
}

package bridge

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	TicketActionUp   = "up"
	TicketActionDown = "down"

	DefaultTicketTTLSec   = 600
	MinTicketTTLSec       = 10
	MaxTicketTTLSec       = 86400
	DefaultTicketMaxBytes = int64(2 << 30) // 2GB safety cap
)

var (
	ErrTicketInvalid  = errors.New("传输短链签名无效或参数损坏")
	ErrTicketExpired  = errors.New("传输短链已过期")
	ErrTicketReplayed = errors.New("一次性上传短链已被使用，请重新签发")
)

var tailscaleCGNAT *net.IPNet

func init() {
	_, tailscaleCGNAT, _ = net.ParseCIDR("100.64.0.0/10")
}

// TransferTicket holds an HMAC-authenticated presigned upload or download grant.
type TransferTicket struct {
	Action       string   `json:"a"`             // "up" | "down"
	NodeID       string   `json:"n"`             // target storage node ID
	FileID       string   `json:"fid,omitempty"` // target file ID for "down"
	FolderPath   string   `json:"fp,omitempty"`  // target folder for "up"
	FileName     string   `json:"fn,omitempty"`  // target filename for "up"
	AllowedUsers []string `json:"au,omitempty"`
	ActorUser    string   `json:"u"`
	ActorDevice  string   `json:"d,omitempty"`
	ActorKeyID   string   `json:"kid,omitempty"`
	MaxBytes     int64    `json:"mb,omitempty"`
	Exp          int64    `json:"exp"`
	Nonce        string   `json:"nc"`
}

func randomHexNonce(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func computeTicketSig(secret, payloadB64 string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("lfh-ticket-v1|"))
	mac.Write([]byte(payloadB64))
	return mac.Sum(nil)
}

// SignTicket normalizes defaults and computes base64url payload + hex HMAC-SHA256 signature.
func SignTicket(secret string, t TransferTicket) (payloadB64, sigHex string, err error) {
	if strings.TrimSpace(secret) == "" {
		return "", "", errors.New("票据签名密钥不能为空")
	}
	t.Action = strings.ToLower(strings.TrimSpace(t.Action))
	if t.Action != TicketActionUp && t.Action != TicketActionDown {
		return "", "", errors.New("ticket action 必须为 up 或 down")
	}
	if strings.TrimSpace(t.Nonce) == "" {
		t.Nonce = randomHexNonce(12)
	}
	now := time.Now().Unix()
	if t.Exp <= 0 {
		t.Exp = now + DefaultTicketTTLSec
	} else {
		ttl := t.Exp - now
		if ttl < MinTicketTTLSec {
			t.Exp = now + MinTicketTTLSec
		} else if ttl > MaxTicketTTLSec {
			t.Exp = now + MaxTicketTTLSec
		}
	}
	if t.MaxBytes <= 0 && t.Action == TicketActionUp {
		t.MaxBytes = DefaultTicketMaxBytes
	}

	raw, err := json.Marshal(t)
	if err != nil {
		return "", "", err
	}
	payloadB64 = base64.RawURLEncoding.EncodeToString(raw)
	sig := computeTicketSig(secret, payloadB64)
	return payloadB64, hex.EncodeToString(sig), nil
}

// VerifyTicket checks HMAC-SHA256 signature and expiration against now.
func VerifyTicket(secret, payloadB64, sigHex string, now time.Time) (TransferTicket, error) {
	p := strings.TrimSpace(payloadB64)
	s := strings.TrimSpace(strings.ToLower(sigHex))
	if strings.TrimSpace(secret) == "" || p == "" || s == "" {
		return TransferTicket{}, ErrTicketInvalid
	}
	gotSig, err := hex.DecodeString(s)
	if err != nil || len(gotSig) != sha256.Size {
		return TransferTicket{}, ErrTicketInvalid
	}
	wantSig := computeTicketSig(secret, p)
	if !hmac.Equal(gotSig, wantSig) {
		return TransferTicket{}, ErrTicketInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return TransferTicket{}, ErrTicketInvalid
	}
	var t TransferTicket
	if err := json.Unmarshal(raw, &t); err != nil {
		return TransferTicket{}, ErrTicketInvalid
	}
	t.Action = strings.ToLower(strings.TrimSpace(t.Action))
	if t.Action != TicketActionUp && t.Action != TicketActionDown {
		return TransferTicket{}, ErrTicketInvalid
	}
	if strings.TrimSpace(t.Nonce) == "" {
		return TransferTicket{}, ErrTicketInvalid
	}
	if now.Unix() > t.Exp {
		return TransferTicket{}, ErrTicketExpired
	}
	return t, nil
}

// TicketReplayGuard prevents one-shot upload tickets from being replayed before expiry.
type TicketReplayGuard struct {
	mu   sync.Mutex
	used map[string]int64
}

func NewTicketReplayGuard() *TicketReplayGuard {
	return &TicketReplayGuard{
		used: make(map[string]int64),
	}
}

// Consume records nonce once and returns false if it has already been used.
func (g *TicketReplayGuard) Consume(nonce string, exp int64, now time.Time) bool {
	nc := strings.TrimSpace(nonce)
	if nc == "" {
		return false
	}
	nowUnix := now.Unix()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.used == nil {
		g.used = make(map[string]int64)
	}
	for k, v := range g.used {
		if v < nowUnix {
			delete(g.used, k)
		}
	}
	if _, exists := g.used[nc]; exists {
		return false
	}
	g.used[nc] = exp
	return true
}

// IsPrivateOrTailscaleAddr reports whether remoteAddr belongs to loopback, LAN RFC1918, ULA, or Tailscale CGNAT (100.64.0.0/10).
func IsPrivateOrTailscaleAddr(remoteAddr string) bool {
	s := strings.TrimSpace(remoteAddr)
	if s == "" {
		return false
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	s = strings.Trim(s, "[]")
	if zoneIdx := strings.IndexByte(s, '%'); zoneIdx >= 0 {
		s = s[:zoneIdx]
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	if tailscaleCGNAT != nil && tailscaleCGNAT.Contains(ip) {
		return true
	}
	return false
}

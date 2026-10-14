package licensecode

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Only the public key is embedded in the customer executable.
//
//go:embed public.key
var PublicKeyPEM []byte

type Payload struct {
	Product        string   `json:"product"`
	LicenseID      string   `json:"license_id"`
	CustomerID     string   `json:"customer_id"`
	InstallationID string   `json:"installation_id"`
	LicenseType    string   `json:"license_type"`
	VersionFrom    string   `json:"version_from"`
	VersionTo      string   `json:"version_to"`
	IssuedAt       string   `json:"issued_at"`
	Features       []string `json:"features"`
}

func PublicKey(data []byte) (ed25519.PublicKey, error) {
	if len(data) == ed25519.PublicKeySize {
		return ed25519.PublicKey(data), nil
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("授权公钥格式无效")
	}
	key, e := x509.ParsePKIXPublicKey(block.Bytes)
	if e != nil {
		return nil, e
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("需要 Ed25519 公钥")
	}
	return pub, nil
}
func PrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("发行方私钥格式无效")
	}
	key, e := x509.ParsePKCS8PrivateKey(block.Bytes)
	if e != nil {
		return nil, e
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("需要 Ed25519 私钥")
	}
	return private, nil
}
func Generate() (private, public []byte, err error) {
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	pr, e := x509.MarshalPKCS8PrivateKey(priv)
	if e != nil {
		return nil, nil, e
	}
	pu, e := x509.MarshalPKIXPublicKey(pub)
	if e != nil {
		return nil, nil, e
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pr}), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pu}), nil
}
func NewPayload(customer, installation, from, to string, features []string) Payload {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return Payload{"RUILIAN_ERP", hex.EncodeToString(b), customer, installation, "PERPETUAL", from, to, time.Now().UTC().Format(time.RFC3339), features}
}
func Encode(payload Payload, private ed25519.PrivateKey) (string, error) {
	if payload.CustomerID == "" || payload.InstallationID == "" {
		return "", fmt.Errorf("客户编号、Installation ID 必填")
	}
	b, e := json.Marshal(payload)
	if e != nil {
		return "", e
	}
	sig := ed25519.Sign(private, b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
func Decode(token string, public ed25519.PublicKey) (Payload, []byte, []byte, error) {
	var p Payload
	token = strings.TrimSpace(token)
	if len(token) > 20000 {
		return p, nil, nil, fmt.Errorf("授权码过长")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return p, nil, nil, fmt.Errorf("授权码格式无效")
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return p, nil, nil, fmt.Errorf("授权载荷无效")
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(public, raw, sig) {
		return p, nil, nil, fmt.Errorf("授权签名校验失败")
	}
	if e = json.Unmarshal(raw, &p); e != nil {
		return p, nil, nil, fmt.Errorf("授权载荷不是有效 JSON")
	}
	return p, raw, sig, nil
}
func VerifyPayload(p Payload, installation, version string) error {
	if p.Product != "RUILIAN_ERP" || p.LicenseType != "PERPETUAL" || p.LicenseID == "" || p.CustomerID == "" {
		return fmt.Errorf("授权产品或授权类型不匹配")
	}
	if p.InstallationID != installation {
		return fmt.Errorf("授权码与本机 Installation ID 不匹配")
	}
	v, e := semver(version)
	if e != nil {
		return e
	}
	lo, e := semver(p.VersionFrom)
	if e != nil {
		return e
	}
	hi, e := semver(p.VersionTo)
	if e != nil {
		return e
	}
	if v < lo || v > hi {
		return fmt.Errorf("当前软件版本不在授权范围内")
	}
	t, e := time.Parse(time.RFC3339, p.IssuedAt)
	if e != nil || t.After(time.Now().UTC().Add(5*time.Minute)) {
		return fmt.Errorf("授权生成时间无效，请检查本机时间")
	}
	if len(p.Features) == 0 {
		return fmt.Errorf("授权功能集合为空")
	}
	return nil
}
func semver(s string) (int64, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("版本须为三段数字，如 1.1.0")
	}
	var v int64
	for _, p := range parts {
		n, e := strconv.ParseInt(p, 10, 64)
		if e != nil || n < 0 || n > 9999 {
			return 0, fmt.Errorf("版本格式无效")
		}
		v = v*10000 + n
	}
	return v, nil
}

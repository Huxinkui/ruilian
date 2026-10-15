package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const captchaCharset = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

var captchaColors = []string{
	"#1e3a8a", "#047857", "#b91c1c", "#7c2d12",
	"#581c87", "#3730a3", "#991b1b", "#065f46",
	"#155e75", "#854d0e", "#4c1d95", "#9a3412",
}

var noiseColors = []string{
	"#94a3b8", "#cbd5e1", "#64748b", "#cbd5e1", "#a1a1aa",
}

type CaptchaEntry struct {
	Code      string
	ExpiresAt time.Time
}

type CaptchaStore struct {
	mu    sync.RWMutex
	items map[string]CaptchaEntry
}

func NewCaptchaStore() *CaptchaStore {
	return &CaptchaStore{
		items: make(map[string]CaptchaEntry),
	}
}

func (s *CaptchaStore) cleanupLocked() {
	now := time.Now()
	for k, v := range s.items {
		if now.After(v.ExpiresAt) {
			delete(s.items, k)
		}
	}
}

func randInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(n.Int64())
}

// Generate creates a 4-character captcha, stores it for 3 minutes, and returns the ID and base64 data URL.
func (s *CaptchaStore) Generate() (string, string) {
	s.mu.Lock()
	if len(s.items) > 100 {
		s.cleanupLocked()
	}
	s.mu.Unlock()

	// 1. Generate 4 random characters
	codeChars := make([]byte, 4)
	for i := 0; i < 4; i++ {
		codeChars[i] = captchaCharset[randInt(len(captchaCharset))]
	}
	code := string(codeChars)
	id := uuid.New().String()

	s.mu.Lock()
	s.items[id] = CaptchaEntry{
		Code:      code,
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()

	// 2. Build SVG
	svg := s.renderSVG(code)
	dataURL := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
	return id, dataURL
}

func (s *CaptchaStore) renderSVG(code string) string {
	width := 130
	height := 42

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height))
	// Background
	sb.WriteString(fmt.Sprintf(`<rect width="%d" height="%d" rx="6" fill="#f8fafc" stroke="#cbd5e1" stroke-width="1"/>`, width, height))

	// Interference lines (3 bezier curves)
	for i := 0; i < 4; i++ {
		x1 := randInt(20)
		y1 := randInt(height)
		qx := randInt(width)
		qy := randInt(height)
		x2 := width - randInt(20)
		y2 := randInt(height)
		strokeColor := noiseColors[randInt(len(noiseColors))]
		strokeWidth := 1 + randInt(2)
		sb.WriteString(fmt.Sprintf(`<path d="M %d %d Q %d %d %d %d" stroke="%s" stroke-width="%d" fill="none" opacity="0.65"/>`, x1, y1, qx, qy, x2, y2, strokeColor, strokeWidth))
	}

	// Interference dots (25 dots)
	for i := 0; i < 25; i++ {
		cx := randInt(width)
		cy := randInt(height)
		r := 1 + randInt(2)
		color := noiseColors[randInt(len(noiseColors))]
		sb.WriteString(fmt.Sprintf(`<circle cx="%d" cy="%d" r="%d" fill="%s" opacity="0.5"/>`, cx, cy, r, color))
	}

	// Characters
	for i, ch := range code {
		x := 15 + i*26 + randInt(6) - 3
		y := 28 + randInt(6) - 3
		angle := randInt(41) - 20 // -20 to +20 degrees
		color := captchaColors[randInt(len(captchaColors))]
		fontSize := 24 + randInt(4) // 24 to 27
		sb.WriteString(fmt.Sprintf(`<text x="%d" y="%d" font-family="'Segoe UI', Roboto, Helvetica, Arial, sans-serif" font-size="%d" font-weight="bold" fill="%s" transform="rotate(%d, %d, %d)" style="user-select:none;">%c</text>`,
			x, y, fontSize, color, angle, x+8, y-10, ch))
	}

	sb.WriteString(`</svg>`)
	return sb.String()
}

// Verify validates the captcha ID and code. It is single-use and case-insensitive.
func (s *CaptchaStore) Verify(id, code string) bool {
	if s == nil {
		return true
	}
	id = strings.TrimSpace(id)
	code = strings.TrimSpace(code)
	if id == "" || code == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.items[id]
	if !ok {
		return false
	}
	// Delete on first check to prevent brute-forcing with the same captcha ID
	delete(s.items, id)

	if time.Now().After(entry.ExpiresAt) {
		return false
	}

	return strings.EqualFold(entry.Code, code)
}

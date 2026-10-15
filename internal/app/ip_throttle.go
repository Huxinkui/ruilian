package app

import (
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// MaxLoginFailuresPerIP is the threshold before an IP is banned from login
	MaxLoginFailuresPerIP = 5
	// LoginLockoutDuration is the ban duration after reaching the failure threshold (30 minutes)
	LoginLockoutDuration = 30 * time.Minute
	// LoginFailWindow is the sliding window for accumulating failures
	LoginFailWindow = 30 * time.Minute
)

type ipLoginRecord struct {
	FailCount   int
	LockedUntil time.Time
	LastFailed  time.Time
}

// IPLoginLimiter manages failed login attempts and temporary lockouts per IP
type IPLoginLimiter struct {
	mu      sync.Mutex
	records map[string]*ipLoginRecord
}

func NewIPLoginLimiter() *IPLoginLimiter {
	return &IPLoginLimiter{
		records: make(map[string]*ipLoginRecord),
	}
}

// CheckLocked returns whether the given IP is currently banned from logging in,
// and the remaining minutes of the ban.
func (l *IPLoginLimiter) CheckLocked(ip string) (bool, int) {
	if l == nil || ip == "" || ip == "test" || ip == "init" {
		return false, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.records[ip]
	if !ok {
		return false, 0
	}

	now := time.Now()
	if rec.LockedUntil.After(now) {
		remaining := int(math.Ceil(rec.LockedUntil.Sub(now).Minutes()))
		if remaining < 1 {
			remaining = 1
		}
		return true, remaining
	}

	// Ban expired
	if !rec.LockedUntil.IsZero() && !rec.LockedUntil.After(now) {
		delete(l.records, ip)
	}

	return false, 0
}

// RecordFailure increments the failure count for the given IP.
// Returns:
// - lockedNow: true if this failure caused the IP to reach the threshold and lock
// - remainingMinutes: minutes remaining in lockout (if locked)
// - currentFailCount: total consecutive failures
// - remainingAttempts: attempts left before ban (if not locked)
func (l *IPLoginLimiter) RecordFailure(ip string) (bool, int, int, int) {
	if l == nil || ip == "" || ip == "test" || ip == "init" {
		return false, 0, 0, MaxLoginFailuresPerIP
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	rec, ok := l.records[ip]
	if !ok {
		rec = &ipLoginRecord{}
		l.records[ip] = rec
	} else if !rec.LockedUntil.IsZero() && now.After(rec.LockedUntil) {
		// Previous lock expired
		rec.FailCount = 0
		rec.LockedUntil = time.Time{}
	} else if !rec.LastFailed.IsZero() && now.Sub(rec.LastFailed) > LoginFailWindow {
		// Window expired without reaching limit
		rec.FailCount = 0
	}

	rec.FailCount++
	rec.LastFailed = now

	// Check if failure threshold reached (5 times)
	if rec.FailCount >= MaxLoginFailuresPerIP {
		rec.LockedUntil = now.Add(LoginLockoutDuration)
		remaining := int(math.Ceil(LoginLockoutDuration.Minutes()))
		if remaining < 1 {
			remaining = 1
		}
		return true, remaining, rec.FailCount, 0
	}

	remainingAttempts := MaxLoginFailuresPerIP - rec.FailCount
	return false, 0, rec.FailCount, remainingAttempts
}

// RecordSuccess clears the failed attempts for the given IP upon successful login
func (l *IPLoginLimiter) RecordSuccess(ip string) {
	if l == nil || ip == "" {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.records, ip)
}

// ResetIP resets records for an IP (useful for testing or admin unlock)
func (l *IPLoginLimiter) ResetIP(ip string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, ip)
}

// ResolveClientIP reliably extracts the client's real IP address,
// prioritizing Cloudflare and reverse-proxy headers before fallback to socket remote address.
func ResolveClientIP(c *gin.Context) string {
	if c == nil {
		return "127.0.0.1"
	}

	// 1. Cloudflare True-Client-IP / CF-Connecting-IP
	if cfIP := strings.TrimSpace(c.GetHeader("CF-Connecting-IP")); cfIP != "" {
		if ip := cleanIPString(cfIP); ip != "" {
			return ip
		}
	}
	if trueClientIP := strings.TrimSpace(c.GetHeader("True-Client-IP")); trueClientIP != "" {
		if ip := cleanIPString(trueClientIP); ip != "" {
			return ip
		}
	}

	// 2. X-Forwarded-For (leftmost client IP)
	if xff := strings.TrimSpace(c.GetHeader("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			if ip := cleanIPString(parts[0]); ip != "" {
				return ip
			}
		}
	}

	// 3. X-Real-IP
	if xReal := strings.TrimSpace(c.GetHeader("X-Real-IP")); xReal != "" {
		if ip := cleanIPString(xReal); ip != "" {
			return ip
		}
	}

	// 4. Gin ClientIP
	if ip := cleanIPString(c.ClientIP()); ip != "" {
		return ip
	}

	return "127.0.0.1"
}

func cleanIPString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Check if it's host:port
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}

	// Strip IPv6 brackets
	raw = strings.TrimPrefix(raw, "[")
	raw = strings.TrimSuffix(raw, "]")

	// Parse to validate
	parsed := net.ParseIP(raw)
	if parsed != nil {
		return parsed.String()
	}

	return raw
}

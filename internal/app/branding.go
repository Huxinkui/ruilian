package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode"
	"unicode/utf8"

	"ruilian/internal/branding"
)

// Branding is intentionally public: the login screen must display it too.
func (a *App) Branding() branding.Identity {
	identity := branding.Default
	rows, err := a.DB.Query("SELECT key,value FROM settings WHERE key IN ('app_name','brand_short','brand_logo')")
	if err != nil {
		return identity
	}
	defer rows.Close()
	for rows.Next() {
		var key, raw, value string
		if rows.Scan(&key, &raw) != nil || json.Unmarshal([]byte(raw), &value) != nil {
			continue
		}
		switch key {
		case "app_name":
			identity.AppName = value
		case "brand_short":
			identity.BrandShort = value
		case "brand_logo":
			identity.BrandLogo = value
		}
	}
	return identity
}

func validateBrandText(value any, maximum int) (string, error) {
	text, ok := value.(string)
	text = strings.TrimSpace(text)
	if !ok || !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > maximum {
		return "", fail("软件名称须为 1～64 个字符，品牌简称须为 1～12 个字符")
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' {
			return "", fail("名称不能包含控制字符或换行")
		}
	}
	return text, nil
}

func validateBrandLogo(value any) error {
	logo, ok := value.(string)
	if !ok {
		return fail("Logo 格式无效")
	}
	if logo == "" {
		return nil
	}
	const maximumBytes = 512 * 1024
	if len(logo) > base64.StdEncoding.EncodedLen(maximumBytes)+32 {
		return fail("Logo 文件不能超过 512 KB")
	}
	format, encoded := "", ""
	for prefix, name := range map[string]string{"data:image/png;base64,": "png", "data:image/jpeg;base64,": "jpeg"} {
		if strings.HasPrefix(logo, prefix) {
			format, encoded = name, strings.TrimPrefix(logo, prefix)
			break
		}
	}
	if format == "" {
		return fail("Logo 仅支持本地 PNG 或 JPEG 图片")
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) == 0 || len(data) > maximumBytes {
		return fail("Logo 图片编码无效或超过 512 KB")
	}
	config, detected, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || detected != format || config.Width < 16 || config.Height < 16 || config.Width > 1024 || config.Height > 1024 {
		return fail("Logo 须为有效图片，宽高均为 16～1024 像素")
	}
	if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
		return fail("Logo 图片已损坏，请重新选择")
	}
	return nil
}

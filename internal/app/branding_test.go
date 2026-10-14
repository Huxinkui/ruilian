package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"ruilian/internal/branding"
)

func logoData(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.NRGBA{R: 10, G: 100, B: 160, A: 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes())
}

func TestBrandingPersistsAcrossRestartAndIsPublicAtLogin(t *testing.T) {
	f := newFixture(t)
	logo := logoData(t, 64, 64)
	want := branding.Identity{AppName: "自定义经营管理", BrandShort: "企业管理", BrandLogo: logo}
	installation := f.a.License.Status(false).InstallationID
	if err := f.a.SaveSettings(f.u, "test", M{"app_name": "  " + want.AppName + "  ", "brand_short": want.BrandShort, "brand_logo": logo}); err != nil {
		t.Fatal(err)
	}
	directory := f.a.DataDir
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory, f.public, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := reopened.Branding(); got != want {
		t.Fatal("branding was not preserved across restart")
	}
	if status := reopened.License.Status(false); status.InstallationID != installation || status.Product != want.AppName || !status.Writable {
		t.Fatal("display branding must not alter license identity or validity")
	}
	router := reopened.Router(fstest.MapFS{"index.html": {Data: []byte("test")}})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/status", nil))
	var body struct {
		OK   bool `json:"ok"`
		Data struct {
			Branding branding.Identity `json:"branding"`
		} `json:"data"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || !body.OK || body.Data.Branding != want {
		t.Fatal("login page did not receive public branding")
	}
	if err = reopened.SaveSettings(f.u, "test", M{"brand_logo": ""}); err != nil || reopened.Branding().BrandLogo != "" {
		t.Fatal("clearing a logo must restore the configured default")
	}
}

func TestBrandingRejectsMalformedAndOversizedImagesAtomically(t *testing.T) {
	f := newFixture(t)
	valid := logoData(t, 32, 32)
	for _, value := range []any{
		123, "https://example.org/logo.png", "data:image/svg+xml;base64,PHN2Zz4=",
		"data:image/png;base64,invalid", strings.Replace(valid, "image/png", "image/jpeg", 1),
		logoData(t, 1025, 16), logoData(t, 8, 8), valid[:len(valid)-12],
		"data:image/png;base64," + strings.Repeat("A", 700000),
	} {
		if err := f.a.SaveSettings(f.u, "test", M{"app_name": "不应保存", "brand_logo": value}); err == nil {
			t.Fatal("malformed logo accepted")
		}
		if got := f.a.Branding(); got.AppName != branding.Default.AppName || got.BrandLogo != "" {
			t.Fatal("failed logo validation partially changed settings")
		}
	}
	for _, input := range []M{{"app_name": " \t"}, {"app_name": "不允许\n换行"}, {"brand_short": strings.Repeat("长", 13)}, {"app_name": false}, {"app_name": "隐藏\u202e文字"}} {
		if err := f.a.SaveSettings(f.u, "test", input); err == nil {
			t.Fatal("invalid brand name accepted")
		}
	}
}

func TestBrandingMigrationAddsDefaultsWithoutChangingExistingData(t *testing.T) {
	f := newFixture(t)
	if _, err := f.a.DB.Exec("DELETE FROM settings WHERE key IN ('app_name','brand_short','brand_logo')"); err != nil {
		t.Fatal(err)
	}
	if err := f.a.migrate(); err != nil {
		t.Fatal(err)
	}
	if got := f.a.Branding(); got != branding.Default {
		t.Fatalf("default brand migration failed: %s", got.AppName)
	}
	if settingString(f.a.DB, "company_name") != "事务验收企业" {
		t.Fatal("migration overwrote company settings")
	}
	if _, _, err := f.a.Login("admin", "Test-ERP-2026", "test"); err != nil {
		t.Fatal("migration changed the existing password")
	}
}

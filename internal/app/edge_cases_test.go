package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ruilian/internal/licensecode"
)

func TestFractionalCostsAndRepeatedPartialReturnConservation(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "7", "0.1429"))
	f.assertStock(f.p, f.warehouse, 7*Scale, 100, 0)
	sale := f.post(f.input("sales_delivery", "7", "1.1111"))
	if sale.Total != 778 || sale.Cost != 100 {
		t.Fatal("sale rounding incorrect")
	}
	for i := 0; i < 7; i++ {
		f.post(f.conversion(sale, "sales_return", "1"))
	}
	f.assertStock(f.p, f.warehouse, 7*Scale, 100, 0)
	if f.openBalance("ar") != 0 {
		t.Fatal("partial return rounding left an outstanding receivable")
	}
	if e := CheckLedger(f.a.DB); e != nil {
		t.Fatal(e)
	}
	r, e := f.a.Report(f.u, "sales_returns", "month", today(), today())
	if e != nil {
		t.Fatal(e)
	}
	if len(r) != 1 || r[0]["amount"] != "-7.78" || r[0]["cost"] != "-1.00" {
		t.Fatalf("return report: %#v", r)
	}
}

func TestSerialCannotBeReturnedTwiceAgainstOriginalSale(t *testing.T) {
	f := newFixture(t)
	f.p = f.master("product", "追踪商品", M{"unit": "台", "serial_tracking": true, "sales_price": "15"})
	in := f.input("purchase_receipt", "2", "10")
	in.Items[0].Serials = []string{"SN-A", "SN-B"}
	f.post(in)
	saleIn := f.input("sales_delivery", "2", "15")
	saleIn.Items[0].Serials = []string{"SN-A", "SN-B"}
	original := f.post(saleIn)
	back := f.conversion(original, "sales_return", "1")
	back.Items[0].Serials = []string{"SN-A"}
	f.post(back)
	resale := f.input("sales_delivery", "1", "15")
	resale.Items[0].Serials = []string{"SN-A"}
	f.post(resale)
	backAgain := f.conversion(original, "sales_return", "1")
	backAgain.Items[0].Serials = []string{"SN-A"}
	e := f.a.write(func(tx *sql.Tx) error { _, e := f.a.saveDocument(tx, f.u, "test", 0, backAgain); return e })
	if e == nil {
		t.Fatal("same serial returned twice against original source")
	}
	if e = CheckLedger(f.a.DB); e != nil {
		t.Fatal(e)
	}
}

func TestOperatingReportReconcilesSalesCashAndStock(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "10", "10"))
	f.post(f.input("sales_delivery", "4", "15"))
	f.post(f.input("ar_receipt", "", "50"))
	r, e := f.a.Report(f.u, "operations", "month", today(), today())
	if e != nil {
		t.Fatal(e)
	}
	if len(r) != 1 {
		t.Fatal("missing operating report")
	}
	for k, want := range map[string]string{"sales": "60.00", "purchase": "100.00", "gross_profit": "20.00", "cash_received": "50.00", "collection_rate": "83.33%", "closing_inventory_value": "60.00", "inventory_turnover": "1.3333"} {
		if r[0][k] != want {
			t.Fatalf("operating %s = %v; want %s", k, r[0][k], want)
		}
	}
	f.post(f.input("inventory_check", "5", "10"))
	counts, e := f.a.Report(f.u, "inventory_count", "date", today(), today())
	if e != nil {
		t.Fatal(e)
	}
	if len(counts) != 1 || counts[0]["delta"] != "-1" || counts[0]["cost_delta"] != "-10.00" {
		t.Fatalf("stock count report: %#v", counts)
	}
}

func TestCorruptLicenseAnchorKeepsBusinessDataReadable(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "3", "10"))
	dir := f.a.DataDir
	if e := f.a.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, ".installation.key"), []byte("damaged"), 0600); e != nil {
		t.Fatal(e)
	}
	a, e := Open(dir, f.public, log.New(io.Discard, "", 0))
	if e != nil {
		t.Fatal("damaged license prevented database access:", e)
	}
	defer a.Close()
	s := a.License.Status(true)
	if s.Status != "abnormal" || s.Writable {
		t.Fatalf("bad anchor status: %#v", s)
	}
	records, e := a.Inventory(f.u, 0, "")
	if e != nil || len(records) != 1 || records[0]["quantity"] != "3" {
		t.Fatal("license anomaly damaged or hid inventory")
	}
	if _, e = a.MakeBackup(f.u, "test", "manual"); e != nil {
		t.Fatal("license anomaly blocked backup:", e)
	}
	p := licensecode.NewPayload("MIGRATED", s.InstallationID, "1.1.0", "1.9999.9999", []string{"sales"})
	code, _ := licensecode.Encode(p, f.private)
	if e = a.License.Activate(f.u, "test", code); e != nil {
		t.Fatal(e)
	}
	if a.License.Allows("sales") != nil || a.License.Allows("purchase") == nil {
		t.Fatal("feature-scoped authorization failed")
	}
}

func TestUnicodePathsAndLedgerIntegrityVerification(t *testing.T) {
	pub, _, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(t.TempDir(), "中文 数据目录")
	a, e := Open(dir, pub, log.New(io.Discard, "", 0))
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if e = a.Setup(M{"username": "admin", "password": "Chinese-Path-2026", "company_name": "路径验收"}, "test"); e != nil {
		t.Fatal(e)
	}
	u, _ := loadUser(a.DB, 1)
	backup, e := a.MakeBackup(u, "test", "manual")
	if e != nil {
		t.Fatal(e)
	}
	path, _, e := a.BackupPath(backup.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = VerifyBackup(path); e != nil {
		t.Fatal(e)
	}
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "2", "10"))
	if e = CheckLedger(f.a.DB); e != nil {
		t.Fatal(e)
	}
	if _, e = f.a.DB.Exec("UPDATE inventory SET quantity=quantity+10000"); e != nil {
		t.Fatal(e)
	}
	if e = CheckLedger(f.a.DB); e == nil {
		t.Fatal("inventory/ledger divergence not detected")
	}
	b, e := f.a.MakeBackup(f.u, "test", "manual")
	if e == nil || b.Integrity != "failed" {
		t.Fatal("inconsistent backup marked valid")
	}
}

func TestMonotonicClockComparisonAndLicenseEvents(t *testing.T) {
	f := newFixture(t)
	f.a.License.mu.Lock()
	f.a.License.startWall = time.Now().Add(-time.Hour).Round(0)
	f.a.License.startMono = time.Now().Add(-2 * time.Hour)
	f.a.License.mu.Unlock()
	status := f.a.License.Status(true)
	if status.Status != "abnormal" {
		t.Fatal("monotonic/wall-clock discrepancy was not detected")
	}
	var events int
	if e := f.a.DB.QueryRow("SELECT COUNT(*) FROM license_event_log WHERE event='abnormal'").Scan(&events); e != nil {
		t.Fatal(e)
	}
	if events != 1 {
		t.Fatalf("expected one anomaly event, got %d", events)
	}
	f.a.License.Status(true)
	_ = f.a.DB.QueryRow("SELECT COUNT(*) FROM license_event_log WHERE event='abnormal'").Scan(&events)
	if events != 1 {
		t.Fatal("unchanged license state repeatedly logged")
	}
}

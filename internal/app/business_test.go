package app

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"ruilian/internal/licensecode"
)

type fixture struct {
	t                                *testing.T
	a                                *App
	u                                User
	p, customer, supplier, warehouse int64
	private                          ed25519.PrivateKey
	public                           ed25519.PublicKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	a, e := Open(t.TempDir(), pub, log.New(io.Discard, "", 0))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = a.Close() })
	if e = a.Setup(M{"username": "admin", "password": "Test-ERP-2026", "company_name": "事务验收企业"}, "test"); e != nil {
		t.Fatal(e)
	}
	u, e := loadUser(a.DB, 1)
	if e != nil {
		t.Fatal(e)
	}
	f := &fixture{t: t, a: a, u: u, private: priv, public: pub}
	_ = a.DB.QueryRow("SELECT id FROM base_records WHERE kind='warehouse'").Scan(&f.warehouse)
	f.p = f.master("product", "测试商品", M{"unit": "件", "purchase_price": "10", "sales_price": "15", "retail_price": "18", "min_price": "0"})
	f.customer = f.master("customer", "测试客户", M{"term_days": 30})
	f.supplier = f.master("supplier", "测试供应商", M{"term_days": 30})
	return f
}
func (f *fixture) master(kind, name string, data M) int64 {
	f.t.Helper()
	m, e := f.a.SaveMaster(f.u, "test", 0, Master{Kind: kind, Name: name, Data: data, Active: true})
	if e != nil {
		f.t.Fatal(e)
	}
	return m.ID
}
func (f *fixture) input(kind, q, p string) DocInput {
	party := int64(0)
	k, _ := kindOf(kind)
	if k.Party == "customer" {
		party = f.customer
	}
	if k.Party == "supplier" {
		party = f.supplier
	}
	d := DocInput{Kind: kind, PartnerID: party, WarehouseID: f.warehouse, Date: today(), Data: M{}, Items: []ItemInput{}}
	if k.Items {
		d.Items = []ItemInput{{ProductID: f.p, Quantity: q, Price: p}}
	} else {
		d.Amount = p
	}
	return d
}
func (f *fixture) save(input DocInput) Doc {
	f.t.Helper()
	var d Doc
	e := f.a.write(func(tx *sql.Tx) error { var e error; d, e = f.a.saveDocument(tx, f.u, "test", 0, input); return e })
	if e != nil {
		f.t.Fatal(e)
	}
	return d
}
func (f *fixture) action(id int64, action string) (Doc, error) {
	var d Doc
	e := f.a.write(func(tx *sql.Tx) error {
		var er error
		d, er = f.a.actDocument(tx, f.u, "test", id, action, "测试验证")
		return er
	})
	return d, e
}
func (f *fixture) post(input DocInput) Doc {
	f.t.Helper()
	d := f.save(input)
	if _, e := f.action(d.ID, "submit"); e != nil {
		f.t.Fatal(e)
	}
	d, e := f.action(d.ID, "approve")
	if e != nil {
		f.t.Fatal(e)
	}
	return d
}
func (f *fixture) stock(product, warehouse int64) (int64, int64, int64) {
	f.t.Helper()
	var q, v, r int64
	if e := f.a.DB.QueryRow("SELECT COALESCE(SUM(quantity),0),COALESCE(SUM(value),0),COALESCE(SUM(reserved),0) FROM inventory WHERE product_id=? AND warehouse_id=?", product, warehouse).Scan(&q, &v, &r); e != nil {
		f.t.Fatal(e)
	}
	return q, v, r
}
func (f *fixture) assertStock(product, warehouse, q, v, r int64) {
	f.t.Helper()
	gotQ, gotV, gotR := f.stock(product, warehouse)
	if gotQ != q || gotV != v || gotR != r {
		f.t.Fatalf("stock: quantity=%d value=%d reserved=%d; want %d %d %d", gotQ, gotV, gotR, q, v, r)
	}
}
func (f *fixture) openBalance(side string) int64 {
	var n int64
	if e := f.a.DB.QueryRow("SELECT COALESCE(SUM(amount-settled),0) FROM bills WHERE side=? AND state='active'", side).Scan(&n); e != nil {
		f.t.Fatal(e)
	}
	return n
}
func (f *fixture) conversion(source Doc, target, q string) DocInput {
	f.t.Helper()
	in, e := f.a.Conversion(f.u, source.ID, target)
	if e != nil {
		f.t.Fatal(e)
	}
	if q != "" {
		in.Items[0].Quantity = q
	}
	return in
}

func TestPurchaseSalesPaymentsReturnsAndReports(t *testing.T) {
	f := newFixture(t)
	purchase := f.post(f.input("purchase_receipt", "100", "10"))
	sale := f.post(f.input("sales_delivery", "20", "15"))
	f.assertStock(f.p, f.warehouse, 80*Scale, 80000, 0)
	if f.openBalance("ap") != 100000 || f.openBalance("ar") != 30000 {
		t.Fatal("incorrect generated payable/receivable")
	}
	f.post(f.input("ar_receipt", "", "100"))
	f.post(f.input("ap_payment", "", "500"))
	if f.openBalance("ar") != 20000 || f.openBalance("ap") != 50000 {
		t.Fatal("partial payment allocation incorrect")
	}
	f.post(f.conversion(sale, "sales_return", "5"))
	f.assertStock(f.p, f.warehouse, 85*Scale, 85000, 0)
	if f.openBalance("ar") != 12500 {
		t.Fatalf("return offset balance=%d", f.openBalance("ar"))
	}
	invalid := f.conversion(sale, "sales_return", "16")
	e := f.a.write(func(tx *sql.Tx) error { _, e := f.a.saveDocument(tx, f.u, "test", 0, invalid); return e })
	if e == nil {
		t.Fatal("over-return accepted")
	}
	f.post(f.conversion(purchase, "purchase_return", "10"))
	f.assertStock(f.p, f.warehouse, 75*Scale, 75000, 0)
	if f.openBalance("ap") != 40000 {
		t.Fatal("purchase return offset failed")
	}
	report, e := f.a.Report(f.u, "sales", "product", today(), today())
	if e != nil {
		t.Fatal(e)
	}
	if len(report) != 1 || report[0]["amount"] != "225.00" || report[0]["profit"] != "75.00" {
		t.Fatalf("report: %#v", report)
	}
	statement, e := f.a.Statement(f.u, "ar", f.customer, today(), today())
	if e != nil {
		t.Fatal(e)
	}
	if statement["closing"] != "125.00" {
		t.Fatalf("statement: %#v", statement)
	}
	if _, e = f.a.Dashboard(f.u); e != nil {
		t.Fatal(e)
	}
	if _, e = f.a.Warnings(f.u); e != nil {
		t.Fatal(e)
	}
}

func TestFailedApprovalRollsBackEveryEffect(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "10", "10"))
	p2 := f.master("product", "缺货商品", M{"unit": "件", "sales_price": "20"})
	input := f.input("sales_delivery", "4", "15")
	input.Items = append(input.Items, ItemInput{ProductID: p2, Quantity: "1", Price: "20"})
	d := f.save(input)
	_, _ = f.action(d.ID, "submit")
	if _, e := f.action(d.ID, "approve"); e == nil {
		t.Fatal("insufficient second line approved")
	}
	f.assertStock(f.p, f.warehouse, 10*Scale, 10000, 0)
	var movements, bills int
	_ = f.a.DB.QueryRow("SELECT COUNT(*) FROM inventory_transactions WHERE document_id=?", d.ID).Scan(&movements)
	_ = f.a.DB.QueryRow("SELECT COUNT(*) FROM bills WHERE document_id=?", d.ID).Scan(&bills)
	if movements != 0 || bills != 0 {
		t.Fatal("partial effects remained after rollback")
	}
	saved, _ := loadDoc(f.a.DB, d.ID)
	if saved.Status != "submitted" {
		t.Fatal("failed document status changed")
	}
}

func TestConcurrentApprovalAndIdempotency(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "1", "10"))
	docs := []Doc{f.save(f.input("sales_delivery", "1", "15")), f.save(f.input("sales_delivery", "1", "15"))}
	for _, d := range docs {
		_, _ = f.action(d.ID, "submit")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, d := range docs {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); _, e := f.action(id, "approve"); results <- e }(d.ID)
	}
	wg.Wait()
	close(results)
	success, failed := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else {
			failed++
		}
	}
	if success != 1 || failed != 1 {
		t.Fatalf("success=%d failures=%d", success, failed)
	}
	f.assertStock(f.p, f.warehouse, 0, 0, 0)
	input := f.input("purchase_receipt", "2", "10")
	mutation := NewMutation(uid(), "POST/documents", []byte(js(input)))
	calls := 0
	fn := func(tx *sql.Tx) (any, error) { calls++; return f.a.saveDocument(tx, f.u, "test", 0, input) }
	first, e := f.a.mutate(f.u, mutation, fn)
	if e != nil {
		t.Fatal(e)
	}
	second, e := f.a.mutate(f.u, mutation, fn)
	if e != nil {
		t.Fatal(e)
	}
	if calls != 1 || js(obj(js(first))) != js(obj(js(second))) {
		t.Fatal("idempotent create duplicated or returned different result")
	}
	mutation.Hash = "different"
	if _, e = f.a.mutate(f.u, mutation, fn); e == nil {
		t.Fatal("reused key with a different payload accepted")
	}
}

func TestOrderReservationPartialFulfillmentAndClose(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "10", "10"))
	order := f.post(f.input("sales_order", "10", "15"))
	f.assertStock(f.p, f.warehouse, 10*Scale, 10000, 10*Scale)
	other := f.save(f.input("sales_delivery", "1", "15"))
	_, _ = f.action(other.ID, "submit")
	if _, e := f.action(other.ID, "approve"); e == nil {
		t.Fatal("reserved stock was sold by an unrelated document")
	}
	delivery := f.post(f.conversion(order, "sales_delivery", "4"))
	f.assertStock(f.p, f.warehouse, 6*Scale, 6000, 6*Scale)
	order, _ = loadDoc(f.a.DB, order.ID)
	if order.Status != "in_progress" {
		t.Fatal("partial order status incorrect")
	}
	if _, e := f.action(order.ID, "close"); e != nil {
		t.Fatal(e)
	}
	f.assertStock(f.p, f.warehouse, 6*Scale, 6000, 0)
	if _, e := f.action(delivery.ID, "reverse"); e != nil {
		t.Fatal(e)
	}
	f.assertStock(f.p, f.warehouse, 10*Scale, 10000, 0)
	order, _ = loadDoc(f.a.DB, order.ID)
	if order.Status != "closed" {
		t.Fatal("closed order unexpectedly reopened")
	}
}

func TestSerialBatchTransferConsignmentAndSettlement(t *testing.T) {
	f := newFixture(t)
	tracked := f.master("product", "序列号商品", M{"unit": "台", "purchase_price": "10", "sales_price": "15", "batch_tracking": true, "serial_tracking": true})
	f.p = tracked
	in := f.input("purchase_receipt", "3", "10")
	in.Items[0].Batch = "B-001"
	in.Items[0].Serials = []string{"S1", "S2", "S3"}
	f.post(in)
	second := f.master("warehouse", "分仓", M{})
	transfer := f.input("transfer", "2", "0")
	transfer.TargetWarehouseID = second
	transfer.Items[0].Batch = "B-001"
	transfer.Items[0].Serials = []string{"S2", "S3"}
	f.post(transfer)
	f.assertStock(tracked, second, 2*Scale, 2000, 0)
	commission := f.master("warehouse", "客户委托仓", M{"consignment": true})
	ship := f.input("consignment_ship", "2", "15")
	ship.WarehouseID = second
	ship.TargetWarehouseID = commission
	ship.Items[0].Batch = "B-001"
	ship.Items[0].Serials = []string{"S2", "S3"}
	shipment := f.post(ship)
	if f.openBalance("ar") != 0 {
		t.Fatal("consignment shipment incorrectly created revenue")
	}
	settle := f.conversion(shipment, "consignment_settlement", "1")
	settle.Items[0].Serials = []string{"S2"}
	f.post(settle)
	if f.openBalance("ar") != 1500 {
		t.Fatal("consignment settlement failed")
	}
	back := f.conversion(shipment, "consignment_return", "1")
	back.Items[0].Serials = []string{"S3"}
	f.post(back)
	f.assertStock(tracked, commission, 0, 0, 0)
	f.assertStock(tracked, second, Scale, 1000, 0)
	duplicate := f.save(in)
	_, _ = f.action(duplicate.ID, "submit")
	if _, e := f.action(duplicate.ID, "approve"); e == nil {
		t.Fatal("duplicate serial accepted")
	}
	var state string
	_ = f.a.DB.QueryRow("SELECT state FROM inventory_serials WHERE product_id=? AND serial='S2'", tracked).Scan(&state)
	if state != "out" {
		t.Fatal("settled serial should be out")
	}
}

func TestAssemblySplitAndStockCount(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "20", "2"))
	p2 := f.master("product", "组件B", M{"unit": "件", "purchase_price": "3"})
	assembled := f.master("product", "成品", M{"unit": "套"})
	in := f.input("purchase_receipt", "10", "3")
	in.Items[0].ProductID = p2
	f.post(in)
	assembly := f.input("assembly", "4", "2")
	assembly.Items[0].Flow = "out"
	assembly.Items = append(assembly.Items, ItemInput{ProductID: p2, Quantity: "2", Price: "3", Flow: "out"}, ItemInput{ProductID: assembled, Quantity: "2", Price: "1", Flow: "in"})
	f.post(assembly)
	f.assertStock(assembled, f.warehouse, 2*Scale, 1400, 0)
	split := f.input("split", "1", "7")
	split.Items[0].ProductID = assembled
	split.Items[0].Flow = "out"
	split.Items = append(split.Items, ItemInput{ProductID: f.p, Quantity: "2", Price: "2", Flow: "in"}, ItemInput{ProductID: p2, Quantity: "1", Price: "3", Flow: "in"})
	f.post(split)
	f.assertStock(assembled, f.warehouse, Scale, 700, 0)
	f.assertStock(f.p, f.warehouse, 18*Scale, 3600, 0)
	f.assertStock(p2, f.warehouse, 9*Scale, 2700, 0)
	count := f.save(f.input("inventory_check", "16", "2"))
	_, _ = f.action(count.ID, "submit")
	f.post(f.input("purchase_receipt", "1", "2"))
	if _, e := f.action(count.ID, "approve"); e == nil {
		t.Fatal("stale stock count accepted")
	}
	f.assertStock(f.p, f.warehouse, 19*Scale, 3800, 0)
	count2 := f.post(f.input("inventory_check", "16", "2"))
	f.assertStock(f.p, f.warehouse, 16*Scale, 3200, 0)
	if _, e := f.action(count2.ID, "reverse"); e != nil {
		t.Fatal(e)
	}
	f.assertStock(f.p, f.warehouse, 19*Scale, 3800, 0)
}

func TestAdvancesRefundsAndWriteoffReversal(t *testing.T) {
	f := newFixture(t)
	advance := f.post(f.input("ar_receipt", "", "50"))
	refund := f.post(f.input("ar_refund", "", "20"))
	var sid int64
	_ = f.a.DB.QueryRow("SELECT id FROM settlements WHERE document_id=?", advance.ID).Scan(&sid)
	s, e := loadSettlement(f.a.DB, sid)
	if e != nil {
		t.Fatal(e)
	}
	if s.Amount-s.Allocated != 3000 {
		t.Fatal("advance refund not deducted")
	}
	f.post(f.input("purchase_receipt", "10", "10"))
	sale := f.post(f.input("sales_delivery", "10", "15"))
	var bid int64
	_ = f.a.DB.QueryRow("SELECT id FROM bills WHERE document_id=?", sale.ID).Scan(&bid)
	if e = f.a.write(func(tx *sql.Tx) error { return f.a.allocate(tx, f.u, sid, []Allocation{{bid, "30"}}) }); e != nil {
		t.Fatal(e)
	}
	if f.openBalance("ar") != 12000 {
		t.Fatal("advance allocation incorrect")
	}
	invalid := f.save(f.input("ar_refund", "", "1"))
	_, _ = f.action(invalid.ID, "submit")
	if _, e = f.action(invalid.ID, "approve"); e == nil {
		t.Fatal("refund beyond available credit accepted")
	}
	if _, e = f.action(refund.ID, "reverse"); e != nil {
		t.Fatal(e)
	}
	s, _ = loadSettlement(f.a.DB, sid)
	if s.Amount-s.Allocated != 2000 {
		t.Fatal("refund reversal did not restore available advance")
	}
	var wid int64
	_ = f.a.DB.QueryRow("SELECT id FROM writeoffs WHERE settlement_id=? AND reversed=0", sid).Scan(&wid)
	if e = f.a.write(func(tx *sql.Tx) error { return f.a.reverseWriteoff(tx, f.u, wid, "更正核销") }); e != nil {
		t.Fatal(e)
	}
	if f.openBalance("ar") != 15000 {
		t.Fatal("writeoff reversal incorrect")
	}
}

func TestExchangesAndCashRetail(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "10", "5"))
	p2 := f.master("product", "换出新商品", M{"unit": "件", "purchase_price": "8", "sales_price": "15"})
	p2in := f.input("purchase_receipt", "5", "8")
	p2in.Items[0].ProductID = p2
	f.post(p2in)
	sale := f.post(f.input("sales_delivery", "2", "10"))
	exchange := f.conversion(sale, "sales_exchange", "1")
	exchange.Items = append(exchange.Items, ItemInput{ProductID: p2, Quantity: "1", Price: "15", Flow: "out"})
	ex := f.post(exchange)
	if ex.Total != 500 || ex.Cost != 300 || f.openBalance("ar") != 2500 {
		t.Fatalf("exchange incorrect: %#v", ex)
	}
	f.assertStock(f.p, f.warehouse, 9*Scale, 4500, 0)
	f.assertStock(p2, f.warehouse, 4*Scale, 3200, 0)
	retail := f.input("retail_sale", "1", "18")
	retail.Data["cash_sale"] = true
	cash := f.post(retail)
	var receiptID int64
	if e := f.a.DB.QueryRow("SELECT id FROM documents WHERE source_id=? AND kind='ar_receipt'", cash.ID).Scan(&receiptID); e != nil {
		t.Fatal(e)
	}
	if f.openBalance("ar") != 2500 {
		t.Fatal("cash retail left an unpaid receivable")
	}
	if _, e := f.action(cash.ID, "reverse"); e == nil {
		t.Fatal("cash sale reversed before its receipt")
	}
	if _, e := f.action(receiptID, "reverse"); e != nil {
		t.Fatal(e)
	}
	if _, e := f.action(cash.ID, "reverse"); e != nil {
		t.Fatal(e)
	}
}

func TestPricingCreditAndMinimumPriceControls(t *testing.T) {
	f := newFixture(t)
	p, e := master(f.a.DB, f.p, "product", false)
	if e != nil {
		t.Fatal(e)
	}
	p.Data["min_price"] = "12"
	if _, e = f.a.SaveMaster(f.u, "test", p.ID, p); e != nil {
		t.Fatal(e)
	}
	c, _ := master(f.a.DB, f.customer, "customer", false)
	c.Data["level"] = "金牌"
	c.Data["credit_limit"] = "50"
	if _, e = f.a.SaveMaster(f.u, "test", c.ID, c); e != nil {
		t.Fatal(e)
	}
	for _, r := range []M{{"name": "阶梯", "kind": "quantity", "min_quantity": "10", "price": "13"}, {"name": "等级", "kind": "level", "customer_level": "金牌", "price": "14"}, {"name": "客户", "kind": "customer", "customer_id": f.customer, "price": "12.5"}} {
		r["product_id"] = f.p
		r["active"] = true
		if e = f.a.SavePriceRule(f.u, "test", 0, r); e != nil {
			t.Fatal(e)
		}
	}
	quote, e := quotePrice(f.a.DB, f.p, f.customer, 20*Scale, today(), "wholesale")
	if e != nil {
		t.Fatal(e)
	}
	if quote.Price != 125000 || quote.Source != "客户" {
		t.Fatalf("quote: %#v", quote)
	}
	f.post(f.input("purchase_receipt", "10", "10"))
	low := f.save(f.input("sales_delivery", "1", "11"))
	_, _ = f.action(low.ID, "submit")
	if _, e = f.action(low.ID, "approve"); e == nil {
		t.Fatal("minimum selling price ignored")
	}
	over := f.save(f.input("sales_delivery", "5", "12.5"))
	_, _ = f.action(over.ID, "submit")
	if _, e = f.action(over.ID, "approve"); e == nil {
		t.Fatal("credit limit ignored")
	}
	f.assertStock(f.p, f.warehouse, 10*Scale, 10000, 0)
}

func TestFixedPrecisionAndNegativeStockCostAdjustment(t *testing.T) {
	if _, e := fixed("1e2", 4); e == nil {
		t.Fatal("exponential decimal input accepted")
	}
	if _, e := fixed("1.00001", 4); e == nil {
		t.Fatal("excess precision accepted")
	}
	q, _ := fixed("0.3333", 4)
	p, _ := fixed("3.3333", 4)
	if amount(q, p) != 111 {
		t.Fatal("decimal multiplication incorrect")
	}
	f := newFixture(t)
	if e := f.a.SaveSettings(f.u, "test", M{"allow_negative_stock": true}); e != nil {
		t.Fatal(e)
	}
	f.post(f.input("sales_delivery", "5", "15"))
	f.assertStock(f.p, f.warehouse, -5*Scale, -5000, 0)
	f.post(f.input("purchase_receipt", "5", "12"))
	f.assertStock(f.p, f.warehouse, 0, 0, 0)
	dashboard, e := f.a.Dashboard(f.u)
	if e != nil {
		t.Fatal(e)
	}
	if dashboard["cost_adjustments"] != "10.00" || dashboard["gross_profit"] != "15.00" {
		t.Fatalf("negative stock cost variance: %#v", dashboard)
	}
}

func TestLicenseExpirySignatureTamperAndDataAccess(t *testing.T) {
	f := newFixture(t)
	f.a.License.mu.Lock()
	f.a.License.state.FirstRun = time.Now().Add(-11 * 24 * time.Hour).UTC().Format(time.RFC3339)
	if e := f.a.License.persist(); e != nil {
		t.Fatal(e)
	}
	f.a.License.mu.Unlock()
	s := f.a.License.Status(true)
	if s.Status != "expired" || s.Writable {
		t.Fatalf("trial expiry: %#v", s)
	}
	router := f.a.Router(fstest.MapFS{"index.html": {Data: []byte("test app")}})
	_, token := apiRequest(t, router, "POST", "/api/auth/login", M{"username": "admin", "password": "Test-ERP-2026"}, nil, "")
	result, _ := apiRequest(t, router, "POST", "/api/documents", f.input("purchase_receipt", "1", "10"), token, "")
	if result.Code != 402 {
		t.Fatalf("expired business write status %d: %s", result.Code, result.Body.String())
	}
	result, _ = apiRequest(t, router, "GET", "/api/export?type=inventory", nil, token, "")
	if result.Code != 200 {
		t.Fatal("expired data export blocked")
	}
	result, _ = apiRequest(t, router, "POST", "/api/system/backups", M{}, token, "")
	if result.Code != 200 {
		t.Fatalf("expired backup blocked: %s", result.Body.String())
	}
	p := licensecode.NewPayload("TEST-CUSTOMER", s.InstallationID, "1.1.0", "1.9999.9999", []string{"*"})
	code, e := licensecode.Encode(p, f.private)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.a.License.Activate(f.u, "test", code); e != nil {
		t.Fatal(e)
	}
	if f.a.License.Status(true).Status != "active" {
		t.Fatal("perpetual activation failed")
	}
	wrong := p
	wrong.InstallationID = uid()
	wrongCode, _ := licensecode.Encode(wrong, f.private)
	if e = f.a.License.Activate(f.u, "test", wrongCode); e == nil {
		t.Fatal("license from a different installation accepted")
	}
	_, _ = f.a.DB.Exec("UPDATE license_local SET payload=replace(payload,'TEST-CUSTOMER','TAMPERED')")
	if f.a.License.Status(true).Writable {
		t.Fatal("tampered license remained writable")
	}
	if e = f.a.License.Activate(f.u, "test", code); e != nil {
		t.Fatal(e)
	}
	f.a.License.mu.Lock()
	f.a.License.state.LastRun = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	_ = f.a.License.persist()
	f.a.License.mu.Unlock()
	if f.a.License.Status(true).Status != "abnormal" {
		t.Fatal("clock rollback not detected")
	}
}

func apiRequest(t *testing.T, router http.Handler, method, path string, body any, cookies []*http.Cookie, origin string) (*httptest.ResponseRecorder, []*http.Cookie) {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewBufferString(js(body))
	}
	request := httptest.NewRequest(method, path, r)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uid())
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	returned := response.Result().Cookies()
	if len(returned) == 0 {
		returned = cookies
	}
	return response, returned
}

func TestAPIRBACWarehouseScopeCSRFAndRedaction(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "5", "10"))
	other := f.master("warehouse", "不可见仓库", M{})
	hidden := f.input("purchase_receipt", "2", "10")
	hidden.WarehouseID = other
	privateDoc := f.post(hidden)
	if e := f.a.SaveRole(f.u, "test", 0, M{"name": "仓库受限", "permissions": []string{"base.view", "inventory.view", "purchase.view"}, "warehouse_ids": []int64{f.warehouse}, "data_scope": "all"}); e != nil {
		t.Fatal(e)
	}
	var roleID int64
	_ = f.a.DB.QueryRow("SELECT id FROM roles WHERE name='仓库受限'").Scan(&roleID)
	if e := f.a.SaveUser(f.u, "test", 0, M{"username": "warehouse", "name": "库管", "password": "Test-Warehouse-2026", "role_id": roleID, "active": true}); e != nil {
		t.Fatal(e)
	}
	router := f.a.Router(fstest.MapFS{"index.html": {Data: []byte("test app")}})
	response, cookies := apiRequest(t, router, "POST", "/api/auth/login", M{"username": "warehouse", "password": "Test-Warehouse-2026"}, nil, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response, _ = apiRequest(t, router, "GET", "/api/inventory/balances", nil, cookies, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if strings.Contains(response.Body.String(), "不可见仓库") || !strings.Contains(response.Body.String(), `"value":null`) {
		t.Fatalf("warehouse scope or redaction failed: %s", response.Body.String())
	}
	response, _ = apiRequest(t, router, "GET", "/api/documents/"+strconvID(privateDoc.ID), nil, cookies, "")
	if response.Code != 403 {
		t.Fatal("unauthorized warehouse document readable")
	}
	response, _ = apiRequest(t, router, "POST", "/api/masters/product", Master{Kind: "product", Name: "越权", Active: true}, cookies, "")
	if response.Code != 403 {
		t.Fatal("RBAC write denied incorrectly")
	}
	response, adminCookies := apiRequest(t, router, "POST", "/api/auth/login", M{"username": "admin", "password": "Test-ERP-2026"}, nil, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	response, _ = apiRequest(t, router, "POST", "/api/documents", f.input("purchase_receipt", "1", "10"), adminCookies, "https://evil.example")
	if response.Code != 403 {
		t.Fatal("cross-origin write accepted")
	}
	response, _ = apiRequest(t, router, "GET", "/api/export?type=masters&kind=product", nil, cookies, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sales_price") || strings.Contains(response.Body.String(), "purchase_price") {
		t.Fatal("CSV exposed hidden price columns")
	}
}
func strconvID(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestBackupVerificationRestoreAndTamperDetection(t *testing.T) {
	f := newFixture(t)
	f.post(f.input("purchase_receipt", "8", "10"))
	replica1, replica2 := t.TempDir(), t.TempDir()
	if e := f.a.SaveSettings(f.u, "test", M{"backup_destinations": []string{replica1, replica2}}); e != nil {
		t.Fatal(e)
	}
	backup, e := f.a.MakeBackup(f.u, "test", "manual")
	if e != nil {
		t.Fatal(e)
	}
	if backup.Integrity != "ok" || len(backup.Replicas) != 2 || backup.Status != "ok" {
		t.Fatalf("backup: %#v", backup)
	}
	path, _, e := f.a.BackupPath(backup.ID)
	if e != nil {
		t.Fatal(e)
	}
	for _, dir := range []string{replica1, replica2} {
		hash, _, e := VerifyBackup(filepath.Join(dir, backup.Filename))
		if e != nil || hash != backup.SHA256 {
			t.Fatal("replica verification failed")
		}
	}
	f.post(f.input("sales_delivery", "3", "15"))
	dir := f.a.DataDir
	if e = f.a.Close(); e != nil {
		t.Fatal(e)
	}
	preserved, e := RestoreDatabase(dir, path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(preserved, "ruilian.db")); e != nil {
		t.Fatal("pre-restore copy missing")
	}
	reopened, e := Open(dir, f.public, log.New(io.Discard, "", 0))
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	var q int64
	_ = reopened.DB.QueryRow("SELECT SUM(quantity) FROM inventory").Scan(&q)
	if q != 8*Scale {
		t.Fatalf("restore stock quantity %d", q)
	}
	if reopened.License.Status(true).Status != "trial" {
		t.Fatal("valid same-machine restore broke trial state")
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.sqlite")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	raw[0] = 'X'
	if e = os.WriteFile(corrupt, raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = VerifyBackup(corrupt); e == nil {
		t.Fatal("corrupt backup accepted")
	}
}

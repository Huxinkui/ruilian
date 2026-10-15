package app

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const Version = "1.1.0"
const Product = "RUILIAN_ERP"
const Scale int64 = 10000
const MaxValue int64 = 900000000000000

type M = map[string]any

type BusinessError struct {
	Code    string
	Message string
}

func (e *BusinessError) Error() string { return e.Message }
func fail(message string) error        { return &BusinessError{"VALIDATION", message} }
func denied(message string) error      { return &BusinessError{"FORBIDDEN", message} }

type Queryer interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func uid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func stamp() string   { return time.Now().UTC().Format(time.RFC3339) }
func today() string   { return time.Now().Format("2006-01-02") }
func js(v any) string { b, _ := json.Marshal(v); return string(b) }
func obj(s string) M  { m := M{}; _ = json.Unmarshal([]byte(s), &m); return m }
func str(m M, k string) string {
	if m[k] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(m[k]))
}
func number(m M, k string) int64 {
	s := str(m, k)
	n, _ := strconv.ParseInt(strings.Split(s, ".")[0], 10, 64)
	return n
}
func flag(m M, k string) bool { return m[k] == true || str(m, k) == "true" || str(m, k) == "1" }
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
func idOf(result sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
func dateValid(s string) bool {
	t, e := time.Parse("2006-01-02", s)
	return e == nil && t.Year() >= 2000 && t.Year() <= 2199
}

// Decimal input is parsed without floating point. Money is stored in cents;
// quantities and unit prices use 1/10000. Multiplication uses arbitrary precision.
func fixed(s string, digits int) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	negative := strings.HasPrefix(s, "-")
	if negative || strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fail("请输入有效数字")
	}
	frac := ""
	if len(parts) == 2 {
		frac = parts[1]
	}
	for len(frac) > digits && strings.HasSuffix(frac, "0") {
		frac = frac[:len(frac)-1]
	}
	if len(frac) > digits {
		return 0, fail(fmt.Sprintf("最多支持 %d 位小数", digits))
	}
	full := parts[0] + frac + strings.Repeat("0", digits-len(frac))
	for _, c := range full {
		if c < '0' || c > '9' {
			return 0, fail("请输入有效数字")
		}
	}
	n, e := strconv.ParseInt(full, 10, 64)
	if e != nil || n > MaxValue {
		return 0, fail("数字超出允许范围")
	}
	if negative {
		n = -n
	}
	return n, nil
}
func fmtFixed(n int64, d int) string {
	neg := ""
	if n < 0 {
		neg = "-"
		n = -n
	}
	pow := int64(1)
	for i := 0; i < d; i++ {
		pow *= 10
	}
	return fmt.Sprintf("%s%d.%0*d", neg, n/pow, d, n%pow)
}
func qty(n int64) string   { return strings.TrimRight(strings.TrimRight(fmtFixed(n, 4), "0"), ".") }
func money(n int64) string { return fmtFixed(n, 2) }
func mulDiv(a, b, div int64) int64 {
	if div == 0 {
		return 0
	}
	if div < 0 {
		div = -div
		a = -a
	}
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	neg := n.Sign() < 0
	n.Abs(n)
	n.Add(n, big.NewInt(div/2))
	n.Quo(n, big.NewInt(div))
	if neg {
		n.Neg(n)
	}
	if !n.IsInt64() || n.Cmp(big.NewInt(MaxValue)) > 0 || n.Cmp(big.NewInt(-MaxValue)) < 0 {
		return MaxValue + 1
	}
	return n.Int64()
}
func amount(q, p int64) int64 { return mulDiv(q, p, 1000000) }
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func bounded(n int64) error {
	if abs(n) > MaxValue {
		return fail("数量或金额超出允许范围")
	}
	return nil
}

type User struct {
	ID             int64    `json:"id"`
	Username       string   `json:"username"`
	Name           string   `json:"name"`
	RoleID         int64    `json:"role_id"`
	RoleName       string   `json:"role_name"`
	Permissions    []string `json:"permissions"`
	Warehouses     []int64  `json:"warehouse_ids"`
	DataScope      string   `json:"data_scope"`
	OrganizationID int64    `json:"organization_id"`
	IsMasterAdmin  bool     `json:"is_master_admin"`
}

func (u User) Can(p string) bool {
	for _, v := range u.Permissions {
		if v == "*" || v == p {
			return true
		}
	}
	return false
}
func (u User) Warehouse(id int64) bool {
	if id == 0 || len(u.Warehouses) == 0 {
		return true
	}
	for _, w := range u.Warehouses {
		if w == id {
			return true
		}
	}
	return false
}

type Master struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Data      M      `json:"data"`
	Active    bool   `json:"active"`
	UpdatedAt string `json:"updated_at"`
}

type DocKind struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Module string `json:"module"`
	Party  string `json:"party"`
	Items  bool   `json:"items"`
	Target bool   `json:"target"`
}

var Kinds = []DocKind{
	{"purchase_order", "采购订单", "purchase", "supplier", true, false},
	{"purchase_receipt", "采购入库", "purchase", "supplier", true, false},
	{"purchase_return", "采购退货", "purchase", "supplier", true, false},
	{"purchase_exchange", "采购换货", "purchase", "supplier", true, false},
	{"sales_order", "销售订单", "sales", "customer", true, false},
	{"sales_delivery", "销售出库", "sales", "customer", true, false},
	{"retail_sale", "零售销售", "sales", "customer", true, false},
	{"sales_return", "销售退货", "sales", "customer", true, false},
	{"sales_exchange", "销售换货", "sales", "customer", true, false},
	{"consignment_ship", "委托发货", "sales", "customer", true, true},
	{"consignment_settlement", "代销结算", "sales", "customer", true, false},
	{"consignment_return", "委托退回", "sales", "customer", true, true},
	{"transfer", "库存调拨", "inventory", "", true, true},
	{"inventory_check", "库存盘点", "inventory", "", true, false},
	{"assembly", "库存组装", "inventory", "", true, false},
	{"split", "库存拆分", "inventory", "", true, false},
	{"opening_inventory", "期初库存", "inventory", "", true, false},
	{"ar_receipt", "客户收款 / 预收", "finance", "customer", false, false},
	{"ap_payment", "供应商付款 / 预付", "finance", "supplier", false, false},
	{"ar_refund", "客户退款", "finance", "customer", false, false},
	{"ap_refund", "供应商退款", "finance", "supplier", false, false},
	{"opening_ar", "期初应收", "finance", "customer", false, false},
	{"opening_ap", "期初应付", "finance", "supplier", false, false},
}

func kindOf(key string) (DocKind, error) {
	if key == "reversal" {
		return DocKind{"reversal", "红冲凭证", "inventory", "", true, false}, nil
	}
	for _, k := range Kinds {
		if k.Key == key {
			return k, nil
		}
	}
	return DocKind{}, fail("不支持的单据类型")
}
func posted(status string) bool {
	return status == "approved" || status == "completed" || status == "in_progress" || status == "closed"
}
func isOrder(kind string) bool { return kind == "sales_order" || kind == "purchase_order" }
func isMoney(kind string) bool {
	return kind == "ar_receipt" || kind == "ap_payment" || kind == "ar_refund" || kind == "ap_refund"
}

type ItemInput struct {
	ProductID        int64    `json:"product_id"`
	LocationID       int64    `json:"location_id"`
	TargetLocationID int64    `json:"target_location_id"`
	SourceItemID     int64    `json:"source_item_id"`
	Batch            string   `json:"batch"`
	Serials          []string `json:"serials"`
	Quantity         string   `json:"quantity"`
	Price            string   `json:"price"`
	Flow             string   `json:"flow"`
	Note             string   `json:"note"`
}
type DocInput struct {
	Kind              string      `json:"kind"`
	PartnerID         int64       `json:"partner_id"`
	WarehouseID       int64       `json:"warehouse_id"`
	TargetWarehouseID int64       `json:"target_warehouse_id"`
	SourceID          int64       `json:"source_id"`
	Date              string      `json:"date"`
	DueDate           string      `json:"due_date"`
	Amount            string      `json:"amount"`
	Note              string      `json:"note"`
	Data              M           `json:"data"`
	Items             []ItemInput `json:"items"`
	Revision          int64       `json:"revision"`
}
type Doc struct {
	ID                  int64  `json:"id"`
	Number              string `json:"number"`
	Kind                string `json:"kind"`
	KindName            string `json:"kind_name"`
	Status              string `json:"status"`
	PartnerID           int64  `json:"partner_id"`
	PartnerName         string `json:"partner_name"`
	WarehouseID         int64  `json:"warehouse_id"`
	WarehouseName       string `json:"warehouse_name"`
	TargetWarehouseID   int64  `json:"target_warehouse_id"`
	TargetWarehouseName string `json:"target_warehouse_name"`
	SourceID            int64  `json:"source_id"`
	SourceNumber        string `json:"source_number"`
	Date                string `json:"date"`
	DueDate             string `json:"due_date"`
	Total               int64  `json:"-"`
	Cost                int64  `json:"-"`
	Amount              string `json:"amount"`
	CostAmount          string `json:"cost_amount"`
	Note                string `json:"note"`
	Data                M      `json:"data"`
	CreatedBy           int64  `json:"created_by"`
	Creator             string `json:"creator"`
	CreatedAt           string `json:"created_at"`
	ApprovedAt          string `json:"approved_at"`
	Revision            int64  `json:"revision"`
	Items               []Item `json:"items"`
}
type Item struct {
	ID               int64    `json:"id"`
	ProductID        int64    `json:"product_id"`
	ProductName      string   `json:"product_name"`
	ProductCode      string   `json:"product_code"`
	Unit             string   `json:"unit"`
	LocationID       int64    `json:"location_id"`
	TargetLocationID int64    `json:"target_location_id"`
	SourceItemID     int64    `json:"source_item_id"`
	Batch            string   `json:"batch"`
	Serials          []string `json:"serials"`
	Flow             string   `json:"flow"`
	Note             string   `json:"note"`
	Q                int64    `json:"-"`
	P                int64    `json:"-"`
	A                int64    `json:"-"`
	C                int64    `json:"-"`
	Book             int64    `json:"-"`
	Quantity         string   `json:"quantity"`
	Price            string   `json:"price"`
	Amount           string   `json:"amount"`
	CostAmount       string   `json:"cost_amount"`
	BookQuantity     string   `json:"book_quantity"`
	PriceSource      string   `json:"price_source"`
}

func (d *Doc) format() {
	d.Amount = money(d.Total)
	d.CostAmount = money(d.Cost)
	for i := range d.Items {
		d.Items[i].format()
	}
}
func (i *Item) format() {
	i.Quantity = qty(i.Q)
	i.Price = qty(i.P)
	i.Amount = money(i.A)
	i.CostAmount = money(i.C)
	i.BookQuantity = qty(i.Book)
}
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return &BusinessError{"NOT_FOUND", "记录不存在或已停用"}
	}
	return err
}

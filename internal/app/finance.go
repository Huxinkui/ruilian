package app

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Allocation struct {
	BillID int64  `json:"bill_id"`
	Amount string `json:"amount"`
}
type bill struct {
	ID, DocumentID, PartnerID, Amount, Settled int64
	Side, DueDate, State                       string
}
type settlement struct {
	ID, DocumentID, PartnerID, Amount, Allocated int64
	Side, State                                  string
}

func loadBill(q Queryer, id int64) (bill, error) {
	var b bill
	e := q.QueryRow("SELECT id,document_id,partner_id,side,amount,settled,due_date,state FROM bills WHERE id=?", id).Scan(&b.ID, &b.DocumentID, &b.PartnerID, &b.Side, &b.Amount, &b.Settled, &b.DueDate, &b.State)
	return b, notFound(e)
}
func loadSettlement(q Queryer, id int64) (settlement, error) {
	var s settlement
	e := q.QueryRow("SELECT id,document_id,partner_id,side,amount,allocated,state FROM settlements WHERE id=?", id).Scan(&s.ID, &s.DocumentID, &s.PartnerID, &s.Side, &s.Amount, &s.Allocated, &s.State)
	return s, notFound(e)
}
func financeScope(q Queryer, u User, docID int64) error {
	var owner, w, t int64
	e := q.QueryRow("SELECT created_by,COALESCE(warehouse_id,0),COALESCE(target_warehouse_id,0) FROM documents WHERE id=?", docID).Scan(&owner, &w, &t)
	if e != nil {
		return e
	}
	if !u.Warehouse(w) || !u.Warehouse(t) || u.DataScope == "own" && owner != u.ID {
		return denied("没有相关往来单据的数据权限")
	}
	return nil
}

func (a *App) checkCredit(q Queryer, d Doc) error {
	if !(d.Kind == "sales_order" || d.Kind == "sales_delivery" || d.Kind == "retail_sale" || d.Kind == "sales_exchange" || d.Kind == "consignment_settlement") {
		return nil
	}
	for _, it := range d.Items {
		if d.Kind == "sales_exchange" && it.Flow == "in" {
			continue
		}
		channel := "wholesale"
		if d.Kind == "retail_sale" {
			channel = "retail"
		}
		price, e := quotePrice(q, it.ProductID, d.PartnerID, it.Q, d.Date, channel)
		if e != nil {
			return e
		}
		if it.P < price.Minimum && settingString(q, "low_price_policy") == "block" {
			return fail(it.ProductName + " 低于最低售价 " + price.MinPrice + "，不能审核")
		}
	}
	if d.Total <= 0 || flag(d.Data, "cash_sale") {
		return nil
	}
	p, e := master(q, d.PartnerID, "customer", true)
	if e != nil {
		return e
	}
	limit, _ := fixed(str(p.Data, "credit_limit"), 2)
	var balance, advance int64
	if e = q.QueryRow("SELECT COALESCE(SUM(amount-settled),0) FROM bills WHERE side='ar' AND partner_id=? AND state='active'", d.PartnerID).Scan(&balance); e != nil {
		return e
	}
	if e = q.QueryRow("SELECT COALESCE(SUM(amount-allocated),0) FROM settlements WHERE side='ar' AND partner_id=? AND state='active'", d.PartnerID).Scan(&advance); e != nil {
		return e
	}
	if limit > 0 && balance-advance+d.Total > limit && settingString(q, "credit_policy") == "block" {
		return fail(fmt.Sprintf("客户信用额度不足：额度 %s，当前占用 %s，本单 %s", money(limit), money(balance-advance), money(d.Total)))
	}
	if settingString(q, "overdue_policy") == "block" {
		var n int
		if e = q.QueryRow("SELECT COUNT(*) FROM bills WHERE side='ar' AND partner_id=? AND state='active' AND amount-settled>0 AND due_date<?", d.PartnerID, today()).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fail("客户存在逾期应收，请收款或调整逾期控制策略")
		}
	}
	return nil
}

func (a *App) applyFinance(tx *sql.Tx, u User, d Doc) error {
	if isMoney(d.Kind) {
		return a.applySettlement(tx, u, d)
	}
	side := ""
	switch d.Kind {
	case "purchase_receipt", "purchase_return", "purchase_exchange", "opening_ap":
		side = "ap"
	case "sales_delivery", "retail_sale", "sales_return", "sales_exchange", "consignment_settlement", "opening_ar":
		side = "ar"
	}
	if side == "" || d.Total == 0 {
		return nil
	}
	if d.Kind == "opening_ar" || d.Kind == "opening_ap" {
		var n int
		if e := tx.QueryRow("SELECT COUNT(*) FROM bills WHERE side=? AND partner_id=? AND state='active'", side, d.PartnerID).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fail("该往来单位已有账务，期初应在正式业务前独立录入且不能重复")
		}
	}
	newID, e := idOf(tx.Exec("INSERT INTO bills(document_id,side,partner_id,amount,due_date,created_at) VALUES(?,?,?,?,?,?)", d.ID, side, d.PartnerID, d.Total, d.DueDate, stamp()))
	if e != nil {
		return e
	}
	if d.Total < 0 && d.SourceID > 0 {
		var originalID int64
		e = tx.QueryRow("SELECT id FROM bills WHERE document_id=? AND state='active'", d.SourceID).Scan(&originalID)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if e == nil {
			original, er := loadBill(tx, originalID)
			if er != nil {
				return er
			}
			amount := min(original.Amount-original.Settled, -d.Total)
			if amount > 0 {
				if er = offsetBills(tx, u, originalID, newID, amount, d.ID); er != nil {
					return er
				}
			}
		}
	}
	if flag(d.Data, "cash_sale") && side == "ar" && d.Total > 0 {
		no, er := nextNumber(tx, "ar_receipt", d.Date)
		if er != nil {
			return er
		}
		cashID, er := idOf(tx.Exec(`INSERT INTO documents(number,kind,status,partner_id,warehouse_id,source_id,date,due_date,total,note,data,created_by,created_at,updated_at,approved_by,approved_at) VALUES(?,'ar_receipt','completed',?,?,?,?,?,?,?,?,?,?,?,?,?)`, no, d.PartnerID, nullID(d.WarehouseID), d.ID, d.Date, d.Date, d.Total, "现销自动收款："+d.Number, js(M{"cash_sale_source": d.ID}), u.ID, stamp(), stamp(), u.ID, stamp()))
		if er != nil {
			return er
		}
		sid, er := idOf(tx.Exec("INSERT INTO settlements(document_id,side,partner_id,amount,created_at) VALUES(?,'ar',?,?,?)", cashID, d.PartnerID, d.Total, stamp()))
		if er != nil {
			return er
		}
		if er = a.allocate(tx, u, sid, []Allocation{{BillID: newID, Amount: money(d.Total)}}); er != nil {
			return er
		}
		if er = audit(tx, u, "local", "现销自动收款", "ar_receipt", cashID, nil, M{"source_number": d.Number, "amount": money(d.Total)}); er != nil {
			return er
		}
	}
	return nil
}
func (a *App) applySettlement(tx *sql.Tx, u User, d Doc) error {
	side := "ar"
	if strings.HasPrefix(d.Kind, "ap_") {
		side = "ap"
	}
	amount := d.Total
	if strings.HasSuffix(d.Kind, "refund") {
		amount = -amount
	}
	id, e := idOf(tx.Exec("INSERT INTO settlements(document_id,side,partner_id,amount,created_at) VALUES(?,?,?,?,?)", d.ID, side, d.PartnerID, amount, stamp()))
	if e != nil {
		return e
	}
	if e = a.allocate(tx, u, id, nil); e != nil {
		return e
	}
	if amount < 0 {
		return refundAdvance(tx, u, id)
	}
	return nil
}

func (a *App) allocate(tx *sql.Tx, u User, sid int64, allocations []Allocation) error {
	s, e := loadSettlement(tx, sid)
	if e != nil {
		return e
	}
	if s.State != "active" {
		return fail("收付款单已冲销")
	}
	if e = financeScope(tx, u, s.DocumentID); e != nil {
		return e
	}
	if len(allocations) == 0 {
		scope, args := scopeSQL(u, "d")
		values := []any{s.Side, s.PartnerID}
		values = append(values, args...)
		rows, er := tx.Query("SELECT b.id,b.amount-b.settled FROM bills b JOIN documents d ON d.id=b.document_id WHERE b.side=? AND b.partner_id=? AND b.state='active' AND b.amount<>b.settled"+scope+" ORDER BY b.due_date,b.id", values...)
		if er != nil {
			return er
		}
		remaining := s.Amount - s.Allocated
		for rows.Next() {
			var id, open int64
			if er = rows.Scan(&id, &open); er != nil {
				rows.Close()
				return er
			}
			if remaining == 0 || open > 0 && remaining < 0 || open < 0 && remaining > 0 {
				continue
			}
			n := min(abs(remaining), abs(open))
			allocations = append(allocations, Allocation{id, money(n)})
			if remaining > 0 {
				remaining -= n
			} else {
				remaining += n
			}
		}
		er = rows.Err()
		rows.Close()
		if er != nil {
			return er
		}
	}
	for _, v := range allocations {
		b, er := loadBill(tx, v.BillID)
		if er != nil {
			return er
		}
		if er = financeScope(tx, u, b.DocumentID); er != nil {
			return er
		}
		if b.State != "active" || b.Side != s.Side || b.PartnerID != s.PartnerID {
			return fail("核销账单须属于同一往来单位和账务方向")
		}
		n, er := fixed(v.Amount, 2)
		if er != nil || n <= 0 {
			return fail("核销金额必须大于零")
		}
		if b.Amount < 0 {
			n = -n
		}
		remaining := s.Amount - s.Allocated
		open := b.Amount - b.Settled
		if remaining == 0 || remaining > 0 && n < 0 || remaining < 0 && n > 0 || abs(n) > abs(open) || abs(n) > abs(remaining) || open > 0 && n < 0 || open < 0 && n > 0 {
			return fail("核销金额超过未核销余额或方向不匹配")
		}
		if _, er = tx.Exec("INSERT INTO writeoffs(settlement_id,bill_id,amount,created_by,created_at) VALUES(?,?,?,?,?)", sid, b.ID, n, u.ID, stamp()); er != nil {
			return er
		}
		if _, er = tx.Exec("UPDATE bills SET settled=settled+? WHERE id=?", n, b.ID); er != nil {
			return er
		}
		if _, er = tx.Exec("UPDATE settlements SET allocated=allocated+? WHERE id=?", n, sid); er != nil {
			return er
		}
		s.Allocated += n
	}
	return nil
}

func offsetBills(tx *sql.Tx, u User, positive, negative, amount, documentID int64) error {
	p, e := loadBill(tx, positive)
	if e != nil {
		return e
	}
	n, e := loadBill(tx, negative)
	if e != nil {
		return e
	}
	if e = financeScope(tx, u, p.DocumentID); e != nil {
		return e
	}
	if e = financeScope(tx, u, n.DocumentID); e != nil {
		return e
	}
	if p.State != "active" || n.State != "active" || p.Side != n.Side || p.PartnerID != n.PartnerID || amount <= 0 || p.Amount-p.Settled < amount || n.Settled-n.Amount < amount {
		return fail("冲账金额超过可用余额，或账单方向 / 往来单位不一致")
	}
	if _, e = tx.Exec("UPDATE bills SET settled=settled+? WHERE id=?", amount, p.ID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE bills SET settled=settled-? WHERE id=?", amount, n.ID); e != nil {
		return e
	}
	_, e = tx.Exec("INSERT INTO bill_offsets(positive_bill_id,negative_bill_id,amount,document_id,created_by,created_at) VALUES(?,?,?,?,?,?)", p.ID, n.ID, amount, documentID, u.ID, stamp())
	return e
}
func refundAdvance(tx *sql.Tx, u User, refundID int64) error {
	r, e := loadSettlement(tx, refundID)
	if e != nil {
		return e
	}
	remaining := -(r.Amount - r.Allocated)
	if remaining <= 0 {
		return nil
	}
	scope, args := scopeSQL(u, "d")
	values := []any{r.Side, r.PartnerID}
	values = append(values, args...)
	rows, e := tx.Query("SELECT s.id,s.amount-s.allocated FROM settlements s JOIN documents d ON d.id=s.document_id WHERE s.side=? AND s.partner_id=? AND s.state='active' AND s.amount-s.allocated>0"+scope+" ORDER BY s.id", values...)
	if e != nil {
		return e
	}
	type pair struct{ id, n int64 }
	items := []pair{}
	for rows.Next() {
		var p pair
		if e = rows.Scan(&p.id, &p.n); e != nil {
			rows.Close()
			return e
		}
		items = append(items, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, p := range items {
		n := min(remaining, p.n)
		if n <= 0 {
			break
		}
		if _, e = tx.Exec("UPDATE settlements SET allocated=allocated+? WHERE id=?", n, p.id); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE settlements SET allocated=allocated-? WHERE id=?", n, r.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO settlement_offsets(positive_settlement_id,negative_settlement_id,amount,created_by,created_at) VALUES(?,?,?,?,?)", p.id, r.ID, n, u.ID, stamp()); e != nil {
			return e
		}
		remaining -= n
	}
	if remaining > 0 {
		return fail("退款超过可退账单与预收 / 预付余额")
	}
	return nil
}

func (a *App) Bills(u User, side string, partner int64, onlyOpen bool) ([]M, error) {
	where := " WHERE b.side=? AND b.state='active'"
	args := []any{side}
	if partner > 0 {
		where += " AND b.partner_id=?"
		args = append(args, partner)
	}
	if onlyOpen {
		where += " AND b.amount<>b.settled"
	}
	scope, sargs := scopeSQL(u, "d")
	where += scope
	args = append(args, sargs...)
	rows, e := a.DB.Query(`SELECT b.id,b.document_id,d.number,d.kind,b.partner_id,p.name,b.amount,b.settled,b.due_date,d.date,b.created_at FROM bills b JOIN documents d ON d.id=b.document_id JOIN base_records p ON p.id=b.partner_id`+where+" ORDER BY b.id DESC", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []M{}
	now, _ := time.Parse("2006-01-02", today())
	for rows.Next() {
		var id, did, pid, amt, settled int64
		var no, kind, pn, due, date, created string
		if e = rows.Scan(&id, &did, &no, &kind, &pid, &pn, &amt, &settled, &due, &date, &created); e != nil {
			return nil, e
		}
		dueTime, _ := time.Parse("2006-01-02", due)
		age := int(now.Sub(dueTime).Hours() / 24)
		if age < 0 {
			age = 0
		}
		out = append(out, M{"id": id, "document_id": did, "number": no, "kind": kind, "partner_id": pid, "partner_name": pn, "side": side, "amount": money(amt), "settled": money(settled), "remaining": money(amt - settled), "due_date": due, "date": date, "overdue_days": age, "created_at": created})
	}
	return out, rows.Err()
}
func (a *App) Settlements(u User, side string, partner int64) ([]M, error) {
	where := " WHERE s.side=? AND s.state='active'"
	args := []any{side}
	if partner > 0 {
		where += " AND s.partner_id=?"
		args = append(args, partner)
	}
	scope, sargs := scopeSQL(u, "d")
	where += scope
	args = append(args, sargs...)
	rows, e := a.DB.Query(`SELECT s.id,s.document_id,d.number,d.kind,s.partner_id,p.name,s.amount,s.allocated,d.date,s.created_at FROM settlements s JOIN documents d ON d.id=s.document_id JOIN base_records p ON p.id=s.partner_id`+where+" ORDER BY s.id DESC", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, did, pid, amt, allocated int64
		var no, kind, pn, date, created string
		if e = rows.Scan(&id, &did, &no, &kind, &pid, &pn, &amt, &allocated, &date, &created); e != nil {
			return nil, e
		}
		out = append(out, M{"id": id, "document_id": did, "number": no, "kind": kind, "partner_id": pid, "partner_name": pn, "amount": money(amt), "allocated": money(allocated), "remaining": money(amt - allocated), "date": date, "created_at": created})
	}
	return out, rows.Err()
}
func (a *App) Statement(u User, side string, partner int64, start, end string) (M, error) {
	if partner <= 0 {
		return nil, fail("请选择往来单位")
	}
	if start == "" {
		start = time.Now().Format("2006-01") + "-01"
	}
	if end == "" {
		end = today()
	}
	if !dateValid(start) || !dateValid(end) || start > end {
		return nil, fail("对账日期范围无效")
	}
	scope, args := scopeSQL(u, "d")
	values := []any{side, partner}
	values = append(values, args...)
	second := append([]any{side, partner}, args...)
	values = append(values, second...)
	query := `SELECT d.id AS document_id,d.number AS number,d.kind AS kind,d.date AS date,b.amount AS charge,0 AS paid FROM bills b JOIN documents d ON d.id=b.document_id WHERE b.state='active' AND b.side=? AND b.partner_id=?` + scope + ` UNION ALL SELECT d.id,d.number,d.kind,d.date,0,s.amount FROM settlements s JOIN documents d ON d.id=s.document_id WHERE s.state='active' AND s.side=? AND s.partner_id=?` + scope + ` ORDER BY 4,1`
	rows, e := a.DB.Query(query, values...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []M{}
	opening, balance, charges, paid := int64(0), int64(0), int64(0), int64(0)
	for rows.Next() {
		var id, charge, payment int64
		var no, kind, date string
		if e = rows.Scan(&id, &no, &kind, &date, &charge, &payment); e != nil {
			return nil, e
		}
		if date > end {
			continue
		}
		balance += charge - payment
		if date < start {
			opening = balance
			continue
		}
		charges += charge
		paid += payment
		k, _ := kindOf(kind)
		out = append(out, M{"document_id": id, "number": no, "kind_name": k.Name, "date": date, "charge": money(charge), "paid": money(payment), "balance": money(balance)})
	}
	return M{"opening": money(opening), "charges": money(charges), "paid": money(paid), "closing": money(balance), "items": out, "start": start, "end": end}, rows.Err()
}

func (a *App) Aging(u User, side string) ([]M, error) {
	bs, e := a.Bills(u, side, 0, true)
	if e != nil {
		return nil, e
	}
	groups := map[int64]M{}
	sums := map[int64][]int64{}
	ids := []int64{}
	for _, b := range bs {
		id := number(b, "partner_id")
		if groups[id] == nil {
			groups[id] = M{"partner_id": id, "partner_name": b["partner_name"]}
			sums[id] = make([]int64, 8)
			ids = append(ids, id)
		}
		n, _ := fixed(str(b, "remaining"), 2)
		idx := 0
		days := number(b, "overdue_days")
		if n < 0 {
			idx = 6
		} else if str(b, "due_date") < today() {
			switch {
			case days <= 30:
				idx = 1
			case days <= 60:
				idx = 2
			case days <= 90:
				idx = 3
			case days <= 180:
				idx = 4
			default:
				idx = 5
			}
		}
		sums[id][idx] += n
		sums[id][7] += n
	}
	out := []M{}
	for _, id := range ids {
		row := groups[id]
		for i, key := range []string{"current", "days_1_30", "days_31_60", "days_61_90", "days_91_180", "days_180_plus", "credit", "total"} {
			row[key] = money(sums[id][i])
		}
		out = append(out, row)
	}
	return out, nil
}

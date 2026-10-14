package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

func queryMaps(q Queryer, query string, args ...any) ([]M, error) {
	rows, e := q.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	out := []M{}
	for rows.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range v {
			ptr[i] = &v[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			return nil, e
		}
		m := M{}
		for i, c := range cols {
			if b, ok := v[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = v[i]
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const activeDocSQL = "d.status IN ('approved','in_progress','completed','closed')"
const salesKindsSQL = "('sales_delivery','retail_sale','sales_return','sales_exchange','consignment_settlement')"
const purchaseKindsSQL = "('purchase_receipt','purchase_return','purchase_exchange')"
const itemSignSQL = "CASE WHEN d.kind IN ('sales_return','purchase_return') OR (d.kind='sales_exchange' AND i.flow='in') OR (d.kind='purchase_exchange' AND i.flow='out') THEN -1 ELSE 1 END"

func (a *App) Dashboard(u User) (M, error) {
	start := time.Now().Format("2006-01") + "-01"
	scope, args := scopeSQL(u, "d")
	result := M{"month": time.Now().Format("2006-01"), "today": today()}
	var sales, purchase, cost int64
	if u.Can("sales.view") || u.Can("reports.view") {
		values := append([]any{start, today()}, args...)
		if e := a.DB.QueryRow("SELECT COALESCE(SUM(d.total),0),COALESCE(SUM(d.cost),0) FROM documents d WHERE "+activeDocSQL+" AND d.kind IN "+salesKindsSQL+" AND d.date>=? AND d.date<=?"+scope, values...).Scan(&sales, &cost); e != nil {
			return nil, e
		}
	}
	if u.Can("purchase.view") || u.Can("reports.view") {
		values := append([]any{start, today()}, args...)
		if e := a.DB.QueryRow("SELECT COALESCE(SUM(d.total),0) FROM documents d WHERE "+activeDocSQL+" AND d.kind IN "+purchaseKindsSQL+" AND d.date>=? AND d.date<=?"+scope, values...).Scan(&purchase); e != nil {
			return nil, e
		}
	}
	var adjustments int64
	values := append([]any{start, today()}, args...)
	if e := a.DB.QueryRow("SELECT COALESCE(SUM(c.amount),0) FROM cost_adjustments c JOIN documents d ON d.id=c.document_id WHERE "+activeDocSQL+" AND d.date>=? AND d.date<=?"+scope, values...).Scan(&adjustments); e != nil {
		return nil, e
	}
	inv, e := a.Inventory(u, 0, "")
	if e != nil {
		return nil, e
	}
	var stockValue, reserved int64
	products := map[int64]bool{}
	byWarehouse := map[string]int64{}
	for _, i := range inv {
		v, _ := fixed(str(i, "value"), 2)
		stockValue += v
		q, _ := fixed(str(i, "reserved"), 4)
		reserved += q
		products[number(i, "product_id")] = true
		byWarehouse[str(i, "warehouse_name")] += v
	}
	ar, ap, arAdvance, apAdvance := int64(0), int64(0), int64(0), int64(0)
	if u.Can("finance.view") || u.Can("reports.view") {
		for _, side := range []string{"ar", "ap"} {
			values := append([]any{side}, args...)
			var open, advance int64
			if e = a.DB.QueryRow("SELECT COALESCE(SUM(b.amount-b.settled),0) FROM bills b JOIN documents d ON d.id=b.document_id WHERE b.state='active' AND b.side=?"+scope, values...).Scan(&open); e != nil {
				return nil, e
			}
			if e = a.DB.QueryRow("SELECT COALESCE(SUM(s.amount-s.allocated),0) FROM settlements s JOIN documents d ON d.id=s.document_id WHERE s.state='active' AND s.side=?"+scope, values...).Scan(&advance); e != nil {
				return nil, e
			}
			if side == "ar" {
				ar = open
				arAdvance = advance
			} else {
				ap = open
				apAdvance = advance
			}
		}
	}
	result["sales"] = money(sales)
	result["purchase"] = money(purchase)
	result["gross_profit"] = money(sales - cost - adjustments)
	result["cost_adjustments"] = money(adjustments)
	result["inventory_value"] = money(stockValue)
	result["sku_count"] = len(products)
	result["reserved"] = qty(reserved)
	result["ar"] = money(ar)
	result["ap"] = money(ap)
	result["ar_advance"] = money(arAdvance)
	result["ap_advance"] = money(apAdvance)
	margin := "0.00"
	if sales != 0 {
		margin = money(mulDiv(sales-cost-adjustments, 10000, sales))
	}
	result["gross_margin"] = margin
	trendStart := time.Now().AddDate(0, 0, -29).Format("2006-01-02")
	values = append([]any{trendStart, today()}, args...)
	rows, e := a.DB.Query("SELECT d.date,COALESCE(SUM(CASE WHEN d.kind IN "+salesKindsSQL+" THEN d.total ELSE 0 END),0),COALESCE(SUM(CASE WHEN d.kind IN "+purchaseKindsSQL+" THEN d.total ELSE 0 END),0) FROM documents d WHERE "+activeDocSQL+" AND d.date>=? AND d.date<=?"+scope+" GROUP BY d.date", values...)
	if e != nil {
		return nil, e
	}
	days := map[string][2]int64{}
	for rows.Next() {
		var date string
		var s, p int64
		if e = rows.Scan(&date, &s, &p); e != nil {
			rows.Close()
			return nil, e
		}
		days[date] = [2]int64{s, p}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	trend := []M{}
	for i := 29; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		v := days[day]
		trend = append(trend, M{"date": day, "sales": money(v[0]), "purchase": money(v[1])})
	}
	result["trend"] = trend
	warehouses := []M{}
	for name, value := range byWarehouse {
		warehouses = append(warehouses, M{"name": name, "value": money(value)})
	}
	sort.Slice(warehouses, func(i, j int) bool { return str(warehouses[i], "name") < str(warehouses[j], "name") })
	result["warehouses"] = warehouses
	pending, total, e := a.Documents(u, M{"status": "submitted", "page": 1, "page_size": 6})
	if e != nil {
		return nil, e
	}
	result["pending"] = pending
	result["pending_count"] = total
	warnings, e := a.Warnings(u)
	if e != nil {
		return nil, e
	}
	unread := 0
	for _, w := range warnings {
		if !flag(w, "acknowledged") {
			unread++
		}
	}
	result["warning_count"] = unread
	if len(warnings) > 6 {
		warnings = warnings[:6]
	}
	result["warnings"] = warnings
	aging, e := a.Aging(u, "ar")
	if e != nil {
		return nil, e
	}
	buckets := make([]int64, 6)
	for _, r := range aging {
		for i, k := range []string{"current", "days_1_30", "days_31_60", "days_61_90", "days_91_180", "days_180_plus"} {
			v, _ := fixed(str(r, k), 2)
			buckets[i] += v
		}
	}
	agingChart := []M{}
	for i, label := range []string{"未到期", "1–30 天", "31–60 天", "61–90 天", "91–180 天", "180 天以上"} {
		agingChart = append(agingChart, M{"name": label, "value": money(buckets[i])})
	}
	result["aging"] = agingChart
	return result, nil
}

func (a *App) Report(u User, category, group, start, end string) ([]M, error) {
	if category == "inventory" {
		return a.Inventory(u, 0, "")
	}
	if category == "ar" || category == "ap" {
		return a.Aging(u, category)
	}
	if start == "" {
		start = time.Now().Format("2006-01") + "-01"
	}
	if end == "" {
		end = today()
	}
	if !dateValid(start) || !dateValid(end) || start > end {
		return nil, fail("报表日期范围无效")
	}
	if category == "operations" {
		return a.OperationReport(u, start, end)
	}
	if category == "inventory_count" {
		return a.CountReport(u, start, end)
	}
	if category != "sales" && category != "purchase" && category != "sales_returns" && category != "purchase_returns" {
		return nil, fail("报表类型无效")
	}
	groups := map[string]string{"date": "d.date", "month": "substr(d.date,1,7)", "product": "i.product_code || ' · ' || i.product_name", "partner": "COALESCE(p.name,'未指定')", "employee": "COALESCE(emp.name,'未指定业务员')", "channel": "CASE WHEN d.kind='retail_sale' OR src.kind='retail_sale' THEN '零售' WHEN d.kind='consignment_settlement' OR src.kind='consignment_settlement' THEN '委托代销' ELSE '批发' END", "flow": "CASE WHEN (" + itemSignSQL + ")<0 THEN '退回 / 冲减' ELSE '正常业务' END"}
	dimension, ok := groups[group]
	if !ok {
		return nil, fail("分组维度无效")
	}
	kinds := salesKindsSQL
	if strings.HasPrefix(category, "purchase") {
		kinds = purchaseKindsSQL
	}
	scope, args := scopeSQL(u, "d")
	values := append([]any{start, end}, args...)
	if strings.HasSuffix(category, "_returns") {
		scope += " AND (" + itemSignSQL + ")<0"
	}
	query := `SELECT ` + dimension + ` AS dimension,COUNT(DISTINCT d.id) AS document_count,COALESCE(SUM(i.quantity*(` + itemSignSQL + `)),0) AS quantity,COALESCE(SUM(i.amount*(` + itemSignSQL + `)),0) AS amount,COALESCE(SUM(i.cost*(` + itemSignSQL + `)),0) AS cost FROM documents d JOIN document_items i ON i.document_id=d.id LEFT JOIN base_records p ON p.id=d.partner_id LEFT JOIN documents src ON src.id=d.source_id LEFT JOIN base_records emp ON emp.id=json_extract(d.data,'$.employee_id') WHERE ` + activeDocSQL + ` AND d.kind IN ` + kinds + ` AND d.date>=? AND d.date<=?` + scope + ` GROUP BY dimension ORDER BY `
	if group == "date" || group == "month" {
		query += "dimension"
	} else {
		query += "amount DESC"
	}
	out, e := queryMaps(a.DB, query, values...)
	if e != nil {
		return nil, e
	}
	for _, row := range out {
		q, amt, cost := number(row, "quantity"), number(row, "amount"), number(row, "cost")
		row["quantity"] = qty(q)
		row["amount"] = money(amt)
		row["cost"] = money(cost)
		row["profit"] = money(amt - cost)
		margin := int64(0)
		if amt != 0 {
			margin = mulDiv(amt-cost, 10000, amt)
		}
		row["margin"] = money(margin) + "%"
		if q != 0 {
			row["avg_price"] = qty(mulDiv(amt, 1000000, q))
		} else {
			row["avg_price"] = "0"
		}
	}
	return out, nil
}

func (a *App) CountReport(u User, start, end string) ([]M, error) {
	scope, args := scopeSQL(u, "d")
	values := append([]any{start, end}, args...)
	rows, e := queryMaps(a.DB, `SELECT d.id AS document_id,d.number,d.date,w.name AS warehouse_name,i.product_code,i.product_name,i.batch,i.book_quantity,i.quantity AS actual_quantity,i.quantity-i.book_quantity AS delta,COALESCE((SELECT SUM(t.cost_delta) FROM inventory_transactions t WHERE t.document_id=d.id AND t.item_id=i.id),0) AS cost_delta FROM documents d JOIN document_items i ON i.document_id=d.id JOIN base_records w ON w.id=d.warehouse_id WHERE d.kind='inventory_check' AND `+activeDocSQL+` AND d.date>=? AND d.date<=?`+scope+` ORDER BY d.date,d.id,i.id`, values...)
	if e != nil {
		return nil, e
	}
	for _, r := range rows {
		for _, k := range []string{"book_quantity", "actual_quantity", "delta"} {
			r[k] = qty(number(r, k))
		}
		r["cost_delta"] = money(number(r, "cost_delta"))
	}
	return rows, nil
}
func (a *App) OperationReport(u User, start, end string) ([]M, error) {
	scope, args := scopeSQL(u, "d")
	from, _ := time.Parse("2006-01-02", start)
	until, _ := time.Parse("2006-01-02", end)
	if until.After(from.AddDate(10, 0, 0)) {
		return nil, fail("经营分析单次查询范围不能超过 10 年，请分段导出")
	}
	cursor := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC)
	out := []M{}
	for !cursor.After(until) {
		periodStart := cursor.Format("2006-01-02")
		if periodStart < start {
			periodStart = start
		}
		next := cursor.AddDate(0, 1, 0)
		periodEnd := next.AddDate(0, 0, -1).Format("2006-01-02")
		if periodEnd > end {
			periodEnd = end
		}
		values := append([]any{periodStart, periodEnd}, args...)
		var sales, purchases, cost, adjustments, received, paid int64
		if e := a.DB.QueryRow("SELECT COALESCE(SUM(CASE WHEN d.kind IN "+salesKindsSQL+" THEN d.total ELSE 0 END),0),COALESCE(SUM(CASE WHEN d.kind IN "+purchaseKindsSQL+" THEN d.total ELSE 0 END),0),COALESCE(SUM(CASE WHEN d.kind IN "+salesKindsSQL+" THEN d.cost ELSE 0 END),0) FROM documents d WHERE "+activeDocSQL+" AND d.date>=? AND d.date<=?"+scope, values...).Scan(&sales, &purchases, &cost); e != nil {
			return nil, e
		}
		if e := a.DB.QueryRow("SELECT COALESCE(SUM(c.amount),0) FROM cost_adjustments c JOIN documents d ON d.id=c.document_id WHERE "+activeDocSQL+" AND d.date>=? AND d.date<=?"+scope, values...).Scan(&adjustments); e != nil {
			return nil, e
		}
		if e := a.DB.QueryRow("SELECT COALESCE(SUM(CASE WHEN s.side='ar' THEN s.amount ELSE 0 END),0),COALESCE(SUM(CASE WHEN s.side='ap' THEN s.amount ELSE 0 END),0) FROM settlements s JOIN documents d ON d.id=s.document_id WHERE s.state='active' AND d.date>=? AND d.date<=?"+scope, values...).Scan(&received, &paid); e != nil {
			return nil, e
		}
		stockScope := ""
		stockArgs := []any{}
		if len(u.Warehouses) > 0 {
			stockScope = " AND i.warehouse_id IN (" + strings.TrimRight(strings.Repeat("?,", len(u.Warehouses)), ",") + ")"
			for _, w := range u.Warehouses {
				stockArgs = append(stockArgs, w)
			}
		}
		var opening, closing int64
		stockValues := append([]any{periodStart, periodEnd}, stockArgs...)
		if e := a.DB.QueryRow("SELECT COALESCE(SUM(CASE WHEN d.date<? THEN t.cost_delta ELSE 0 END),0),COALESCE(SUM(CASE WHEN d.date<=? THEN t.cost_delta ELSE 0 END),0) FROM inventory_transactions t JOIN inventory i ON i.id=t.inventory_id JOIN documents d ON d.id=t.document_id WHERE 1=1"+stockScope, stockValues...).Scan(&opening, &closing); e != nil {
			return nil, e
		}
		average := mulDiv(opening+closing, 1, 2)
		collection, turnover, days := "—", "—", "—"
		if sales > 0 {
			collection = money(mulDiv(received, 10000, sales)) + "%"
		}
		if average > 0 {
			turnover = qty(mulDiv(cost, 10000, average))
			if cost > 0 {
				s, _ := time.Parse("2006-01-02", periodStart)
				en, _ := time.Parse("2006-01-02", periodEnd)
				dayCount := int64(en.Sub(s).Hours()/24) + 1
				days = money(mulDiv(average, dayCount*100, cost))
			}
		}
		out = append(out, M{"period": cursor.Format("2006-01"), "sales": money(sales), "purchase": money(purchases), "sales_cost": money(cost), "cost_adjustments": money(adjustments), "gross_profit": money(sales - cost - adjustments), "cash_received": money(received), "cash_paid": money(paid), "collection_rate": collection, "opening_inventory_value": money(opening), "closing_inventory_value": money(closing), "average_inventory_value": money(average), "inventory_turnover": turnover, "stock_days": days})
		cursor = next
	}
	return out, nil
}

func (a *App) Warnings(u User) ([]M, error) {
	if !u.Can("warnings.view") {
		return []M{}, nil
	}
	out := []M{}
	add := func(kind, level, title, message, entity string, id int64, marker string) {
		keyBytes := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d/%s", kind, entity, id, marker)))
		out = append(out, M{"key": hex.EncodeToString(keyBytes[:]), "kind": kind, "level": level, "title": title, "message": message, "entity": entity, "entity_id": id, "acknowledged": false})
	}
	stocks, e := a.Inventory(u, 0, "")
	if e != nil {
		return nil, e
	}
	type aggregate struct {
		pid, wid, q, res, min, max int64
		pn, wn                     string
	}
	grouped := map[string]*aggregate{}
	for _, s := range stocks {
		key := fmt.Sprintf("%d/%d", number(s, "product_id"), number(s, "warehouse_id"))
		g := grouped[key]
		if g == nil {
			lo, _ := fixed(str(s, "min_stock"), 4)
			hi, _ := fixed(str(s, "max_stock"), 4)
			g = &aggregate{pid: number(s, "product_id"), wid: number(s, "warehouse_id"), min: lo, max: hi, pn: str(s, "product_name"), wn: str(s, "warehouse_name")}
			grouped[key] = g
		}
		q, _ := fixed(str(s, "quantity"), 4)
		r, _ := fixed(str(s, "reserved"), 4)
		g.q += q
		g.res += r
	}
	products, e := a.Masters(u, "product", "", true)
	if e != nil {
		return nil, e
	}
	warehouses, e := a.Masters(u, "warehouse", "", true)
	if e != nil {
		return nil, e
	}
	for _, p := range products {
		lo, _ := fixed(str(p.Data, "min_stock"), 4)
		if lo <= 0 {
			continue
		}
		for _, w := range warehouses {
			if flag(w.Data, "consignment") {
				continue
			}
			key := fmt.Sprintf("%d/%d", p.ID, w.ID)
			if grouped[key] == nil {
				grouped[key] = &aggregate{pid: p.ID, wid: w.ID, min: lo, pn: p.Name, wn: w.Name}
			}
		}
	}
	staleDays, _ := strconv.Atoi(settingString(a.DB, "stale_days"))
	if staleDays < 1 {
		staleDays = 90
	}
	for key, g := range grouped {
		available := g.q - g.res
		if available < g.min {
			add("low_stock", "high", g.pn+" 库存不足", g.wn+" · 可用 "+qty(available)+"，下限 "+qty(g.min), "inventory", g.pid, key+"/"+qty(available))
		}
		if g.max > 0 && g.q > g.max {
			add("high_stock", "medium", g.pn+" 库存超限", g.wn+" · 现存 "+qty(g.q)+"，上限 "+qty(g.max), "inventory", g.pid, key+"/"+qty(g.q))
		}
		if g.q > 0 {
			var last string
			if e = a.DB.QueryRow(`SELECT COALESCE(MAX(CASE WHEN t.delta<0 AND d.kind IN ('sales_delivery','retail_sale','consignment_settlement','sales_exchange') THEN t.created_at END),MIN(t.created_at),'') FROM inventory_transactions t JOIN inventory i ON i.id=t.inventory_id JOIN documents d ON d.id=t.document_id WHERE i.product_id=? AND i.warehouse_id=?`, g.pid, g.wid).Scan(&last); e != nil {
				return nil, e
			}
			t, er := time.Parse(time.RFC3339, last)
			if er == nil && time.Since(t) >= time.Duration(staleDays)*24*time.Hour {
				add("stale_stock", "medium", g.pn+" 长期未动销", fmt.Sprintf("%s · 已 %d 天未销售，现存 %s", g.wn, int(time.Since(t).Hours()/24), qty(g.q)), "inventory", g.pid, key+"/"+last)
			}
		}
	}
	if u.Can("finance.view") || u.Can("reports.view") {
		for _, side := range []string{"ar", "ap"} {
			bills, e := a.Bills(u, side, 0, true)
			if e != nil {
				return nil, e
			}
			for _, b := range bills {
				n, _ := fixed(str(b, "remaining"), 2)
				if n <= 0 {
					continue
				}
				due := str(b, "due_date")
				if side == "ar" && due < today() {
					add("ar_overdue", "high", str(b, "partner_name")+" 应收逾期", str(b, "number")+" · "+str(b, "remaining")+" 元，逾期 "+str(b, "overdue_days")+" 天", "document", number(b, "document_id"), str(b, "remaining")+due)
				}
				if side == "ap" && due <= time.Now().AddDate(0, 0, 7).Format("2006-01-02") {
					add("ap_due", "medium", str(b, "partner_name")+" 应付到期", str(b, "number")+" · "+str(b, "remaining")+" 元，到期日 "+due, "document", number(b, "document_id"), str(b, "remaining")+due)
				}
			}
		}
		customers, e := a.Masters(u, "customer", "", true)
		if e != nil {
			return nil, e
		}
		for _, c := range customers {
			limit, _ := fixed(str(c.Data, "credit_limit"), 2)
			if limit <= 0 {
				continue
			}
			bs, e := a.Bills(u, "ar", c.ID, true)
			if e != nil {
				return nil, e
			}
			ss, e := a.Settlements(u, "ar", c.ID)
			if e != nil {
				return nil, e
			}
			var exposure int64
			for _, b := range bs {
				n, _ := fixed(str(b, "remaining"), 2)
				exposure += n
			}
			for _, s := range ss {
				n, _ := fixed(str(s, "remaining"), 2)
				exposure -= n
			}
			if exposure > limit {
				add("credit_limit", "high", c.Name+" 信用超额", "占用 "+money(exposure)+" 元，额度 "+money(limit)+" 元", "customer", c.ID, money(exposure))
			}
		}
	}
	if u.Can("sales.view") {
		docs, _, e := a.Documents(u, M{"module": "sales", "status": "submitted", "page_size": 200})
		if e != nil {
			return nil, e
		}
		for _, d := range docs {
			full, e := loadDoc(a.DB, d.ID)
			if e != nil {
				return nil, e
			}
			for _, it := range full.Items {
				if d.Kind == "sales_return" || d.Kind == "sales_exchange" && it.Flow == "in" {
					continue
				}
				p, e := master(a.DB, it.ProductID, "product", false)
				if e != nil {
					return nil, e
				}
				minimum, _ := fixed(str(p.Data, "min_price"), 4)
				if it.P < minimum {
					add("low_price", "high", it.ProductName+" 低于最低售价", d.Number+" · 单价 "+qty(it.P)+"，最低售价 "+qty(minimum), "document", d.ID, fmt.Sprint(it.ID))
				}
			}
		}
	}
	acks, e := queryMaps(a.DB, "SELECT warning_key,created_at FROM warning_ack")
	if e != nil {
		return nil, e
	}
	ackMap := map[string]string{}
	for _, v := range acks {
		ackMap[str(v, "warning_key")] = str(v, "created_at")
	}
	for _, w := range out {
		if at := ackMap[str(w, "key")]; at != "" {
			w["acknowledged"] = true
			w["acknowledged_at"] = at
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if flag(out[i], "acknowledged") != flag(out[j], "acknowledged") {
			return !flag(out[i], "acknowledged")
		}
		if str(out[i], "level") != str(out[j], "level") {
			return str(out[i], "level") == "high"
		}
		return str(out[i], "title") < str(out[j], "title")
	})
	return out, nil
}

func (a *App) SerialRecords(u User, search string) ([]M, error) {
	query := `SELECT s.id,s.product_id,p.code AS product_code,p.name AS product_name,s.serial,i.warehouse_id,w.name AS warehouse_name,COALESCE(l.name,'') AS location_name,i.batch,s.state,s.updated_at FROM inventory_serials s JOIN base_records p ON p.id=s.product_id JOIN inventory i ON i.id=s.inventory_id JOIN base_records w ON w.id=i.warehouse_id LEFT JOIN base_records l ON l.id=i.location_id WHERE (s.serial LIKE ? OR p.name LIKE ?) ORDER BY s.id DESC`
	rows, e := queryMaps(a.DB, query, "%"+search+"%", "%"+search+"%")
	if e != nil {
		return nil, e
	}
	out := []M{}
	for _, r := range rows {
		if u.Warehouse(number(r, "warehouse_id")) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (a *App) WriteoffRecords(u User, side string) ([]M, error) {
	scope, args := scopeSQL(u, "d")
	values := append([]any{side}, args...)
	rows, e := queryMaps(a.DB, `SELECT w.id,w.bill_id,w.settlement_id,w.amount,w.reversed,w.created_at,d.number AS settlement_number,bd.number AS bill_number,p.name AS partner_name,usr.name AS operator FROM writeoffs w JOIN settlements s ON s.id=w.settlement_id JOIN documents d ON d.id=s.document_id JOIN bills b ON b.id=w.bill_id JOIN documents bd ON bd.id=b.document_id JOIN base_records p ON p.id=s.partner_id JOIN users usr ON usr.id=w.created_by WHERE s.side=?`+scope+` ORDER BY w.id DESC LIMIT 10000`, values...)
	if e != nil {
		return nil, e
	}
	out := []M{}
	for _, r := range rows {
		b, er := loadBill(a.DB, number(r, "bill_id"))
		if er != nil {
			return nil, er
		}
		if financeScope(a.DB, u, b.DocumentID) != nil {
			continue
		}
		r["amount"] = money(number(r, "amount"))
		out = append(out, r)
	}
	return out, nil
}
func (a *App) reverseWriteoff(tx *sql.Tx, u User, id int64, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fail("请填写撤销核销原因")
	}
	var sid, bid, n int64
	var reversed bool
	if e := tx.QueryRow("SELECT settlement_id,bill_id,amount,reversed FROM writeoffs WHERE id=?", id).Scan(&sid, &bid, &n, &reversed); e != nil {
		return notFound(e)
	}
	if reversed {
		return fail("该核销记录已撤销")
	}
	s, e := loadSettlement(tx, sid)
	if e != nil {
		return e
	}
	b, e := loadBill(tx, bid)
	if e != nil {
		return e
	}
	if e = financeScope(tx, u, s.DocumentID); e != nil {
		return e
	}
	if e = financeScope(tx, u, b.DocumentID); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE bills SET settled=settled-? WHERE id=?", n, bid); e != nil {
		return e
	}
	if _, e = tx.Exec("UPDATE settlements SET allocated=allocated-? WHERE id=?", n, sid); e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE writeoffs SET reversed=1 WHERE id=?", id)
	return e
}

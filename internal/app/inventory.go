package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type stock struct {
	ID, ProductID, WarehouseID, LocationID, Q, Reserved, Value int64
	Batch                                                      string
}

func getStock(q Queryer, product, warehouse, location int64, batch string, create bool) (stock, error) {
	s := stock{ProductID: product, WarehouseID: warehouse, LocationID: location, Batch: batch}
	if create {
		if _, e := q.Exec("INSERT OR IGNORE INTO inventory(product_id,warehouse_id,location_id,batch,updated_at) VALUES(?,?,?,?,?)", product, warehouse, location, batch, stamp()); e != nil {
			return s, e
		}
	}
	e := q.QueryRow("SELECT id,quantity,reserved,value FROM inventory WHERE product_id=? AND warehouse_id=? AND location_id=? AND batch=?", product, warehouse, location, batch).Scan(&s.ID, &s.Q, &s.Reserved, &s.Value)
	if e == sql.ErrNoRows && !create {
		return s, nil
	}
	return s, e
}

// move is the only writer of physical stock balances. Each change is represented
// by an immutable transaction in the same SQLite write transaction as its document.
// incomingCost is cents for receipts, or -1 to calculate weighted-average issues.
func (a *App) move(tx *sql.Tx, u User, d Doc, item Item, warehouse, location, delta, incomingCost int64) (int64, error) {
	if delta == 0 {
		return 0, nil
	}
	if !u.Warehouse(warehouse) {
		return 0, denied("没有库存仓库权限")
	}
	if e := locationValid(tx, location, warehouse); e != nil {
		return 0, e
	}
	p, e := master(tx, item.ProductID, "product", true)
	if e != nil {
		return 0, e
	}
	if flag(p.Data, "batch_tracking") && strings.TrimSpace(item.Batch) == "" {
		return 0, fail(item.ProductName + " 必须填写批次")
	}
	s, e := getStock(tx, item.ProductID, warehouse, location, item.Batch, true)
	if e != nil {
		return 0, e
	}
	newQ := s.Q + delta
	if e = bounded(newQ); e != nil {
		return 0, e
	}
	if delta < 0 && (newQ < s.Reserved) && (!settingBool(tx, "allow_negative_stock") || s.Reserved > 0 || flag(p.Data, "serial_tracking")) {
		return 0, fail(fmt.Sprintf("%s 可用库存不足：可用 %s，需要 %s", item.ProductName, qty(s.Q-s.Reserved), qty(-delta)))
	}
	valueDelta := incomingCost
	if delta < 0 {
		if incomingCost >= 0 {
			valueDelta = -incomingCost
		} else if s.Q > 0 {
			valueDelta = -mulDiv(-delta, s.Value, s.Q)
		} else {
			price, _ := fixed(str(p.Data, "purchase_price"), 4)
			valueDelta = -amount(-delta, price)
		}
		if newQ == 0 {
			valueDelta = -s.Value
		}
	} else if incomingCost < 0 {
		return 0, fail("入库成本无效")
	}
	if delta > 0 && s.Q < 0 {
		cover := min(delta, -s.Q)
		oldCost := mulDiv(-s.Value, cover, -s.Q)
		newCost := mulDiv(incomingCost, cover, delta)
		variance := newCost - oldCost
		valueDelta -= variance
		if variance != 0 {
			if _, e = tx.Exec("INSERT INTO cost_adjustments(document_id,item_id,amount,note,created_at) VALUES(?,?,?,?,?)", d.ID, item.ID, variance, "负库存回补成本差异", stamp()); e != nil {
				return 0, e
			}
		}
	}
	newValue := s.Value + valueDelta
	if e = bounded(newValue); e != nil {
		return 0, e
	}
	if newQ >= 0 && newValue < 0 {
		return 0, fail("库存成本不足，请先核对后续业务")
	}
	serials := item.Serials
	if serials == nil {
		serials = []string{}
	}
	if flag(p.Data, "serial_tracking") {
		if abs(delta)%Scale != 0 || int64(len(serials))*Scale != abs(delta) {
			return 0, fail(item.ProductName + " 的序列号数量必须等于变动数量（盘点填写差异序列号）")
		}
		seen := map[string]bool{}
		for _, serial := range serials {
			serial = strings.TrimSpace(serial)
			if serial == "" || len(serial) > 200 || seen[serial] {
				return 0, fail("序列号不能为空或重复，最长 200 字节")
			}
			seen[serial] = true
			var inv int64
			var state string
			er := tx.QueryRow("SELECT inventory_id,state FROM inventory_serials WHERE product_id=? AND serial=?", item.ProductID, serial).Scan(&inv, &state)
			if delta > 0 {
				if er == nil && state == "in" {
					return 0, fail("序列号 " + serial + " 已在库存中")
				}
				if er != nil && er != sql.ErrNoRows {
					return 0, er
				}
				if _, e = tx.Exec("INSERT INTO inventory_serials(product_id,serial,inventory_id,state,updated_at) VALUES(?,?,?,'in',?) ON CONFLICT(product_id,serial) DO UPDATE SET inventory_id=excluded.inventory_id,state='in',updated_at=excluded.updated_at", item.ProductID, serial, s.ID, stamp()); e != nil {
					return 0, e
				}
			} else {
				if er != nil || state != "in" || inv != s.ID {
					return 0, fail("序列号 " + serial + " 不在指定库存中")
				}
				if _, e = tx.Exec("UPDATE inventory_serials SET state='out',updated_at=? WHERE product_id=? AND serial=?", stamp(), item.ProductID, serial); e != nil {
					return 0, e
				}
			}
		}
	}
	if _, e = tx.Exec("UPDATE inventory SET quantity=?,value=?,updated_at=? WHERE id=?", newQ, newValue, stamp(), s.ID); e != nil {
		return 0, e
	}
	if _, e = tx.Exec("INSERT INTO inventory_transactions(document_id,item_id,inventory_id,before_quantity,delta,after_quantity,cost_delta,before_value,after_value,serials,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", d.ID, item.ID, s.ID, s.Q, delta, newQ, valueDelta, s.Value, newValue, js(serials), u.ID, stamp()); e != nil {
		return 0, e
	}
	return abs(valueDelta), nil
}

func reserveOrder(tx *sql.Tx, d Doc) error {
	for _, it := range d.Items {
		s, e := getStock(tx, it.ProductID, d.WarehouseID, it.LocationID, it.Batch, true)
		if e != nil {
			return e
		}
		if s.Q-s.Reserved < it.Q {
			return fail(it.ProductName + " 可用库存不足，不能占用订单数量")
		}
		if _, e = tx.Exec("UPDATE inventory SET reserved=reserved+?,updated_at=? WHERE id=?", it.Q, stamp(), s.ID); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO stock_reservations(document_id,item_id,inventory_id,quantity) VALUES(?,?,?,?)", d.ID, it.ID, s.ID, it.Q); e != nil {
			return e
		}
	}
	return nil
}
func consumeReservation(tx *sql.Tx, sourceItemID, quantity int64) error {
	var id, inv, q, c int64
	e := tx.QueryRow("SELECT id,inventory_id,quantity,consumed FROM stock_reservations WHERE item_id=?", sourceItemID).Scan(&id, &inv, &q, &c)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	if c+quantity > q {
		return fail("出库数量超过订单剩余占用数量")
	}
	if _, e = tx.Exec("UPDATE stock_reservations SET consumed=consumed+? WHERE id=?", quantity, id); e != nil {
		return e
	}
	_, e = tx.Exec("UPDATE inventory SET reserved=reserved-? WHERE id=?", quantity, inv)
	return e
}
func releaseReservations(tx *sql.Tx, docID int64) error {
	rows, e := tx.Query("SELECT id,inventory_id,quantity-consumed FROM stock_reservations WHERE document_id=?", docID)
	if e != nil {
		return e
	}
	type r struct{ id, inv, remaining int64 }
	rs := []r{}
	for rows.Next() {
		var v r
		if e = rows.Scan(&v.id, &v.inv, &v.remaining); e != nil {
			rows.Close()
			return e
		}
		rs = append(rs, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range rs {
		if _, e = tx.Exec("UPDATE inventory SET reserved=reserved-? WHERE id=?", v.remaining, v.inv); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE stock_reservations SET quantity=consumed WHERE id=?", v.id); e != nil {
			return e
		}
	}
	return nil
}

func (a *App) applyStock(tx *sql.Tx, u User, d *Doc) error {
	if d.Kind == "sales_order" {
		return reserveOrder(tx, *d)
	}
	if d.Kind == "purchase_order" || isMoney(d.Kind) || d.Kind == "opening_ar" || d.Kind == "opening_ap" {
		return nil
	}
	if d.Kind == "assembly" || d.Kind == "split" {
		return a.assemble(tx, u, d)
	}
	for i := range d.Items {
		it := &d.Items[i]
		var cost int64
		var e error
		switch d.Kind {
		case "purchase_receipt", "opening_inventory":
			if d.Kind == "opening_inventory" {
				s, er := getStock(tx, it.ProductID, d.WarehouseID, it.LocationID, it.Batch, true)
				if er != nil {
					return er
				}
				var n int
				_ = tx.QueryRow("SELECT COUNT(*) FROM inventory_transactions WHERE inventory_id=?", s.ID).Scan(&n)
				if n > 0 {
					return fail(it.ProductName + " 已有库存流水，不能重复录入期初")
				}
			}
			cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, it.Q, it.A)
		case "purchase_return":
			cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, -it.Q, -1)
		case "sales_delivery", "retail_sale", "consignment_settlement":
			if it.SourceItemID > 0 {
				if e = consumeReservation(tx, it.SourceItemID, it.Q); e != nil {
					return e
				}
			}
			cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, -it.Q, -1)
		case "sales_return":
			cost, e = originalCost(tx, *it)
			if e == nil {
				cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, it.Q, cost)
			}
		case "purchase_exchange":
			if it.Flow == "out" {
				cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, -it.Q, -1)
			} else {
				cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, it.Q, it.A)
			}
		case "sales_exchange":
			if it.Flow == "in" {
				cost, e = originalCost(tx, *it)
				if e == nil {
					cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, it.Q, cost)
				}
			} else {
				cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, -it.Q, -1)
			}
		case "transfer", "consignment_ship", "consignment_return":
			cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, -it.Q, -1)
			if e == nil {
				_, e = a.move(tx, u, *d, *it, d.TargetWarehouseID, it.TargetLocationID, it.Q, cost)
			}
		case "inventory_check":
			s, er := getStock(tx, it.ProductID, d.WarehouseID, it.LocationID, it.Batch, true)
			if er != nil {
				return er
			}
			if s.Q != it.Book {
				return fail(it.ProductName + " 的库存已发生变化，请退回草稿重新保存盘点单")
			}
			delta := it.Q - it.Book
			incoming := int64(-1)
			if delta > 0 {
				if s.Q > 0 {
					incoming = mulDiv(delta, s.Value, s.Q)
				} else {
					incoming = amount(delta, it.P)
				}
			}
			cost, e = a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, delta, incoming)
		default:
			return fail("未实现的库存业务类型")
		}
		if e != nil {
			return e
		}
		it.C = cost
		if _, e = tx.Exec("UPDATE document_items SET cost=? WHERE id=?", cost, it.ID); e != nil {
			return e
		}
		sign := int64(1)
		if d.Kind == "sales_return" || d.Kind == "purchase_return" || d.Kind == "sales_exchange" && it.Flow == "in" || d.Kind == "purchase_exchange" && it.Flow == "out" {
			sign = -1
		}
		d.Cost += cost * sign
	}
	_, e := tx.Exec("UPDATE documents SET cost=? WHERE id=?", d.Cost, d.ID)
	return e
}

func originalCost(q Queryer, it Item) (int64, error) {
	var c, quantity int64
	if e := q.QueryRow("SELECT cost,quantity FROM document_items WHERE id=?", it.SourceItemID).Scan(&c, &quantity); e != nil {
		return 0, fail("退货缺少有效的原单明细")
	}
	var usedQ, usedCost int64
	if e := q.QueryRow("SELECT COALESCE(SUM(i.quantity),0),COALESCE(SUM(i.cost),0) FROM document_items i JOIN documents d ON d.id=i.document_id WHERE i.source_item_id=? AND d.kind<>'reversal' AND d.status IN ('approved','completed','in_progress','closed')", it.SourceItemID).Scan(&usedQ, &usedCost); e != nil {
		return 0, e
	}
	if usedQ+it.Q == quantity {
		return c - usedCost, nil
	}
	return min(mulDiv(c, it.Q, quantity), c-usedCost), nil
}
func (a *App) assemble(tx *sql.Tx, u User, d *Doc) error {
	totalCost, weights := int64(0), int64(0)
	incoming := []int{}
	for i := range d.Items {
		it := &d.Items[i]
		if it.Flow == "out" {
			c, e := a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, -it.Q, -1)
			if e != nil {
				return e
			}
			it.C = c
			totalCost += c
		} else {
			incoming = append(incoming, i)
			w := it.A
			if w == 0 {
				w = it.Q
			}
			weights += w
		}
	}
	if len(incoming) == 0 || weights <= 0 {
		return fail("至少需要一条产出明细")
	}
	remaining := totalCost
	for idx, i := range incoming {
		it := &d.Items[i]
		w := it.A
		if w == 0 {
			w = it.Q
		}
		cost := mulDiv(totalCost, w, weights)
		if idx == len(incoming)-1 {
			cost = remaining
		}
		remaining -= cost
		actual, e := a.move(tx, u, *d, *it, d.WarehouseID, it.LocationID, it.Q, cost)
		if e != nil {
			return e
		}
		it.C = actual
	}
	for _, it := range d.Items {
		if _, e := tx.Exec("UPDATE document_items SET cost=? WHERE id=?", it.C, it.ID); e != nil {
			return e
		}
	}
	d.Cost = totalCost
	_, e := tx.Exec("UPDATE documents SET cost=? WHERE id=?", totalCost, d.ID)
	return e
}

func (a *App) Inventory(u User, warehouse int64, search string) ([]M, error) {
	query := `SELECT i.id,i.product_id,p.code,p.name,p.data,i.warehouse_id,w.name,w.data,i.location_id,COALESCE(l.name,''),i.batch,i.quantity,i.reserved,i.value,i.updated_at FROM inventory i JOIN base_records p ON p.id=i.product_id JOIN base_records w ON w.id=i.warehouse_id LEFT JOIN base_records l ON l.id=i.location_id WHERE 1=1`
	args := []any{}
	if warehouse > 0 {
		query += " AND i.warehouse_id=?"
		args = append(args, warehouse)
	}
	if search != "" {
		query += " AND (p.name LIKE ? OR p.code LIKE ? OR i.batch LIKE ?)"
		for i := 0; i < 3; i++ {
			args = append(args, "%"+search+"%")
		}
	}
	query += " ORDER BY p.code,w.name,i.batch,i.location_id"
	rows, e := a.DB.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, pid, wid, lid, q, r, v int64
		var code, name, pdata, wn, wdata, ln, batch, updated string
		if e = rows.Scan(&id, &pid, &code, &name, &pdata, &wid, &wn, &wdata, &lid, &ln, &batch, &q, &r, &v, &updated); e != nil {
			return nil, e
		}
		if !u.Warehouse(wid) {
			continue
		}
		pd := obj(pdata)
		avg := int64(0)
		if q != 0 {
			avg = mulDiv(v, 1000000, q)
		}
		out = append(out, M{"id": id, "product_id": pid, "product_code": code, "product_name": name, "unit": str(pd, "unit"), "warehouse_id": wid, "warehouse_name": wn, "consignment": flag(obj(wdata), "consignment"), "location_id": lid, "location_name": ln, "batch": batch, "quantity": qty(q), "reserved": qty(r), "available": qty(q - r), "value": money(v), "avg_cost": qty(avg), "min_stock": str(pd, "min_stock"), "max_stock": str(pd, "max_stock"), "updated_at": updated})
	}
	return out, rows.Err()
}
func (a *App) StockTransactions(u User, docID, warehouse int64, search string) ([]M, error) {
	query := `SELECT t.id,t.document_id,d.number,d.kind,p.code,p.name,w.id,w.name,COALESCE(l.name,''),i.batch,t.before_quantity,t.delta,t.after_quantity,t.cost_delta,t.before_value,t.after_value,t.serials,usr.name,t.created_at FROM inventory_transactions t JOIN inventory i ON i.id=t.inventory_id JOIN documents d ON d.id=t.document_id JOIN base_records p ON p.id=i.product_id JOIN base_records w ON w.id=i.warehouse_id LEFT JOIN base_records l ON l.id=i.location_id JOIN users usr ON usr.id=t.created_by WHERE 1=1`
	args := []any{}
	if docID > 0 {
		query += " AND d.id=?"
		args = append(args, docID)
	}
	if warehouse > 0 {
		query += " AND i.warehouse_id=?"
		args = append(args, warehouse)
	}
	if search != "" {
		query += " AND (d.number LIKE ? OR p.name LIKE ? OR p.code LIKE ?)"
		for i := 0; i < 3; i++ {
			args = append(args, "%"+search+"%")
		}
	}
	scope, sargs := scopeSQL(u, "d")
	query += scope + " ORDER BY t.id DESC"
	args = append(args, sargs...)
	rows, e := a.DB.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, did, wid, before, delta, after, cost, bv, av int64
		var no, kind, pc, pn, wn, ln, batch, serials, name, created string
		if e = rows.Scan(&id, &did, &no, &kind, &pc, &pn, &wid, &wn, &ln, &batch, &before, &delta, &after, &cost, &bv, &av, &serials, &name, &created); e != nil {
			return nil, e
		}
		if !u.Warehouse(wid) {
			continue
		}
		var sns []string
		_ = json.Unmarshal([]byte(serials), &sns)
		out = append(out, M{"id": id, "document_id": did, "number": no, "kind": kind, "product_code": pc, "product_name": pn, "warehouse_name": wn, "location_name": ln, "batch": batch, "before_quantity": qty(before), "delta": qty(delta), "after_quantity": qty(after), "cost_delta": money(cost), "before_value": money(bv), "after_value": money(av), "serials": sns, "operator": name, "created_at": created})
	}
	return out, rows.Err()
}

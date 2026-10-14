package app

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
)

type Mutation struct{ Key, Endpoint, Hash string }

func NewMutation(key, endpoint string, body []byte) Mutation {
	h := sha256.Sum256(body)
	return Mutation{key, endpoint, hex.EncodeToString(h[:])}
}
func (a *App) mutate(u User, m Mutation, fn func(*sql.Tx) (any, error)) (any, error) {
	var response any
	if len(m.Key) < 8 || len(m.Key) > 128 {
		return nil, fail("操作缺少有效的 Idempotency-Key，请刷新页面后重试")
	}
	e := a.write(func(tx *sql.Tx) error {
		var endpoint, hash, saved string
		er := tx.QueryRow("SELECT endpoint,request_hash,response FROM idempotency WHERE user_id=? AND key=?", u.ID, m.Key).Scan(&endpoint, &hash, &saved)
		if er == nil {
			if endpoint != m.Endpoint || hash != m.Hash {
				return fail("同一幂等键不能用于不同的请求")
			}
			return json.Unmarshal([]byte(saved), &response)
		}
		if er != sql.ErrNoRows {
			return er
		}
		response, er = fn(tx)
		if er != nil {
			return er
		}
		_, er = tx.Exec("INSERT INTO idempotency(user_id,key,endpoint,request_hash,response,created_at) VALUES(?,?,?,?,?,?)", u.ID, m.Key, m.Endpoint, m.Hash, js(response), stamp())
		return er
	})
	return response, e
}

func (a *App) actDocument(tx *sql.Tx, u User, ip string, id int64, action, reason string) (Doc, error) {
	d, e := loadDoc(tx, id)
	if e != nil {
		return d, e
	}
	permission := "edit"
	if action == "approve" || action == "reverse" || action == "reject" && u.Can(moduleOf(d.Kind)+".approve") {
		permission = "approve"
	}
	if e = a.docAccess(u, d, permission); e != nil {
		return d, e
	}
	before := d
	switch action {
	case "submit":
		if d.Status != "draft" {
			return d, fail("仅草稿可提交审核")
		}
		d.Status = "submitted"
	case "reject":
		if d.Status != "submitted" {
			return d, fail("仅待审核单据可退回")
		}
		if !u.Can(moduleOf(d.Kind)+".approve") && d.CreatedBy != u.ID {
			return d, denied("只有审核人或制单人可以退回")
		}
		d.Status = "draft"
	case "cancel":
		if d.Status != "draft" && d.Status != "submitted" {
			return d, fail("仅草稿或待审核单据可作废")
		}
		d.Status = "cancelled"
	case "close":
		if !isOrder(d.Kind) || !posted(d.Status) || d.Status == "closed" {
			return d, fail("仅已审核且未关闭的订单可关闭")
		}
		if strings.TrimSpace(reason) == "" {
			return d, fail("请填写关闭原因")
		}
		if e = releaseReservations(tx, d.ID); e != nil {
			return d, e
		}
		d.Status = "closed"
	case "approve":
		if posted(d.Status) {
			return d, nil
		}
		if d.Status != "submitted" {
			return d, fail("请先提交单据，再执行审核")
		}
		if e = validateSource(tx, d); e != nil {
			return d, e
		}
		k, _ := kindOf(d.Kind)
		if k.Party != "" {
			if _, e = master(tx, d.PartnerID, k.Party, true); e != nil {
				return d, e
			}
		}
		for _, wid := range []int64{d.WarehouseID, d.TargetWarehouseID} {
			if wid > 0 {
				if _, e = master(tx, wid, "warehouse", true); e != nil {
					return d, e
				}
			}
		}
		if e = a.checkCredit(tx, d); e != nil {
			return d, e
		}
		if e = a.applyStock(tx, u, &d); e != nil {
			return d, e
		}
		if e = a.applyFinance(tx, u, d); e != nil {
			return d, e
		}
		d.Status = "completed"
		if isOrder(d.Kind) {
			d.Status = "approved"
		}
		if _, e = tx.Exec("UPDATE documents SET approved_by=?,approved_at=? WHERE id=?", u.ID, stamp(), d.ID); e != nil {
			return d, e
		}
	case "reverse":
		if !posted(d.Status) {
			return d, fail("仅已审核单据可红冲")
		}
		if strings.TrimSpace(reason) == "" {
			return d, fail("请填写红冲原因")
		}
		if e = a.reverseDocument(tx, u, d, reason); e != nil {
			return d, e
		}
		d.Status = "reversed"
	default:
		return d, fail("不支持的单据操作")
	}
	if _, e = tx.Exec("UPDATE documents SET status=?,updated_at=?,revision=revision+1 WHERE id=?", d.Status, stamp(), d.ID); e != nil {
		return d, e
	}
	if d.SourceID > 0 {
		if e = refreshOrder(tx, d.SourceID); e != nil {
			return d, e
		}
	}
	if e = audit(tx, u, ip, map[string]string{"submit": "提交审核", "reject": "退回草稿", "cancel": "作废", "close": "关闭订单", "approve": "审核入账", "reverse": "红冲"}[action], d.Kind, d.ID, M{"number": before.Number, "status": before.Status}, M{"number": d.Number, "status": d.Status, "reason": reason}); e != nil {
		return d, e
	}
	return loadDoc(tx, d.ID)
}
func moduleOf(kind string) string { k, _ := kindOf(kind); return k.Module }
func refreshOrder(q Queryer, id int64) error {
	d, e := loadDoc(q, id)
	if e != nil {
		return e
	}
	if !isOrder(d.Kind) || !posted(d.Status) || d.Status == "closed" {
		return nil
	}
	var total, used int64
	for _, it := range d.Items {
		total += it.Q
		n, _, er := sourceUsed(q, it.ID, 0)
		if er != nil {
			return er
		}
		used += n
	}
	status := "approved"
	if used >= total {
		status = "completed"
	} else if used > 0 {
		status = "in_progress"
	}
	_, e = q.Exec("UPDATE documents SET status=?,revision=revision+1,updated_at=? WHERE id=?", status, stamp(), id)
	return e
}

func (a *App) reverseDocument(tx *sql.Tx, u User, d Doc, reason string) error {
	var e error
	var child int64
	if e := tx.QueryRow("SELECT COUNT(*) FROM documents WHERE source_id=? AND kind<>'reversal' AND status IN ('approved','in_progress','completed','closed')", d.ID).Scan(&child); e != nil {
		return e
	}
	if child > 0 {
		return fail("该单据已有已审核下游单据，请先冲销下游单据；退货请使用退货业务")
	}
	if e = unwindFinance(tx, d.ID); e != nil {
		return e
	}
	if isOrder(d.Kind) {
		return releaseReservations(tx, d.ID)
	}
	rows, e := tx.Query("SELECT DISTINCT inventory_id FROM inventory_transactions WHERE document_id=?", d.ID)
	if e != nil {
		return e
	}
	invs := []int64{}
	for rows.Next() {
		var id int64
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		invs = append(invs, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, inv := range invs {
		var later int
		if e = tx.QueryRow("SELECT COUNT(*) FROM inventory_transactions WHERE inventory_id=? AND document_id<>? AND id>(SELECT MAX(id) FROM inventory_transactions WHERE inventory_id=? AND document_id=?)", inv, d.ID, inv, d.ID).Scan(&later); e != nil {
			return e
		}
		if later > 0 {
			return fail("涉及库存已有后续流水，不能反向覆盖成本；请使用退货、换货或盘点单处理")
		}
	}
	no, e := nextNumber(tx, "reversal", today())
	if e != nil {
		return e
	}
	reversalID, e := idOf(tx.Exec(`INSERT INTO documents(number,kind,status,partner_id,warehouse_id,target_warehouse_id,source_id,date,due_date,total,cost,note,data,created_by,created_at,updated_at,approved_by,approved_at) VALUES(?,'reversal','completed',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, no, nullID(d.PartnerID), nullID(d.WarehouseID), nullID(d.TargetWarehouseID), d.ID, today(), today(), -d.Total, -d.Cost, reason, js(M{"original_kind": d.Kind, "original_number": d.Number}), u.ID, stamp(), stamp(), u.ID, stamp()))
	if e != nil {
		return e
	}
	itemIDs := map[int64]int64{}
	for _, it := range d.Items {
		original := it.ID
		it.SourceItemID = original
		id, er := insertItem(tx, reversalID, it)
		if er != nil {
			return er
		}
		itemIDs[original] = id
	}
	rows, e = tx.Query("SELECT item_id,inventory_id,before_quantity,delta,after_quantity,cost_delta,before_value,after_value,serials FROM inventory_transactions WHERE document_id=? ORDER BY id DESC", d.ID)
	if e != nil {
		return e
	}
	type movement struct {
		item, inv, bq, delta, aq, cost, bv, av int64
		serials                                string
	}
	moves := []movement{}
	for rows.Next() {
		var m movement
		if e = rows.Scan(&m.item, &m.inv, &m.bq, &m.delta, &m.aq, &m.cost, &m.bv, &m.av, &m.serials); e != nil {
			rows.Close()
			return e
		}
		moves = append(moves, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, m := range moves {
		var current, value, reserved, product int64
		if e = tx.QueryRow("SELECT quantity,value,reserved,product_id FROM inventory WHERE id=?", m.inv).Scan(&current, &value, &reserved, &product); e != nil {
			return e
		}
		if current != m.aq || value != m.av {
			return fail("库存余额与原流水不一致，禁止红冲")
		}
		if m.bq < reserved && reserved > 0 {
			return fail("库存已被其他销售订单占用，请先关闭相关订单")
		}
		var serials []string
		_ = json.Unmarshal([]byte(m.serials), &serials)
		for _, serial := range serials {
			if m.delta > 0 {
				if _, e = tx.Exec("UPDATE inventory_serials SET state='out',updated_at=? WHERE product_id=? AND serial=?", stamp(), product, serial); e != nil {
					return e
				}
			} else {
				if _, e = tx.Exec("UPDATE inventory_serials SET inventory_id=?,state='in',updated_at=? WHERE product_id=? AND serial=?", m.inv, stamp(), product, serial); e != nil {
					return e
				}
			}
		}
		if _, e = tx.Exec("UPDATE inventory SET quantity=?,value=?,updated_at=? WHERE id=?", m.bq, m.bv, stamp(), m.inv); e != nil {
			return e
		}
		if _, e = tx.Exec("INSERT INTO inventory_transactions(document_id,item_id,inventory_id,before_quantity,delta,after_quantity,cost_delta,before_value,after_value,serials,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", reversalID, itemIDs[m.item], m.inv, m.aq, -m.delta, m.bq, -m.cost, m.av, m.bv, m.serials, u.ID, stamp()); e != nil {
			return e
		}
	}
	if d.SourceID > 0 {
		var parentKind, parentStatus string
		if e = tx.QueryRow("SELECT kind,status FROM documents WHERE id=?", d.SourceID).Scan(&parentKind, &parentStatus); e != nil {
			return e
		}
		if parentKind == "sales_order" {
			for _, it := range d.Items {
				var id, inv int64
				if e = tx.QueryRow("SELECT id,inventory_id FROM stock_reservations WHERE item_id=?", it.SourceItemID).Scan(&id, &inv); e != nil {
					return e
				}
				if parentStatus == "closed" {
					if _, e = tx.Exec("UPDATE stock_reservations SET consumed=consumed-?,quantity=quantity-? WHERE id=?", it.Q, it.Q, id); e != nil {
						return e
					}
				} else {
					if _, e = tx.Exec("UPDATE stock_reservations SET consumed=consumed-? WHERE id=?", it.Q, id); e != nil {
						return e
					}
					if _, e = tx.Exec("UPDATE inventory SET reserved=reserved+? WHERE id=?", it.Q, inv); e != nil {
						return e
					}
				}
			}
		}
	}
	_, e = tx.Exec("UPDATE documents SET reversal_id=? WHERE id=?", reversalID, d.ID)
	return e
}

func unwindFinance(tx *sql.Tx, docID int64) error {
	var sid int64
	e := tx.QueryRow("SELECT id FROM settlements WHERE document_id=? AND state='active'", docID).Scan(&sid)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if sid > 0 {
		var used int
		if e = tx.QueryRow("SELECT COUNT(*) FROM settlement_offsets WHERE positive_settlement_id=? AND reversed=0", sid).Scan(&used); e != nil {
			return e
		}
		if used > 0 {
			return fail("预收 / 预付款已有退款，请先冲销对应退款单")
		}
		rows, er := tx.Query("SELECT id,positive_settlement_id,amount FROM settlement_offsets WHERE negative_settlement_id=? AND reversed=0", sid)
		if er != nil {
			return er
		}
		type entry struct{ id, target, n int64 }
		entries := []entry{}
		for rows.Next() {
			var v entry
			if er = rows.Scan(&v.id, &v.target, &v.n); er != nil {
				rows.Close()
				return er
			}
			entries = append(entries, v)
		}
		er = rows.Err()
		rows.Close()
		if er != nil {
			return er
		}
		for _, v := range entries {
			if _, er = tx.Exec("UPDATE settlements SET allocated=allocated-? WHERE id=?", v.n, v.target); er != nil {
				return er
			}
			if _, er = tx.Exec("UPDATE settlements SET allocated=allocated+? WHERE id=?", v.n, sid); er != nil {
				return er
			}
			if _, er = tx.Exec("UPDATE settlement_offsets SET reversed=1 WHERE id=?", v.id); er != nil {
				return er
			}
		}
		rows, er = tx.Query("SELECT id,bill_id,amount FROM writeoffs WHERE settlement_id=? AND reversed=0", sid)
		if er != nil {
			return er
		}
		entries = []entry{}
		for rows.Next() {
			var v entry
			if er = rows.Scan(&v.id, &v.target, &v.n); er != nil {
				rows.Close()
				return er
			}
			entries = append(entries, v)
		}
		er = rows.Err()
		rows.Close()
		if er != nil {
			return er
		}
		for _, v := range entries {
			if _, er = tx.Exec("UPDATE bills SET settled=settled-? WHERE id=?", v.n, v.target); er != nil {
				return er
			}
			if _, er = tx.Exec("UPDATE writeoffs SET reversed=1 WHERE id=?", v.id); er != nil {
				return er
			}
		}
		if _, er = tx.Exec("UPDATE settlements SET allocated=0,state='reversed' WHERE id=?", sid); er != nil {
			return er
		}
	}
	rows, e := tx.Query("SELECT id,positive_bill_id,negative_bill_id,amount FROM bill_offsets WHERE document_id=? AND reversed=0", docID)
	if e != nil {
		return e
	}
	type offset struct{ id, p, n, amount int64 }
	offsets := []offset{}
	for rows.Next() {
		var o offset
		if e = rows.Scan(&o.id, &o.p, &o.n, &o.amount); e != nil {
			rows.Close()
			return e
		}
		offsets = append(offsets, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, o := range offsets {
		if _, e = tx.Exec("UPDATE bills SET settled=settled-? WHERE id=?", o.amount, o.p); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE bills SET settled=settled+? WHERE id=?", o.amount, o.n); e != nil {
			return e
		}
		if _, e = tx.Exec("UPDATE bill_offsets SET reversed=1 WHERE id=?", o.id); e != nil {
			return e
		}
	}
	var bid, settled int64
	e = tx.QueryRow("SELECT id,settled FROM bills WHERE document_id=? AND state='active'", docID).Scan(&bid, &settled)
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	if bid > 0 {
		if settled != 0 {
			return fail("单据已有收付款或往来冲账，请先撤销核销 / 冲销关联收付款单")
		}
		_, e = tx.Exec("UPDATE bills SET state='reversed' WHERE id=?", bid)
		if e != nil {
			return e
		}
	}
	return nil
}

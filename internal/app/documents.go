package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const documentSelect = `SELECT d.id,d.number,d.kind,d.status,COALESCE(d.partner_id,0),COALESCE(p.name,''),COALESCE(d.warehouse_id,0),COALESCE(w.name,''),COALESCE(d.target_warehouse_id,0),COALESCE(tw.name,''),COALESCE(d.source_id,0),COALESCE(src.number,''),d.date,d.due_date,d.total,d.cost,d.note,d.data,d.created_by,u.name,d.created_at,d.approved_at,d.revision FROM documents d LEFT JOIN base_records p ON p.id=d.partner_id LEFT JOIN base_records w ON w.id=d.warehouse_id LEFT JOIN base_records tw ON tw.id=d.target_warehouse_id LEFT JOIN documents src ON src.id=d.source_id JOIN users u ON u.id=d.created_by `

type scanner interface{ Scan(...any) error }

func scanDoc(row scanner) (Doc, error) {
	var d Doc
	var data string
	e := row.Scan(&d.ID, &d.Number, &d.Kind, &d.Status, &d.PartnerID, &d.PartnerName, &d.WarehouseID, &d.WarehouseName, &d.TargetWarehouseID, &d.TargetWarehouseName, &d.SourceID, &d.SourceNumber, &d.Date, &d.DueDate, &d.Total, &d.Cost, &d.Note, &data, &d.CreatedBy, &d.Creator, &d.CreatedAt, &d.ApprovedAt, &d.Revision)
	if e != nil {
		return d, notFound(e)
	}
	d.Data = obj(data)
	k, _ := kindOf(d.Kind)
	d.KindName = k.Name
	d.Items = []Item{}
	d.format()
	return d, nil
}
func loadDoc(q Queryer, id int64) (Doc, error) {
	d, e := scanDoc(q.QueryRow(documentSelect+"WHERE d.id=?", id))
	if e != nil {
		return d, e
	}
	rows, e := q.Query(`SELECT id,product_id,product_name,product_code,unit,location_id,target_location_id,COALESCE(source_item_id,0),batch,serials,flow,quantity,price,amount,cost,book_quantity,price_source,note FROM document_items WHERE document_id=? ORDER BY id`, id)
	if e != nil {
		return d, e
	}
	defer rows.Close()
	for rows.Next() {
		var i Item
		var serials string
		if e = rows.Scan(&i.ID, &i.ProductID, &i.ProductName, &i.ProductCode, &i.Unit, &i.LocationID, &i.TargetLocationID, &i.SourceItemID, &i.Batch, &serials, &i.Flow, &i.Q, &i.P, &i.A, &i.C, &i.Book, &i.PriceSource, &i.Note); e != nil {
			return d, e
		}
		_ = json.Unmarshal([]byte(serials), &i.Serials)
		i.format()
		d.Items = append(d.Items, i)
	}
	return d, rows.Err()
}

func (a *App) Documents(u User, filters M) ([]Doc, int, error) {
	where := " WHERE 1=1"
	args := []any{}
	allowed := []string{}
	for _, k := range Kinds {
		if u.Can(k.Module + ".view") {
			allowed = append(allowed, k.Key)
		}
	}
	if len(allowed) == 0 {
		return []Doc{}, 0, nil
	}
	where += " AND d.kind IN (" + strings.TrimRight(strings.Repeat("?,", len(allowed)), ",") + ")"
	for _, k := range allowed {
		args = append(args, k)
	}
	for key, col := range map[string]string{"kind": "d.kind", "status": "d.status", "partner_id": "d.partner_id", "warehouse_id": "d.warehouse_id"} {
		if v := str(filters, key); v != "" && v != "0" {
			where += " AND " + col + "=?"
			args = append(args, v)
		}
	}
	if module := str(filters, "module"); module != "" {
		ks := []string{}
		for _, k := range Kinds {
			if k.Module == module {
				ks = append(ks, k.Key)
			}
		}
		if len(ks) == 0 {
			return nil, 0, fail("模块无效")
		}
		where += " AND d.kind IN (" + strings.TrimRight(strings.Repeat("?,", len(ks)), ",") + ")"
		for _, k := range ks {
			args = append(args, k)
		}
	}
	if s := str(filters, "search"); s != "" {
		where += " AND (d.number LIKE ? OR p.name LIKE ? OR d.note LIKE ?)"
		for i := 0; i < 3; i++ {
			args = append(args, "%"+s+"%")
		}
	}
	if s := str(filters, "start"); s != "" {
		where += " AND d.date>=?"
		args = append(args, s)
	}
	if s := str(filters, "end"); s != "" {
		where += " AND d.date<=?"
		args = append(args, s)
	}
	scope, sargs := scopeSQL(u, "d")
	where += scope
	args = append(args, sargs...)
	var total int
	e := a.DB.QueryRow("SELECT COUNT(*) FROM documents d LEFT JOIN base_records p ON p.id=d.partner_id"+where, args...).Scan(&total)
	if e != nil {
		return nil, 0, e
	}
	page, size := number(filters, "page"), number(filters, "page_size")
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	args = append(args, size, (page-1)*size)
	rows, e := a.DB.Query(documentSelect+where+" ORDER BY d.id DESC LIMIT ? OFFSET ?", args...)
	if e != nil {
		return nil, 0, e
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		d, er := scanDoc(rows)
		if er != nil {
			return nil, 0, er
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

func nextNumber(q Queryer, kind, date string) (string, error) {
	prefixes := map[string]string{"purchase_order": "CGDD", "purchase_receipt": "CGRK", "purchase_return": "CGTH", "purchase_exchange": "CGHH", "sales_order": "XSDD", "sales_delivery": "XSCK", "retail_sale": "LSXS", "sales_return": "XSTH", "sales_exchange": "XSHH", "consignment_ship": "WTFH", "consignment_settlement": "DXJS", "consignment_return": "WTTH", "transfer": "DB", "inventory_check": "PD", "assembly": "ZZ", "split": "CF", "opening_inventory": "QCKC", "opening_ar": "QCYS", "opening_ap": "QCYF", "ar_receipt": "SK", "ap_payment": "FK", "ar_refund": "KHTK", "ap_refund": "GYTK", "reversal": "HC"}
	prefix := prefixes[kind]
	var custom map[string]string
	_ = json.Unmarshal([]byte(setting(q, "document_prefixes")), &custom)
	if custom[kind] != "" {
		prefix = custom[kind]
	}
	if prefix == "" {
		prefix = "DJ"
	}
	day := strings.ReplaceAll(date, "-", "")
	if _, e := q.Exec("INSERT INTO document_sequences(prefix,day,value) VALUES(?,?,1) ON CONFLICT(prefix,day) DO UPDATE SET value=value+1", prefix, day); e != nil {
		return "", e
	}
	var n int64
	if e := q.QueryRow("SELECT value FROM document_sequences WHERE prefix=? AND day=?", prefix, day).Scan(&n); e != nil {
		return "", e
	}
	return fmt.Sprintf("%s-%s-%04d", prefix, day, n), nil
}

func (a *App) saveDocument(tx *sql.Tx, u User, ip string, id int64, input DocInput) (Doc, error) {
	d := Doc{ID: id, Kind: input.Kind, PartnerID: input.PartnerID, WarehouseID: input.WarehouseID, TargetWarehouseID: input.TargetWarehouseID, SourceID: input.SourceID, Date: input.Date, DueDate: input.DueDate, Note: strings.TrimSpace(input.Note), Data: input.Data, Status: "draft", CreatedBy: u.ID, Items: []Item{}}
	k, e := kindOf(d.Kind)
	if e != nil || d.Kind == "reversal" {
		return d, fail("无效的单据类型")
	}
	if !u.Can(k.Module + ".edit") {
		return d, denied("没有新增或修改此类单据的权限")
	}
	if d.Data == nil {
		d.Data = M{}
	}
	var before any
	if id > 0 {
		old, er := loadDoc(tx, id)
		if er != nil {
			return d, er
		}
		if er = a.docAccess(u, old, "edit"); er != nil {
			return d, er
		}
		if old.Status != "draft" {
			return d, fail("仅草稿单据可以修改")
		}
		if old.Kind != d.Kind {
			return d, fail("单据类型不能修改")
		}
		if input.Revision != old.Revision {
			return d, fail("单据已被其他窗口修改，请刷新后重试")
		}
		before = old
		d.CreatedBy = old.CreatedBy
		d.Number = old.Number
		d.Revision = old.Revision + 1
	} else {
		d.Revision = 1
	}
	if !u.Warehouse(d.WarehouseID) || !u.Warehouse(d.TargetWarehouseID) {
		return d, denied("没有所选仓库权限")
	}
	if d.Date == "" {
		d.Date = today()
	}
	if !dateValid(d.Date) {
		return d, fail("单据日期无效")
	}
	if len(d.Note) > 4000 {
		return d, fail("备注最多 4000 字节")
	}
	if k.Party != "" {
		p, er := master(tx, d.PartnerID, k.Party, true)
		if er != nil {
			return d, fail("请选择有效的" + map[string]string{"customer": "客户", "supplier": "供应商"}[k.Party])
		}
		d.PartnerName = p.Name
		if d.DueDate == "" {
			t, _ := time.Parse("2006-01-02", d.Date)
			d.DueDate = t.AddDate(0, 0, int(number(p.Data, "term_days"))).Format("2006-01-02")
		}
	}
	if d.DueDate != "" && !dateValid(d.DueDate) {
		return d, fail("到期日期无效")
	}
	if employee := number(d.Data, "employee_id"); employee > 0 {
		if _, e = master(tx, employee, "employee", true); e != nil {
			return d, e
		}
	}
	if k.Items {
		w, er := master(tx, d.WarehouseID, "warehouse", true)
		if er != nil {
			return d, fail("请选择有效仓库")
		}
		d.WarehouseName = w.Name
		if k.Target {
			target, er := master(tx, d.TargetWarehouseID, "warehouse", true)
			if er != nil {
				return d, fail("请选择调入仓库")
			}
			if d.WarehouseID == d.TargetWarehouseID {
				return d, fail("调出、调入仓库不能相同")
			}
			d.TargetWarehouseName = target.Name
			if d.Kind == "consignment_ship" && !flag(target.Data, "consignment") {
				return d, fail("委托发货须选择标记为委托仓的调入仓库")
			}
		}
		if d.Kind == "consignment_settlement" && !flag(w.Data, "consignment") {
			return d, fail("代销结算须从委托仓出库")
		}
		if len(input.Items) == 0 || len(input.Items) > 200 {
			return d, fail("单据需要 1～200 行明细")
		}
	} else {
		d.WarehouseID = 0
		d.TargetWarehouseID = 0
		if len(input.Items) > 0 {
			return d, fail("资金单据不包含商品明细")
		}
		d.Total, e = fixed(input.Amount, 2)
		if e != nil {
			return d, e
		}
		if d.Total == 0 || isMoney(d.Kind) && d.Total < 0 {
			return d, fail("资金单据金额须大于零，期初往来可填写负数")
		}
	}
	var source Doc
	sourceItems := map[int64]Item{}
	if d.SourceID > 0 {
		source, e = loadDoc(tx, d.SourceID)
		if e != nil {
			return d, e
		}
		if e = a.docAccess(u, source, "view"); e != nil {
			return d, e
		}
		if !posted(source.Status) {
			return d, fail("来源单据尚未审核或已被冲销")
		}
		if source.PartnerID != d.PartnerID {
			return d, fail("来源单据的往来单位不一致")
		}
		if !validConversion(source.Kind, d.Kind) {
			return d, fail("来源单据类型不支持当前业务")
		}
		if isOrder(source.Kind) && source.Status == "closed" {
			return d, fail("来源订单已关闭")
		}
		for _, it := range source.Items {
			sourceItems[it.ID] = it
		}
	}
	if needsSource(d.Kind) && d.SourceID == 0 {
		return d, fail("退货、换货和代销结算必须关联已审核原单")
	}
	seen := map[string]bool{}
	sourceSeen := map[int64]bool{}
	inCount, outCount := 0, 0
	for _, v := range input.Items {
		p, er := master(tx, v.ProductID, "product", true)
		if er != nil {
			return d, er
		}
		it := Item{ProductID: p.ID, ProductName: p.Name, ProductCode: p.Code, Unit: str(p.Data, "unit"), LocationID: v.LocationID, TargetLocationID: v.TargetLocationID, SourceItemID: v.SourceItemID, Batch: strings.TrimSpace(v.Batch), Serials: v.Serials, Flow: v.Flow, Note: v.Note}
		if len(it.Batch) > 160 || len(it.Note) > 1000 {
			return d, fail("批次或明细备注过长")
		}
		for j := range it.Serials {
			it.Serials[j] = strings.TrimSpace(it.Serials[j])
		}
		if er = locationValid(tx, it.LocationID, d.WarehouseID); er != nil {
			return d, er
		}
		if k.Target {
			if er = locationValid(tx, it.TargetLocationID, d.TargetWarehouseID); er != nil {
				return d, er
			}
		}
		it.Q, er = fixed(v.Quantity, 4)
		if er != nil {
			return d, er
		}
		if it.Q < 0 || it.Q == 0 && d.Kind != "inventory_check" {
			return d, fail(p.Name + " 数量须大于零")
		}
		if flag(p.Data, "batch_tracking") && it.Batch == "" {
			return d, fail(p.Name + " 必须填写批次")
		}
		if d.Kind == "purchase_exchange" || d.Kind == "sales_exchange" || d.Kind == "assembly" || d.Kind == "split" {
			if it.Flow != "in" && it.Flow != "out" {
				return d, fail("请选择每行明细的入库 / 出库方向")
			}
			if it.Flow == "in" {
				inCount++
			} else {
				outCount++
			}
		} else {
			it.Flow = ""
		}
		returnLine := d.Kind == "purchase_return" || d.Kind == "sales_return" || d.Kind == "purchase_exchange" && it.Flow == "out" || d.Kind == "sales_exchange" && it.Flow == "in"
		original, hasSource := sourceItems[it.SourceItemID]
		if it.SourceItemID > 0 {
			if sourceSeen[it.SourceItemID] {
				return d, fail("同一来源明细不能重复添加，请合并数量")
			}
			sourceSeen[it.SourceItemID] = true
		}
		if it.SourceItemID > 0 && !hasSource {
			return d, fail("来源明细不属于所选原单")
		}
		if returnLine && !hasSource {
			return d, fail(p.Name + " 的退回明细必须关联原单")
		}
		if hasSource {
			if original.ProductID != it.ProductID || original.Batch != it.Batch {
				return d, fail("商品或批次与原单明细不一致")
			}
			if isOrder(source.Kind) && (d.WarehouseID != source.WarehouseID || it.LocationID != original.LocationID) {
				return d, fail("执行订单须使用原订单的仓库和库位")
			}
			if source.Kind == "consignment_ship" && (d.WarehouseID != source.TargetWarehouseID || it.LocationID != original.TargetLocationID) {
				return d, fail("委托结算 / 退回须使用委托发货的目标仓库与库位")
			}
			if len(original.Serials) > 0 {
				allowed := map[string]bool{}
				for _, sn := range original.Serials {
					allowed[sn] = true
				}
				for _, sn := range it.Serials {
					if !allowed[sn] {
						return d, fail("序列号不在原单明细中")
					}
				}
			}
		}
		if source.ID > 0 && (!strings.HasSuffix(d.Kind, "exchange")) && !hasSource {
			return d, fail("关联原单时，每条明细均需选择来源明细")
		}
		salesPricing := d.Kind == "sales_order" || d.Kind == "sales_delivery" || d.Kind == "retail_sale" || d.Kind == "consignment_ship" || d.Kind == "consignment_settlement" || d.Kind == "sales_exchange" && it.Flow == "out"
		if returnLine {
			it.P = original.P
			it.PriceSource = "原单价格"
		} else if hasSource && isOrder(source.Kind) {
			it.P = original.P
			it.PriceSource = "原订单价格"
			if v.Price != "" {
				requested, er := fixed(v.Price, 4)
				if er != nil || requested < 0 {
					return d, fail("单价无效")
				}
				if requested != original.P {
					if salesPricing && !u.Can("price.override") {
						return d, denied("没有手工修改订单约定价格的权限")
					}
					it.P = requested
					it.PriceSource = "手工调价"
				}
			}
		} else if salesPricing {
			channel := "wholesale"
			if d.Kind == "retail_sale" {
				channel = "retail"
			}
			quote, er := quotePrice(tx, it.ProductID, d.PartnerID, it.Q, d.Date, channel)
			if er != nil {
				return d, er
			}
			it.P = quote.Price
			it.PriceSource = quote.Source
			if v.Price != "" {
				requested, er := fixed(v.Price, 4)
				if er != nil || requested < 0 {
					return d, fail("销售单价无效")
				}
				if requested != quote.Price {
					if !u.Can("price.override") {
						return d, denied("没有手工修改销售单价的权限，请使用自动定价")
					}
					it.P = requested
					it.PriceSource = "手工调价"
				}
			}
		} else {
			price := v.Price
			if price == "" {
				price = str(p.Data, "purchase_price")
			}
			it.P, er = fixed(price, 4)
			if er != nil || it.P < 0 {
				return d, fail("单价须为非负数，最多四位小数")
			}
			it.PriceSource = "录入成本"
		}
		it.A = amount(it.Q, it.P)
		if er = bounded(it.A); er != nil {
			return d, er
		}
		if returnLine {
			usedQ, usedA, er := sourceUsed(tx, it.SourceItemID, d.ID)
			if er != nil {
				return d, er
			}
			it.A = mulDiv(original.A, it.Q, original.Q)
			if usedQ+it.Q == original.Q {
				it.A = original.A - usedA
			}
		}
		if d.Kind == "inventory_check" {
			s, er := getStock(tx, it.ProductID, d.WarehouseID, it.LocationID, it.Batch, false)
			if er != nil {
				return d, er
			}
			it.Book = s.Q
		}
		if d.Kind == "inventory_check" || d.Kind == "opening_inventory" {
			key := fmt.Sprintf("%d|%d|%s", it.ProductID, it.LocationID, it.Batch)
			if seen[key] {
				return d, fail("同一库存维度不能重复盘点或重复初始化")
			}
			seen[key] = true
		}
		sign := int64(1)
		if d.Kind == "sales_return" || d.Kind == "purchase_return" || d.Kind == "sales_exchange" && it.Flow == "in" || d.Kind == "purchase_exchange" && it.Flow == "out" {
			sign = -1
		}
		d.Total += it.A * sign
		if er = bounded(d.Total); er != nil {
			return d, er
		}
		d.Items = append(d.Items, it)
	}
	if (strings.HasSuffix(d.Kind, "exchange") || d.Kind == "assembly" || d.Kind == "split") && (inCount == 0 || outCount == 0) {
		return d, fail("换货、组装和拆分需要同时包含入库与出库明细")
	}
	if e = validateSource(tx, d); e != nil {
		return d, e
	}
	if flag(d.Data, "cash_sale") && d.Kind != "sales_delivery" && d.Kind != "retail_sale" && d.Kind != "consignment_settlement" && d.Kind != "sales_exchange" {
		delete(d.Data, "cash_sale")
	}
	if id == 0 {
		d.Number, e = nextNumber(tx, d.Kind, d.Date)
		if e != nil {
			return d, e
		}
		d.ID, e = idOf(tx.Exec(`INSERT INTO documents(number,kind,status,partner_id,warehouse_id,target_warehouse_id,source_id,date,due_date,total,note,data,created_by,created_at,updated_at) VALUES(?,?,'draft',?,?,?,?,?,?,?,?,?,?,?,?)`, d.Number, d.Kind, nullID(d.PartnerID), nullID(d.WarehouseID), nullID(d.TargetWarehouseID), nullID(d.SourceID), d.Date, d.DueDate, d.Total, d.Note, js(d.Data), u.ID, stamp(), stamp()))
	} else {
		_, e = tx.Exec(`UPDATE documents SET partner_id=?,warehouse_id=?,target_warehouse_id=?,source_id=?,date=?,due_date=?,total=?,note=?,data=?,revision=?,updated_at=? WHERE id=?`, nullID(d.PartnerID), nullID(d.WarehouseID), nullID(d.TargetWarehouseID), nullID(d.SourceID), d.Date, d.DueDate, d.Total, d.Note, js(d.Data), d.Revision, stamp(), id)
	}
	if e != nil {
		return d, e
	}
	if id > 0 {
		if _, e = tx.Exec("DELETE FROM document_items WHERE document_id=?", id); e != nil {
			return d, e
		}
	}
	for i := range d.Items {
		it := &d.Items[i]
		it.ID, e = insertItem(tx, d.ID, *it)
		if e != nil {
			return d, e
		}
	}
	if e = audit(tx, u, ip, "保存草稿", d.Kind, d.ID, before, d); e != nil {
		return d, e
	}
	return loadDoc(tx, d.ID)
}

func insertItem(q Queryer, docID int64, it Item) (int64, error) {
	return idOf(q.Exec(`INSERT INTO document_items(document_id,product_id,product_name,product_code,unit,location_id,target_location_id,source_item_id,batch,serials,flow,quantity,price,amount,cost,book_quantity,price_source,note) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, docID, it.ProductID, it.ProductName, it.ProductCode, it.Unit, it.LocationID, it.TargetLocationID, nullID(it.SourceItemID), it.Batch, js(it.Serials), it.Flow, it.Q, it.P, it.A, it.C, it.Book, it.PriceSource, it.Note))
}
func needsSource(kind string) bool {
	return kind == "purchase_return" || kind == "sales_return" || kind == "purchase_exchange" || kind == "sales_exchange" || kind == "consignment_settlement" || kind == "consignment_return"
}
func validConversion(source, target string) bool {
	options := map[string][]string{"purchase_order": {"purchase_receipt"}, "sales_order": {"sales_delivery"}, "purchase_receipt": {"purchase_return", "purchase_exchange"}, "purchase_exchange": {"purchase_return", "purchase_exchange"}, "sales_delivery": {"sales_return", "sales_exchange"}, "retail_sale": {"sales_return", "sales_exchange"}, "sales_exchange": {"sales_return", "sales_exchange"}, "consignment_ship": {"consignment_settlement", "consignment_return"}, "consignment_settlement": {"sales_return", "sales_exchange"}}
	for _, v := range options[source] {
		if v == target {
			return true
		}
	}
	return false
}
func sourceUsed(q Queryer, itemID, excludeDoc int64) (int64, int64, error) {
	var quantity, value int64
	e := q.QueryRow(`SELECT COALESCE(SUM(i.quantity),0),COALESCE(SUM(i.amount),0) FROM document_items i JOIN documents d ON d.id=i.document_id WHERE i.source_item_id=? AND d.id<>? AND d.status IN ('approved','completed','in_progress','closed') AND d.kind<>'reversal'`, itemID, excludeDoc).Scan(&quantity, &value)
	return quantity, value, e
}
func validateSource(q Queryer, d Doc) error {
	if d.SourceID == 0 {
		return nil
	}
	source, e := loadDoc(q, d.SourceID)
	if e != nil {
		return e
	}
	if !posted(source.Status) || isOrder(source.Kind) && source.Status == "closed" {
		return fail("来源单据已失效或已关闭")
	}
	group := map[int64]int64{}
	for _, it := range d.Items {
		if it.SourceItemID > 0 {
			group[it.SourceItemID] += it.Q
		}
	}
	for _, it := range d.Items {
		if it.SourceItemID == 0 {
			continue
		}
		for _, serial := range it.Serials {
			var used int
			if e = q.QueryRow("SELECT COUNT(*) FROM document_items i JOIN documents doc ON doc.id=i.document_id, json_each(i.serials) sn WHERE i.source_item_id=? AND doc.id<>? AND doc.kind<>'reversal' AND doc.status IN ('approved','completed','in_progress','closed') AND sn.value=?", it.SourceItemID, d.ID, serial).Scan(&used); e != nil {
				return e
			}
			if used > 0 {
				return fail("序列号 " + serial + " 已执行或退回，不能重复关联同一原单")
			}
		}
	}
	for id, total := range group {
		var original Item
		found := false
		for _, it := range source.Items {
			if it.ID == id {
				original = it
				found = true
				break
			}
		}
		if !found {
			return fail("来源明细无效")
		}
		if source.Kind == "sales_exchange" && original.Flow != "out" || source.Kind == "purchase_exchange" && original.Flow != "in" {
			return fail("换货原单只能选择实际换出的销售商品或换入的采购商品")
		}
		used, _, er := sourceUsed(q, id, d.ID)
		if er != nil {
			return er
		}
		if used+total > original.Q {
			return fail(original.ProductName + " 超过原单剩余可执行 / 可退数量（剩余 " + qty(original.Q-used) + "）")
		}
	}
	return nil
}

func (a *App) Conversion(u User, id int64, target string) (DocInput, error) {
	d, e := loadDoc(a.DB, id)
	if e != nil {
		return DocInput{}, e
	}
	if e = a.docAccess(u, d, "view"); e != nil {
		return DocInput{}, e
	}
	if !posted(d.Status) || !validConversion(d.Kind, target) {
		return DocInput{}, fail("原单状态或类型不支持此转换")
	}
	input := DocInput{Kind: target, PartnerID: d.PartnerID, WarehouseID: d.WarehouseID, SourceID: d.ID, Date: today(), Data: M{}, Items: []ItemInput{}, Note: "来源：" + d.Number}
	if d.Kind == "consignment_ship" {
		input.WarehouseID = d.TargetWarehouseID
		input.TargetWarehouseID = d.WarehouseID
	}
	for _, it := range d.Items {
		if d.Kind == "sales_exchange" && it.Flow != "out" || d.Kind == "purchase_exchange" && it.Flow != "in" {
			continue
		}
		used, _, er := sourceUsed(a.DB, it.ID, 0)
		if er != nil {
			return input, er
		}
		remaining := it.Q - used
		if remaining <= 0 {
			continue
		}
		flow := ""
		if target == "sales_exchange" {
			flow = "in"
		}
		if target == "purchase_exchange" {
			flow = "out"
		}
		loc := it.LocationID
		targetLoc := int64(0)
		if d.Kind == "consignment_ship" {
			loc = it.TargetLocationID
			targetLoc = it.LocationID
		}
		serials := []string{}
		if used == 0 {
			serials = it.Serials
		}
		input.Items = append(input.Items, ItemInput{ProductID: it.ProductID, LocationID: loc, TargetLocationID: targetLoc, SourceItemID: it.ID, Batch: it.Batch, Serials: serials, Quantity: qty(remaining), Price: qty(it.P), Flow: flow})
	}
	if len(input.Items) == 0 {
		return input, fail("原单已全部执行或退回")
	}
	return input, nil
}

package app

import (
	"database/sql"
	"fmt"
	"strings"
)

var MasterKinds = map[string]string{"product": "商品", "customer": "客户", "supplier": "供应商", "warehouse": "仓库", "location": "库位", "category": "商品分类", "brand": "品牌", "unit": "单位", "employee": "员工 / 业务员", "organization": "组织", "dictionary": "数据字典"}

func master(q Queryer, id int64, kind string, active bool) (Master, error) {
	var m Master
	var data string
	e := q.QueryRow("SELECT id,kind,code,name,data,active,updated_at FROM base_records WHERE id=?", id).Scan(&m.ID, &m.Kind, &m.Code, &m.Name, &data, &m.Active, &m.UpdatedAt)
	if e != nil {
		return m, notFound(e)
	}
	m.Data = obj(data)
	if kind != "" && kind != m.Kind {
		return m, fail("基础资料类型不匹配")
	}
	if active && !m.Active {
		return m, fail(m.Name + " 已停用")
	}
	return m, nil
}
func (a *App) Masters(u User, kind, search string, activeOnly bool) ([]Master, error) {
	if _, ok := MasterKinds[kind]; !ok {
		return nil, fail("资料类型无效")
	}
	query := "SELECT id,kind,code,name,data,active,updated_at FROM base_records WHERE kind=?"
	args := []any{kind}
	if search != "" {
		query += " AND (name LIKE ? OR code LIKE ? OR data LIKE ?)"
		for i := 0; i < 3; i++ {
			args = append(args, "%"+search+"%")
		}
	}
	if activeOnly {
		query += " AND active=1"
	}
	query += " ORDER BY active DESC,code,id"
	rows, e := a.DB.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Master{}
	for rows.Next() {
		var m Master
		var data string
		if e = rows.Scan(&m.ID, &m.Kind, &m.Code, &m.Name, &data, &m.Active, &m.UpdatedAt); e != nil {
			return nil, e
		}
		m.Data = obj(data)
		if kind == "warehouse" && !u.Warehouse(m.ID) {
			continue
		}
		if kind == "location" && !u.Warehouse(number(m.Data, "warehouse_id")) {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (a *App) SaveMaster(u User, ip string, id int64, input Master) (Master, error) {
	var result Master
	if _, ok := MasterKinds[input.Kind]; !ok {
		return result, fail("资料类型无效")
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.TrimSpace(input.Code)
	if input.Name == "" || len(input.Name) > 240 || len(input.Code) > 64 {
		return result, fail("名称必填，名称最多 240 字节，编码最多 64 字节")
	}
	if input.Data == nil {
		input.Data = M{}
	}
	e := a.write(func(tx *sql.Tx) error {
		var before any
		if id > 0 {
			old, e := master(tx, id, input.Kind, false)
			if e != nil {
				return e
			}
			before = old
			if !u.Can("amount.view") {
				for _, key := range []string{"purchase_price", "sales_price", "retail_price", "guide_price", "min_price", "credit_limit", "discount_bps"} {
					if value, ok := old.Data[key]; ok {
						input.Data[key] = value
					} else {
						delete(input.Data, key)
					}
				}
			} else if !u.Can("cost.view") {
				if value, ok := old.Data["purchase_price"]; ok {
					input.Data["purchase_price"] = value
				}
			}
			if input.Kind == "warehouse" && !u.Warehouse(id) {
				return denied("没有此仓库权限")
			}
			if input.Kind == "product" {
				var n int
				_ = tx.QueryRow("SELECT COUNT(*) FROM inventory_transactions t JOIN inventory i ON i.id=t.inventory_id WHERE i.product_id=?", id).Scan(&n)
				if n > 0 && (str(old.Data, "unit") != str(input.Data, "unit") || flag(old.Data, "batch_tracking") != flag(input.Data, "batch_tracking") || flag(old.Data, "serial_tracking") != flag(input.Data, "serial_tracking")) {
					return fail("商品已有库存流水，不能变更单位、批次或序列号管理方式")
				}
			}
			if !input.Active && (input.Kind == "product" || input.Kind == "warehouse" || input.Kind == "location") {
				col := map[string]string{"product": "product_id", "warehouse": "warehouse_id", "location": "location_id"}[input.Kind]
				var n int
				_ = tx.QueryRow("SELECT COUNT(*) FROM inventory WHERE "+col+"=? AND (quantity<>0 OR reserved<>0)", id).Scan(&n)
				if n > 0 {
					return fail("尚有库存或占用数量，不能停用")
				}
			}
		}
		if input.Kind == "product" {
			if str(input.Data, "unit") == "" {
				input.Data["unit"] = "件"
			}
			for _, f := range []string{"purchase_price", "sales_price", "retail_price", "guide_price", "min_price", "min_stock", "max_stock"} {
				n, e := fixed(str(input.Data, f), 4)
				if e != nil || n < 0 {
					return fail(f + " 必须是非负数，最多四位小数")
				}
				input.Data[f] = qty(n)
			}
			lo, _ := fixed(str(input.Data, "min_stock"), 4)
			hi, _ := fixed(str(input.Data, "max_stock"), 4)
			if hi > 0 && lo > hi {
				return fail("库存上限不能小于下限")
			}
			for field, kind := range map[string]string{"category_id": "category", "brand_id": "brand"} {
				if v := number(input.Data, field); v > 0 {
					if _, e := master(tx, v, kind, true); e != nil {
						return e
					}
				}
			}
		}
		if input.Kind == "customer" || input.Kind == "supplier" {
			credit, e := fixed(str(input.Data, "credit_limit"), 2)
			if e != nil || credit < 0 {
				return fail("信用额度须为非负金额")
			}
			input.Data["credit_limit"] = money(credit)
			days := number(input.Data, "term_days")
			if days < 0 || days > 3650 {
				return fail("账期须为 0～3650 天")
			}
			discount := number(input.Data, "discount_bps")
			if str(input.Data, "discount_bps") == "" {
				discount = 10000
			}
			if discount < 0 || discount > 10000 {
				return fail("折扣应在 0～10000 基点之间")
			}
			input.Data["discount_bps"] = discount
		}
		if input.Kind == "location" {
			wid := number(input.Data, "warehouse_id")
			if !u.Warehouse(wid) {
				return denied("没有此仓库权限")
			}
			if _, e := master(tx, wid, "warehouse", true); e != nil {
				return fail("请选择有效仓库")
			}
		}
		if input.Kind == "category" {
			parent := number(input.Data, "parent_id")
			seen := map[int64]bool{id: true}
			for parent > 0 {
				if seen[parent] {
					return fail("分类不能形成循环层级")
				}
				seen[parent] = true
				m, e := master(tx, parent, "category", true)
				if e != nil {
					return e
				}
				parent = number(m.Data, "parent_id")
			}
		}
		if input.Code == "" {
			var n int64
			_ = tx.QueryRow("SELECT COALESCE(MAX(id),0)+1 FROM base_records").Scan(&n)
			prefix := map[string]string{"product": "SP", "customer": "KH", "supplier": "GY", "warehouse": "CK", "location": "KW"}[input.Kind]
			if prefix == "" {
				prefix = strings.ToUpper(input.Kind[:2])
			}
			input.Code = fmt.Sprintf("%s%05d", prefix, n)
		}
		var e error
		if id == 0 {
			id, e = idOf(tx.Exec("INSERT INTO base_records(kind,code,name,data,active,created_at,updated_at) VALUES(?,?,?,?,?,?,?)", input.Kind, input.Code, input.Name, js(input.Data), input.Active, stamp(), stamp()))
		} else {
			_, e = tx.Exec("UPDATE base_records SET code=?,name=?,data=?,active=?,updated_at=? WHERE id=?", input.Code, input.Name, js(input.Data), input.Active, stamp(), id)
		}
		if e != nil {
			return fail("编码已存在，或基础资料保存失败")
		}
		result, e = master(tx, id, input.Kind, false)
		if e != nil {
			return e
		}
		return audit(tx, u, ip, "保存资料", input.Kind, id, before, result)
	})
	return result, e
}

func locationValid(q Queryer, id, warehouse int64) error {
	if id == 0 {
		return nil
	}
	m, e := master(q, id, "location", true)
	if e != nil {
		return e
	}
	if number(m.Data, "warehouse_id") != warehouse {
		return fail("库位不属于所选仓库")
	}
	return nil
}

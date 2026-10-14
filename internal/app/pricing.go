package app

import (
	"database/sql"
	"sort"
)

type Quote struct {
	Price     int64  `json:"-"`
	Minimum   int64  `json:"-"`
	UnitPrice string `json:"price"`
	MinPrice  string `json:"min_price"`
	Source    string `json:"source"`
	RuleID    int64  `json:"rule_id"`
}

func quotePrice(q Queryer, productID, customerID, quantity int64, date, channel string) (Quote, error) {
	p, e := master(q, productID, "product", true)
	if e != nil {
		return Quote{}, e
	}
	minimum, e := fixed(str(p.Data, "min_price"), 4)
	if e != nil {
		return Quote{}, e
	}
	price, _ := fixed(str(p.Data, "sales_price"), 4)
	source := "商品销售价"
	if channel == "retail" {
		if retail, _ := fixed(str(p.Data, "retail_price"), 4); retail > 0 {
			price = retail
			source = "商品零售价"
		}
	}
	if price == 0 {
		price, _ = fixed(str(p.Data, "guide_price"), 4)
		source = "商品指导价"
	}
	level := ""
	discount := int64(10000)
	if customerID > 0 {
		customer, er := master(q, customerID, "customer", true)
		if er != nil {
			return Quote{}, er
		}
		level = str(customer.Data, "level")
		if str(customer.Data, "discount_bps") != "" {
			discount = number(customer.Data, "discount_bps")
		}
	}
	rows, e := q.Query(`SELECT id,name,kind,COALESCE(customer_id,0),customer_level,min_quantity,price,min_price,priority FROM price_rules WHERE active=1 AND product_id=? AND min_quantity<=? AND (max_quantity=0 OR max_quantity>=?) AND (start_date='' OR start_date<=?) AND (end_date='' OR end_date>=?)`, productID, quantity, quantity, date, date)
	if e != nil {
		return Quote{}, e
	}
	type candidate struct {
		id, p, min, q  int64
		rank, priority int
		name           string
	}
	matches := []candidate{}
	for rows.Next() {
		var c candidate
		var kind, l string
		var customer int64
		if e = rows.Scan(&c.id, &c.name, &kind, &customer, &l, &c.q, &c.p, &c.min, &c.priority); e != nil {
			rows.Close()
			return Quote{}, e
		}
		switch kind {
		case "customer":
			if customerID == 0 || customer != customerID {
				continue
			}
			c.rank = 500
		case "level":
			if level == "" || l != level {
				continue
			}
			c.rank = 400
		case "promotion":
			c.rank = 350
		case "quantity":
			c.rank = 300
		default:
			continue
		}
		matches = append(matches, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return Quote{}, e
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank > matches[j].rank
		}
		if matches[i].priority != matches[j].priority {
			return matches[i].priority > matches[j].priority
		}
		if matches[i].q != matches[j].q {
			return matches[i].q > matches[j].q
		}
		return matches[i].id > matches[j].id
	})
	result := Quote{Price: price, Minimum: minimum, Source: source}
	if len(matches) > 0 {
		c := matches[0]
		result.Price = c.p
		if c.min > result.Minimum {
			result.Minimum = c.min
		}
		result.Source = c.name
		result.RuleID = c.id
	} else if discount != 10000 {
		result.Price = mulDiv(price, discount, 10000)
		result.Source += " × 客户折扣"
	}
	result.UnitPrice = qty(result.Price)
	result.MinPrice = qty(result.Minimum)
	return result, nil
}

func (a *App) SavePriceRule(u User, ip string, id int64, input M) error {
	name, kind := str(input, "name"), str(input, "kind")
	if name == "" || len(name) > 200 {
		return fail("请填写价格规则名称")
	}
	if kind != "customer" && kind != "level" && kind != "quantity" && kind != "promotion" {
		return fail("无效的价格规则类型")
	}
	price, e := fixed(str(input, "price"), 4)
	if e != nil || price < 0 {
		return fail("价格须为非负数")
	}
	minimum, e := fixed(str(input, "min_price"), 4)
	if e != nil || minimum < 0 {
		return fail("最低售价须为非负数")
	}
	lo, e := fixed(str(input, "min_quantity"), 4)
	if e != nil || lo < 0 {
		return fail("起订数量须为非负数")
	}
	hi, e := fixed(str(input, "max_quantity"), 4)
	if e != nil || hi < 0 || (hi > 0 && hi < lo) {
		return fail("数量区间无效")
	}
	start, end := str(input, "start_date"), str(input, "end_date")
	if start != "" && !dateValid(start) || end != "" && !dateValid(end) || (start != "" && end != "" && start > end) {
		return fail("生效日期区间无效")
	}
	return a.write(func(tx *sql.Tx) error {
		if _, e = master(tx, number(input, "product_id"), "product", true); e != nil {
			return e
		}
		if kind == "customer" {
			if _, e = master(tx, number(input, "customer_id"), "customer", true); e != nil {
				return e
			}
		}
		if kind == "level" && str(input, "customer_level") == "" {
			return fail("请选择客户等级")
		}
		args := []any{name, kind, number(input, "product_id"), nullID(number(input, "customer_id")), str(input, "customer_level"), lo, hi, price, minimum, start, end, number(input, "priority"), flag(input, "active"), stamp()}
		if id == 0 {
			args = append(args, stamp())
			id, e = idOf(tx.Exec(`INSERT INTO price_rules(name,kind,product_id,customer_id,customer_level,min_quantity,max_quantity,price,min_price,start_date,end_date,priority,active,updated_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...))
		} else {
			args = append(args, id)
			_, e = tx.Exec(`UPDATE price_rules SET name=?,kind=?,product_id=?,customer_id=?,customer_level=?,min_quantity=?,max_quantity=?,price=?,min_price=?,start_date=?,end_date=?,priority=?,active=?,updated_at=? WHERE id=?`, args...)
		}
		if e != nil {
			return e
		}
		return audit(tx, u, ip, "保存价格规则", "price_rule", id, nil, input)
	})
}
func (a *App) PriceRules() ([]M, error) {
	rows, e := a.DB.Query(`SELECT r.id,r.name,r.kind,r.product_id,p.name,COALESCE(r.customer_id,0),COALESCE(c.name,''),r.customer_level,r.min_quantity,r.max_quantity,r.price,r.min_price,r.start_date,r.end_date,r.priority,r.active FROM price_rules r JOIN base_records p ON p.id=r.product_id LEFT JOIN base_records c ON c.id=r.customer_id ORDER BY r.active DESC,r.id DESC`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []M{}
	for rows.Next() {
		var id, pid, cid, lo, hi, price, minPrice, priority int64
		var name, kind, pn, cn, level, start, end string
		var active bool
		if e = rows.Scan(&id, &name, &kind, &pid, &pn, &cid, &cn, &level, &lo, &hi, &price, &minPrice, &start, &end, &priority, &active); e != nil {
			return nil, e
		}
		out = append(out, M{"id": id, "name": name, "kind": kind, "product_id": pid, "product_name": pn, "customer_id": cid, "customer_name": cn, "customer_level": level, "min_quantity": qty(lo), "max_quantity": qty(hi), "price": qty(price), "min_price": qty(minPrice), "start_date": start, "end_date": end, "priority": priority, "active": active})
	}
	return out, rows.Err()
}

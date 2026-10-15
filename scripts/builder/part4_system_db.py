# -*- coding: utf-8 -*-
"""
part4_system_db.py: 构建第十三至十五章（系统安全与RBAC、数据库表结构全景字典、单据状态机深度设计）
"""

from .common import add_h1, add_h2, add_h3, add_p, add_bullet, add_table, add_callout, add_code_block

def build_part4(doc):
    # ==========================================
    # 十三、系统权限、安全管理与审计日志深度设计
    # ==========================================
    add_h1(doc, "十三、系统权限、安全管理与审计日志深度设计")

    add_h2(doc, "13.1 增强型 RBAC 角色与权限体系架构")
    add_p(doc, "系统在 `users` 表与 `roles` 表中构建了细粒度、多层次的访问控制模型：")
    add_bullet(doc, "支持通配符 `*`（超级管理员所有权限）以及细分到具体模块的树状权限标识符。例如：`dashboard:view`（查看工作台）、`masters:product:edit`（修改商品）、`purchase:order:approve`（审核采购订单）、`finance:writeoff`（核销应收应付）、`reports:cost:view`（查看敏感成本价与毛利）。", bold_prefix="模块与操作级权限（permissions）：")
    add_bullet(doc, "支持 `data_scope` 属性配置。若设为 `all`，用户可查阅全公司所有业务员的单据；若设为 `self`，用户仅能查看与操作自己（created_by = 当前用户 ID）开具的业务单据，实现销售员之间的客户与单据严密互斥隔离。", bold_prefix="数据可见性范围隔离（data_scope）：")
    add_bullet(doc, "支持在角色中绑定其有权访问的仓库白名单 ID 列表（JSON 格式）。仓管员仅能在其被授权的仓库内进行出入库与盘点操作，防止跨仓库越权发货。", bold_prefix="仓储实体权限约束（warehouse_ids）：")
    add_bullet(doc, "针对商品采购单价、库存结存单位成本、销售单笔毛利率等机密财务指标，施加专门的字段级脱敏保护。未被授予财务权限的操作员在浏览单据与报表时，成本与毛利字段以 `***` 掩码呈现，彻底杜绝商业机密外泄。", bold_prefix="敏感财务成本脱敏：")

    add_h2(doc, "13.2 接口幂等控制引擎（Idempotency Engine）")
    add_p(doc, "在网络抖动或客户端频繁快速点击场景下，极易发生“重复扣减库存”、“重复核销应收款”或“重复生成单据”的严重隐患。系统在 `idempotency` 表中建立了服务端幂等性保护机制：")
    
    code_idempotency = """
    +------------------+                    +------------------+
    |   前端客户端     |                    |   后端 Gin 引擎  |
    +------------------+                    +------------------+
             |                                       |
             |  1. POST 请求携带 Header             |
             |     Idempotency-Key: <UUID>           |
             | ------------------------------------> |
             |                                       | 2. 查询 idempotency 表:
             |                                       |    SELECT * FROM idempotency 
             |                                       |    WHERE user_id=? AND key=?
             |                                       |
             |                                       +---------------+
             |                                       | 检查是否已处理|
             |                                       +---------------+
             |                                       |
             |  3. [情况 A: 已存在记录]             |
             |     直接返回已缓存的响应 JSON         |
             | <------------------------------------ | (不再执行任何业务事务)
             |                                       |
             |                                       | 4. [情况 B: 首次处理]
             |                                       |    开启业务事务执行开单/审核;
             |                                       |    保存执行结果至 idempotency 表;
             |  5. 返回执行成功响应                  |
             | <------------------------------------ |
"""
    add_code_block(doc, code_idempotency)

    add_h2(doc, "13.3 操作审计日志（operation_log）与安全留痕")
    add_p(doc, "系统在底层通过统一审计函数 `audit(q, user, ip, action, entity, entity_id, before, after)` 实施全生命周期操作审计：")
    add_bullet(doc, "操作人 ID（user_id）、用户名（username）、客户端真实 IP 地址（ip）、操作类型（action，如 create, update, approve, reverse, delete）、业务实体类型（entity，如 document, base_record, price_rule）、实体 ID、变更前数据快照（before_data JSON）、变更后数据快照（after_data JSON）、发生时间戳（created_at）。", bold_prefix="审计要素覆盖：")
    add_bullet(doc, "审计流水独立只读追加写入，严禁任何业务接口提供修改或物理删除操作日志的逻辑，确保系统具备金融级的反舞弊与事后法务举证追溯能力。", bold_prefix="不可篡改性保障：")

    # ==========================================
    # 十四、数据库核心实体模型与物理表结构字典
    # ==========================================
    add_h1(doc, "十四、数据库核心实体模型与物理表结构字典")

    add_h2(doc, "14.1 核心数据表结构清单")
    add_p(doc, "系统底层共包含 19 张核心关系数据表，各表在业务链路中承担明确的持久化职责：")

    schema_list_headers = ["序号", "物理表名 (Table Name)", "所属领域与功能定义", "主键与核心约束"]
    schema_list_data = [
        ["1", "settings", "全局参数与企业业务配置字典", "key (TEXT PK)"],
        ["2", "roles", "系统角色与权限策略定义表", "id (INTEGER PK AUTOINCREMENT), name UNIQUE"],
        ["3", "users", "系统用户账户与登录凭证表", "id (INTEGER PK), username UNIQUE, FK(role_id)"],
        ["4", "sessions", "用户登录态与 Token 会话生命周期表", "token_hash (TEXT PK), FK(user_id)"],
        ["5", "base_records", "统一基础主档表（商品/客户/供应商/仓库/字典）", "id (INTEGER PK), UNIQUE(kind, code)"],
        ["6", "document_sequences", "单据唯一编号高并发自增序列器", "PRIMARY KEY(prefix, day)"],
        ["7", "documents", "采购/销售/仓储通用业务单据统一主表", "id (INTEGER PK), number (TEXT UNIQUE), revision"],
        ["8", "document_items", "通用业务单据行项明细表", "id (INTEGER PK), FK(document_id), FK(product_id)"],
        ["9", "inventory", "四维库存实时结存与锁定余额汇总表", "id (INTEGER PK), UNIQUE(product_id,wh,loc,batch)"],
        ["10", "inventory_transactions", "绝对单向递增的不可逆库存记账流水表", "id (INTEGER PK), FK(document_id), FK(inventory_id)"],
        ["11", "cost_adjustments", "成本回补与负库存差异调账记录表", "id (INTEGER PK), FK(document_id), FK(item_id)"],
        ["12", "inventory_serials", "一物一码单件序列号全生命周期追踪表", "id (INTEGER PK), UNIQUE(product_id, serial)"],
        ["13", "stock_reservations", "销售订单未出库前的可用库存预占锁定表", "id (INTEGER PK), UNIQUE(item_id, inventory_id)"],
        ["14", "bills", "往来应收（AR）与应付（AP）原始账单主表", "id (INTEGER PK), document_id UNIQUE, side, partner_id"],
        ["15", "settlements", "资金收款单与付款单结算凭证主表", "id (INTEGER PK), document_id UNIQUE, side, partner_id"],
        ["16", "writeoffs", "结算单对账单的多对多资金核销明细表", "id (INTEGER PK), FK(settlement_id), FK(bill_id)"],
        ["17", "bill_offsets", "销售退货等正负账单之间的双向对冲记录表", "id (INTEGER PK), FK(positive_bill_id), FK(negative_bill)"],
        ["18", "settlement_offsets", "预付款与退款等正负资金结算单之间的对冲表", "id (INTEGER PK), FK(pos_settle_id), FK(neg_settle_id)"],
        ["19", "price_rules", "客户专属价/等级价/限时促销/阶梯价格决策规则表", "id (INTEGER PK), kind, FK(product_id), active"]
    ]
    add_table(doc, schema_list_headers, schema_list_data, [0.6, 2.0, 3.2, 1.7])

    add_h2(doc, "14.2 核心业务表物理字典精细定义")

    add_h3(doc, "14.2.1 业务单据主表 (documents)")
    add_p(doc, "记录所有进销存单据的核心业务头信息，支持多类型单据共用统一状态机与审计骨架：")
    doc_table_headers = ["字段名称", "字段类型", "允许为空", "默认值", "约束/索引", "业务含义与设计说明"]
    doc_table_data = [
        ["id", "INTEGER", "NO", "AUTO", "PRIMARY KEY", "单据自增唯一系统主键。"],
        ["number", "TEXT", "NO", "None", "UNIQUE", "对外展示单据编号（如 PO-20261015-0001），具备法律效力。"],
        ["kind", "TEXT", "NO", "None", "INDEX", "单据类型：purchase_order, purchase_receipt, sales_order, sales_delivery 等。"],
        ["status", "TEXT", "NO", "draft", "INDEX", "状态机状态：draft, pending, approved, completed, voided, reversed。"],
        ["partner_id", "INTEGER", "YES", "NULL", "FK(base_records)", "往来单位外键 ID（客户或供应商）。"],
        ["warehouse_id", "INTEGER", "YES", "NULL", "FK(base_records)", "主业务仓库外键 ID（发货仓、入库仓或调出仓）。"],
        ["target_warehouse_id", "INTEGER", "YES", "NULL", "FK(base_records)", "目标仓库外键 ID（用于调拨单调入仓）。"],
        ["source_id", "INTEGER", "YES", "NULL", "FK(documents)", "上游源单主键 ID，建立单据间树状上下游追溯链。"],
        ["date", "TEXT", "NO", "None", "INDEX", "业务发生日期（格式：YYYY-MM-DD）。"],
        ["due_date", "TEXT", "NO", "''", "None", "收付款结算到期日（格式：YYYY-MM-DD）。"],
        ["total", "INTEGER", "NO", "0", "None", "单据含税总金额（单位：整数分，Scale 100）。"],
        ["cost", "INTEGER", "NO", "0", "None", "单据结转总成本（单位：整数分，用于出库单核算毛利）。"],
        ["revision", "INTEGER", "NO", "1", "None", "乐观并发控制版本号，每次成功更新原子递增 1。"],
        ["reversal_id", "INTEGER", "YES", "NULL", "FK(documents)", "若发生红冲/冲销，指向生成的红冲单据 ID。"],
        ["created_by", "INTEGER", "NO", "None", "FK(users)", "单据创建人用户 ID。"],
        ["approved_by", "INTEGER", "YES", "NULL", "FK(users)", "审核人用户 ID。"],
        ["approved_at", "TEXT", "NO", "''", "None", "审核通过的 UTC 时间戳。"]
    ]
    add_table(doc, doc_table_headers, doc_table_data, [1.4, 1.0, 0.8, 0.8, 1.4, 2.1])

    add_h3(doc, "14.2.2 业务单据明细表 (document_items)")
    item_table_headers = ["字段名称", "字段类型", "允许为空", "默认值", "约束/索引", "业务含义与设计说明"]
    item_table_data = [
        ["id", "INTEGER", "NO", "AUTO", "PRIMARY KEY", "明细行项自增唯一主键。"],
        ["document_id", "INTEGER", "NO", "None", "FK(documents)", "所属单据主键 ID，级联索引检索。"],
        ["product_id", "INTEGER", "NO", "None", "FK(base_records)", "对应商品主档 ID。"],
        ["unit", "TEXT", "NO", "None", "None", "单据行所使用的计量单位。"],
        ["location_id", "INTEGER", "NO", "0", "None", "具体库位 ID。"],
        ["source_item_id", "INTEGER", "YES", "NULL", "FK(items)", "对应上游源单明细行 ID，用于精细追踪分批执行进度与成本。"],
        ["batch", "TEXT", "NO", "''", "None", "批次号字符串（如启用批次管理）。"],
        ["serials", "TEXT", "NO", "'[]'", "None", "序列号 JSON 数组（如启用一物一码）。"],
        ["quantity", "INTEGER", "NO", "None", "None", "业务数量（以万分之一为单位，Scale 10000）。"],
        ["price", "INTEGER", "NO", "None", "None", "成交含税单价（以万分之一为单位，Scale 10000）。"],
        ["amount", "INTEGER", "NO", "None", "None", "行项含税总金额（单位：整数分）。"],
        ["cost", "INTEGER", "NO", "0", "None", "行项结转总成本（单位：整数分）。"],
        ["price_source", "TEXT", "NO", "''", "None", "价格来源标记（如 customer_special, promo, base 等）。"]
    ]
    add_table(doc, item_table_headers, item_table_data, [1.4, 1.0, 0.8, 0.8, 1.4, 2.1])

    add_h3(doc, "14.2.3 物理四维库存结存表 (inventory)")
    inv_table_headers = ["字段名称", "字段类型", "允许为空", "默认值", "约束/索引", "业务含义与设计说明"]
    inv_table_data = [
        ["id", "INTEGER", "NO", "AUTO", "PRIMARY KEY", "四维库存记录唯一标识。"],
        ["product_id", "INTEGER", "NO", "None", "FK(base_records)", "商品主档外键。"],
        ["warehouse_id", "INTEGER", "NO", "None", "FK(base_records)", "所属仓库外键。"],
        ["location_id", "INTEGER", "NO", "0", "None", "所属库位外键。"],
        ["batch", "TEXT", "NO", "''", "None", "批次号。"],
        ["quantity", "INTEGER", "NO", "0", "None", "当前物理在库实际总数量（Scale 10000）。"],
        ["reserved", "INTEGER", "NO", "0", "None", "销售订单当前锁定预占数量（Scale 10000）。"],
        ["value", "INTEGER", "NO", "0", "None", "当前在库商品总资产金额（单位：整数分）。"],
        ["updated_at", "TEXT", "NO", "None", "None", "最后一次库存变动时间戳。"],
        ["UNIQUE 约束", "复合唯一", "NO", "None", "四维组合唯一", "UNIQUE(product_id, warehouse_id, location_id, batch)。"]
    ]
    add_table(doc, inv_table_headers, inv_table_data, [1.4, 1.0, 0.8, 0.8, 1.4, 2.1])

    # ==========================================
    # 十五、单据生命周期与通用状态机流转设计
    # ==========================================
    add_h1(doc, "十五、单据生命周期与通用状态机流转设计")

    add_h2(doc, "15.1 统一单据状态机跃迁图谱")
    add_p(doc, "所有核心进销存单据（采购单、销售单、入库单、出库单、调拨单、盘点单等）均受统一定义的严格状态机驱动：")
    
    code_state_machine = """
                +-------------------+
                |   草稿 (Draft)    | <-----------+ (驳回退回)
                +-------------------+             |
                          | (提交审核)            |
                          v                       |
                +-------------------+             |
                |  待审核 (Pending) | ------------+
                +-------------------+
                          | (审核入账 - BEGIN IMMEDIATE)
                          v
                +-------------------+
                |  已审核 (Approved)| ----------------------------+ (全部执行完毕)
                +-------------------+                             |
                          | (部分下游执行, 如部分到货)             |
                          v                                       v
                +-------------------+                   +-------------------+
                | 执行中 (InProgress)| ----------------> |  已完成 (Completed)|
                +-------------------+ (完成全部执行)    +-------------------+
                          |
                          | (受控纠错流程)
                          v
                +-------------------+
                | 已作废 / 已冲销   |
                | (Voided/Reversed) |
                +-------------------+
"""
    add_code_block(doc, code_state_machine)

    add_h2(doc, "15.2 状态跃迁控制与操作权限矩阵")
    state_headers = ["当前初始状态", "允许执行动作", "跃迁后目标状态", "前置业务校验条件与系统行为"]
    state_data = [
        ["草稿 (draft)", "保存/修改", "draft", "允许修改单据头与明细行，校验必填项与基本格式。"],
        ["草稿 (draft)", "提交审核", "pending", "校验单据行项非空、单价与数量有效，锁定草稿编辑。"],
        ["待审核 (pending)", "撤回/驳回", "draft", "将单据退回为草稿状态，恢复操作员修改权限。"],
        ["待审核 (pending)", "审核通过", "approved", "开启原子事务：校验可用库存、重算移动平均成本、锁定预占或扣减库存、生成往来账单、记录审计流水。"],
        ["已审核 (approved)", "部分下推", "in_progress", "当下游单据（如出库单）仅引用并执行了部分数量时自动跃迁。"],
        ["已审核 (approved)", "反审核 (撤回)", "draft", "严格校验：若下游已产生出库、入库或资金核销，强行阻断！仅在下游未动时回滚所有影响。"],
        ["执行中/已审核", "全额完成", "completed", "下游单据累计执行量达到 100%，上游订单自动归档闭环。"],
        ["任何未结状态", "作废/关闭", "voided", "释放销售订单预占库存，关闭单据后续流转。"]
    ]
    add_table(doc, state_headers, state_data, [1.4, 1.4, 1.4, 3.3])

    add_h2(doc, "15.3 乐观并发锁（Optimistic Locking）保障机制")
    add_p(doc, "在多操作员并发修改同一张单据时，为杜绝后提交的操作无声覆盖先提交的修改，系统在 `documents` 表中引入 `revision` 乐观锁机制：")
    add_bullet(doc, "前端获取单据详情时，必须一并读取当前的 `revision` 版本数值（如 revision = 3）；", bold_prefix="版本号传递：")
    add_bullet(doc, "提交更新时，后端执行条件更新语句：`UPDATE documents SET ..., revision = revision + 1 WHERE id = ? AND revision = ?`；", bold_prefix="原子递增：")
    add_bullet(doc, "若影响行数 RowsAffected 等于 0，说明该单据在被当前用户编辑期间已被其他同事修改并保存。系统立即回滚事务并返回 `409 CONFLICT: 单据已被他人修改，请刷新最新版本后重试`，彻底阻断脏数据覆盖。", bold_prefix="冲突熔断：")

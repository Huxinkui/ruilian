# -*- coding: utf-8 -*-
"""
part3_finance_price.py: 构建第九至十二章（往来资金、价格决策引擎、预警中心、报表中心深度设计）
"""

from .common import add_h1, add_h2, add_h3, add_p, add_bullet, add_table, add_callout, add_code_block

def build_part3(doc):
    # ==========================================
    # 九、往来与资金管理模块深度设计
    # ==========================================
    add_h1(doc, "九、往来与资金管理模块深度设计")

    add_h2(doc, "9.1 应收应付（AR/AP）双向对称数据模型")
    add_p(doc, "系统在底层采用高度优雅的对称结构统一管理客户应收（AR）与供应商应付（AP）：")
    add_bullet(doc, "记录由销售出库（side='ar'）或采购入库（side='ap'）产生的原始应收应付账单。字段包含：document_id（绑定业务单据）、partner_id（往来客户/供应商）、amount（账单原始总额）、settled（已核销金额）、due_date（款项到期日）、state（active 未结清 / settled 已结清 / cancelled 已作废）。", bold_prefix="业务账单表（bills）：")
    add_bullet(doc, "记录资金的真实流入与流出。客户收款单（side='ar'）与供应商付款单（side='ap'）。字段包含：document_id、partner_id、amount（实收/实付金额）、allocated（已分配核销金额）、state。", bold_prefix="资金结算单表（settlements）：")
    add_bullet(doc, "记录结算单与账单之间的多对多核销关系。字段包含：settlement_id、bill_id、amount（本次核销金额）、created_by、reversed（是否已反审核撤销）。", bold_prefix="资金核销关系表（writeoffs）：")

    add_h2(doc, "9.2 资金收付与自动核销（FIFO Auto-Writeoff）算法")
    add_p(doc, "资金核销不仅支持财务人员手动在界面上跨单据、多账单精准勾选分配，更内置了业界领先的高效自动核销引擎：")
    
    code_writeoff_algo = """
    【先进先出自动核销算法流程】：
    输入：往来单位 ID (partner_id), 业务方向 (side: ar/ap), 本次实收/实付金额 (SettlementAmount)
    1. 开启数据库排他事务 BEGIN IMMEDIATE;
    2. 查询该往来单位所有未结清账单：
       SELECT * FROM bills WHERE partner_id=? AND side=? AND state='active' 
       ORDER BY due_date ASC, id ASC;
    3. 剩余待核销金额 Remainder = SettlementAmount;
    4. FOR EACH bill IN bills:
           IF Remainder <= 0 THEN BREAK;
           未结金额 Unsettled = bill.amount - bill.settled;
           本次冲销 Apply = MIN(Remainder, Unsettled);
           // 写入核销流水
           INSERT INTO writeoffs(settlement_id, bill_id, amount, created_by, created_at)
           VALUES(..., Apply, ...);
           // 更新账单已核销金额
           bill.settled += Apply;
           IF bill.settled >= bill.amount THEN bill.state = 'settled';
           Remainder -= Apply;
    5. 结算单记录已分配金额：settlement.allocated = SettlementAmount - Remainder;
    6. IF Remainder > 0 THEN:
           // 超额款项自动沉淀为该单位的「预收款」或「预付款」余额
           系统将 Remainder 保留在当前结算单中，待后续新单据产生时直接点击核销;
    7. 提交事务 COMMIT;
"""
    add_code_block(doc, code_writeoff_algo)

    add_h2(doc, "9.3 贷项冲抵与双向对冲（Offsetting）机制")
    add_p(doc, "在实际商贸活动中，退货、折扣或对账差额常常产生负账单或退款单，系统提供完善的免资金流动对冲机制：")
    add_bullet(doc, "销售退货产生的应收负账单，可直接与该客户的历史正向未结应收账单在 `bill_offsets` 表中对冲冲平，无需先退款再收款。", bold_prefix="账单间对冲（bill_offsets）：")
    add_bullet(doc, "客户之前支付的预收款或退款结算单，可与后续收款单在 `settlement_offsets` 表中相互对冲冲销。", bold_prefix="结算单间对冲（settlement_offsets）：")
    add_bullet(doc, "若核销操作存在人为录入失误，系统支持「撤销核销」（Un-writeoff）。系统将 `writeoffs.reversed` 标志置为 1，原子回退账单的 `settled` 金额与结算单的 `allocated` 金额，恢复账单为 `active` 状态，并完整保留撤销审计轨迹。", bold_prefix="反核销与错误冲正：")

    add_h2(doc, "9.4 客户信用额度与账期强控制模型")
    add_p(doc, "为有效防范客户拖欠货款与坏账风险，系统在销售开单与审核全流程中嵌入信用风险敞口强校验：")
    
    code_credit_formula = """
    【客户当前信用敞口计算公式】：
    当前敞口风险值 (RiskExposure) = 历史未结清应收账单总额 (UnsettledBills)
                                 + 审核中且尚未出库的销售订单总金额 (PendingOrders)
                                 - 客户沉淀的可用预收账款余额 (AdvanceReceipts)

    【开单与审核拦截规则】：
    IF 客户设置了信用额度 (CreditLimit > 0):
        IF RiskExposure + 本次单据金额 > CreditLimit:
            拦截报错: HTTP 400 VALIDATION: 客户信用额度超限 (当前敞口已达上限)，禁止开单/审核！
    IF 客户存在严重逾期账款 (OverdueDays > AllowToleranceDays):
        拦截报错: HTTP 400 VALIDATION: 该客户存在超期未付应收账单，系统已锁定赊销权限！
"""
    add_code_block(doc, code_credit_formula)

    add_h2(doc, "9.5 应收账龄分析多维区间模型")
    add_p(doc, "系统依据应收账单的实际到期日（due_date）与当前日期的差值，构建精确到日的账龄分析矩阵：")
    
    aging_headers = ["账龄区间", "逾期判定标准", "风险等级", "系统管控与催收建议"]
    aging_data = [
        ["正常账期内", "当前日期 <= 到期日（剩余账期天数 >= 0）", "正常安全", "正常业务跟进，可继续享受常规授信额度。"],
        ["逾期 1~30 天", "到期日 < 当前日期 <= 到期日 + 30 天", "一级关注", "轻度逾期，系统在工作台提示预警，业务员跟进日常对账催收。"],
        ["逾期 31~60 天", "到期日 + 31 天 <= 当前日期 <= 到期日 + 60 天", "二级预警", "中度逾期，系统收紧新增赊销额度，开单需主管审批。"],
        ["逾期 61~90 天", "到期日 + 61 天 <= 当前日期 <= 到期日 + 90 天", "三级高危", "严重逾期，系统强制冻结该客户所有赊销权限，仅支持全款现销。"],
        ["逾期 90 天以上", "当前日期 > 到期日 + 90 天", "四级呆坏账", "极度高危，系统自动上报预警中心，移交法务与专人清收处理。"]
    ]
    add_table(doc, aging_headers, aging_data, [1.4, 2.6, 1.2, 2.8])

    # ==========================================
    # 十、价格决策与折扣引擎模块深度设计
    # ==========================================
    add_h1(doc, "十、价格决策与折扣引擎模块深度设计")

    add_h2(doc, "10.1 六级价格决策优先级金字塔")
    add_p(doc, "系统在 `price_rules` 表与基础主档中构建了一套灵活强大的动态价格决策引擎。在销售或采购录入商品明细时，系统按照严格的优先级顺序从高到低逐级匹配，一旦命中立即终止后续查找：")

    price_headers = ["优先级层级", "规则类型 (kind)", "判定条件与匹配维度", "业务应用场景"]
    price_data = [
        ["Level 1 (最高)", "客户专属约定价", "product_id + customer_id 强匹配，且在生效期内", "核心战略大客户合同一口价、框架协议约定采购价格。"],
        ["Level 2", "客户等级价格", "product_id + customer_level（如 VIP1/经销商/批发商）", "不同渠道分销等级的标准化差异定价体系。"],
        ["Level 3", "限时促销特价", "start_date <= 当前业务日期 <= end_date，且 active=1", "节假日促销、季末清仓特惠、厂商联动降价活动。"],
        ["Level 4", "数量阶梯价格", "min_quantity <= 本次购买数量 <= max_quantity", "支持“买得越多单价越低”的批量大宗批发折扣。"],
        ["Level 5", "基础销售售价", "取自商品主档 base_records 中的 base_price", "商品常规标准销售牌价。"],
        ["Level 6 (保底)", "零售指导售价", "取自商品主档 base_records 中的 guide_price", "官方零售参考价，无其他规则时的最终兜底价格。"]
    ]
    add_table(doc, price_headers, price_data, [1.4, 1.6, 2.6, 2.4])

    add_h2(doc, "10.2 底线价格保护（Min Price Guard）")
    add_p(doc, "为防范前线业务人员为了冲刺个人业绩私自大幅让利、或因失误录入超低价格导致企业巨额亏损，系统设置了不可突破的底线限价防线：")
    add_bullet(doc, "在商品主档或价格规则中设定 `min_price`（最低销售限价，以分为单位）。", bold_prefix="底线配置：")
    add_bullet(doc, "在销售出库单录入或修改时，不论业务员输入何种折扣率或特批价格，折后实际成交单价 `item.price` 必须满足：`item.price >= item.min_price`。若低于底线，系统在审核时直接强行报错拦截，杜绝低于成本线甩卖的合规漏洞。", bold_prefix="刚性拦截：")

    add_h2(doc, "10.3 客户端即时试算与服务端防篡改安全校验")
    add_p(doc, "系统在价格交互设计上兼顾了「极致的操作流畅度」与「绝对的后端安全性」：")
    add_bullet(doc, "操作员在前端选择客户与商品并修改数量后，前端异步调用 `/api/pricing/calculate` 接口。后端定价引擎即时返回推荐价格、命中的价格规则名称与折扣信息，前端界面实现无刷新即时自动填充。", bold_prefix="前端即时试算体验：")
    add_bullet(doc, "前端提交的单据数据绝不作为后端记账的唯一信任源。在保存和审核单据时，后端独立重跑定价引擎与校验规则，严密防范通过 Postman、爬虫或改包工具篡改单价的行为。若发现提交价格违背最低限价，后端直接阻断并记录安全告警日志。", bold_prefix="后端防篡改强校验：")

    # ==========================================
    # 十一、预警中心与智能监控模块深度设计
    # ==========================================
    add_h1(doc, "十一、预警中心与智能监控模块深度设计")

    add_h2(doc, "11.1 业务预警六大雷达矩阵")
    add_p(doc, "系统在后台运行实时与定时智能巡检分析服务，构建覆盖仓储、资金与交易全领域的六大预警雷达：")

    warn_headers = ["预警雷达维度", "触发阈值判定条件", "风险危害分析", "闭环处置动作"]
    warn_data = [
        ["1. 库存短缺预警", "四维库存可用量 <= 商品设定的最低安全库存 min_stock", "缺货导致订单无法及时履约交付，造成客户流失。", "一键下推生成采购建议订单或跨仓调拨申请。"],
        ["2. 库存超限积压", "四维库存现存量 >= 商品设定的最高库存 max_stock", "资金被过度占用，仓库仓储成本激增，贬值风险高。", "触发降价促销、放缓后续采购入库节奏。"],
        ["3. 呆滞物料雷达", "商品在库超过 N 天（默认 90 天）无任何出库销售流水", "物料陈旧过期报废，导致企业账面资产减值坏账。", "系统提示折价处理、返厂换货或捆绑促销组合。"],
        ["4. 客户信用超额", "客户当前风险敞口金额 > 客户授信额度 credit_limit", "客户赊销资金敞口过大，可能诱发系统性坏账逾期。", "强制阻断新增销售赊销审核，仅支持现款现货。"],
        ["5. 往来账款逾期", "当前日期 > 账单到期日 due_date，且账单仍未结清", "企业应收现金流受阻，账龄拉长导致呆账风险递增。", "触发财务账龄催收通知，扣减业务员当期提成考核。"],
        ["6. 售价底线异常", "销售成交单价低于设定的最低指导售价 min_price", "销售利润被恶意侵蚀，价格体系崩溃引发窜货内讧。", "审核时系统直接报错拦截，需总监级特批权限。"]
    ]
    add_table(doc, warn_headers, warn_data, [1.4, 2.4, 2.0, 2.2])

    add_h2(doc, "11.2 预警确认（warning_ack）管理闭环")
    add_p(doc, "系统不仅提供报警展示，更建立了严肃的预警知悉与消警闭环流程：")
    add_bullet(doc, "管理人员在预警中心查看明细后，可针对特定预警项点击「确认知悉」。系统在 `warning_ack` 表中写入 `warning_key`、操作员 `created_by` 与当前时间戳，此预警在当前工作日不再频繁打扰推送。", bold_prefix="知悉确认留痕：")
    add_bullet(doc, "当仓库实际完成补货、或财务实际收回应收款项使得指标恢复正常范围后，系统在下一次巡检时自动清除该预警记录，实现从预警产生、人为介入到业务消除的全生命周期自闭环。", bold_prefix="自动消警闭环：")

    # ==========================================
    # 十二、报表中心与经营分析模块深度设计
    # ==========================================
    add_h1(doc, "十二、报表中心与经营分析模块深度设计")

    add_h2(doc, "12.1 经营工作台核心 KPI 口径与公式定义")
    add_p(doc, "工作台首页展示的核心财务与经营指标定义严密，数据口径严格遵循会计电算化规范：")

    kpi_headers = ["经营 KPI 指标", "数学计算公式", "数据统计源表", "管理学意义与监控目标"]
    kpi_data = [
        ["1. 净销售额 (Net Sales)", "销售出库单总额 - 销售退货单总额", "documents (kind=sales_delivery/return, status=approved)", "衡量当期真实有效主营业务销售规模，剔除退货虚增。"],
        ["2. 待收客户款 (Receivables)", "SUM(bills.amount - bills.settled) [side='ar', state='active']", "bills (side='ar', state='active')", "反映企业目前沉淀在客户手中的待回笼流动资金规模。"],
        ["3. 库存总资产 (Inventory)", "SUM(inventory.quantity * inventory.value / quantity)", "inventory (quantity > 0)", "反映当前所有仓库现有商品的物理账面结存资产总值。"],
        ["4. 销售毛利润 (Gross Profit)", "净销售额 - 销售商品出库总成本 (cost)", "documents & document_items", "企业核心盈利能力指标，出库成本精确结转扣减。"],
        ["5. 资金回款率 (Collection Rate)", "(当期客户净收款总额 / 当期净销售额) * 100%", "settlements / documents", "衡量企业销售转化为现金流的效率，防范虚假繁荣。"],
        ["6. 库存周转率 (Turnover Rate)", "当期销售出库总成本 / 期初期末平均库存资产", "document_items / inventory", "反映企业存货的流动性与运营周转效能，越高越健康。"]
    ]
    add_table(doc, kpi_headers, kpi_data, [1.5, 2.5, 1.8, 2.2])

    add_h2(doc, "12.2 核心业务报表矩阵")
    add_p(doc, "报表中心提供穿透至原始单据明细的多维交叉分析能力：")
    add_bullet(doc, "按日、周、月维度聚合展示采购进货额、销售出库额、退货额与毛利走势，辅助管理层判断季节性周期波动。", bold_prefix="采购与销售趋势报表：")
    add_bullet(doc, "Top 20 热销商品排行（按销售额、销量、贡献毛利排序），精准识别“明星商品”与“亏损商品”。", bold_prefix="商品排行分析报表：")
    add_bullet(doc, "核心大客户采购额与回款排行、主力供应商供货比重排行，为商务谈判与账期优惠提供精准数据背书。", bold_prefix="往来单位贡献报表：")
    add_bullet(doc, "统计各业务员的名下开单额、实际回款额、退货率与毛利创收，为提成核算与绩效考核提供客观凭证。", bold_prefix="业务员业绩报表：")
    add_bullet(doc, "按“期初结存 + 本期入库 - 本期出库 = 期末结存”标准会计平衡恒等式，按商品和仓库逐笔展开变动流水，供审计与年终盘点对账。", bold_prefix="进销存台账明细表：")

    add_h2(doc, "12.3 报表聚合计算模型与索引优化策略")
    add_p(doc, "为确保在海量历史单据（数十万至数百万行）下报表依然实现秒级响应，系统在数据库层实施了针对性的组合索引覆盖：")
    add_bullet(doc, "覆盖单据类型、审核状态与日期的组合查询，极大加速工作台与月报的时间切片统计。", bold_prefix="`idx_docs_filter` ON documents(kind, status, date)：")
    add_bullet(doc, "覆盖往来账单的多条件状态过滤与到期日账龄切片，保证账龄表瞬间完成百万级账单聚合。", bold_prefix="`idx_bills_party` ON bills(side, partner_id, state, due_date)：")
    add_bullet(doc, "覆盖库存流水追溯查询，保障进销存台账按时间回溯重算时避免全表扫描。", bold_prefix="`idx_inv_tx_doc` & `idx_inv_tx_inv` ON inventory_transactions：")

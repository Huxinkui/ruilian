# -*- coding: utf-8 -*-
"""
part5_backup_license_tenant.py: 构建第十六至十八章（备份与容灾、软件授权安全、多租户架构与总控中枢深度设计）
"""

from .common import add_h1, add_h2, add_h3, add_p, add_bullet, add_table, add_callout, add_code_block

def build_part5(doc):
    # ==========================================
    # 十六、数据安全、自动快照与灾难恢复引擎设计
    # ==========================================
    add_h1(doc, "十六、数据安全、自动快照与灾难恢复引擎设计")

    add_h2(doc, "16.1 SQLite 在线一致性快照技术（VACUUM INTO）")
    add_p(doc, "对于正在高频读写的数据库，传统的直接复制文件（File Copy）会导致严重的脏读、数据截断以及 WAL 日志不匹配引起的数据库损坏。系统彻底摒弃文件级拷贝，采用 SQLite 原生提供的在线一致性原子快照引擎：")
    add_bullet(doc, "系统在后台执行 `VACUUM main INTO '<backup_file_path>'` 指令。该指令在数据库底层开启事务级只读一致性视图，将全部有效数据页紧凑整理并原子写入独立的备份物理文件中，整个过程不阻塞前端正常查询与业务录入。", bold_prefix="在线原子快照：")
    add_bullet(doc, "快照生成后，系统立即为备份文件计算 SHA-256 摘要哈希值，并在独立的只读连接中执行 `PRAGMA integrity_check` 与外键约束检查。只有自检 100% 通过的副本才会被标记为「可用快照」并计入 `backup_records` 表。", bold_prefix="快照自检与验真：")

    add_h2(doc, "16.2 多梯度备份保留策略（Retention Policy）")
    add_p(doc, "系统内置高密度的自动化定时备份守护协程，执行多梯度的保留归档策略：")

    backup_ret_headers = ["备份梯队", "执行频次与触发时机", "保留副本数量", "灾难防护目标"]
    backup_ret_data = [
        ["高频运行快照", "系统运行期间每 15 分钟自动执行一次", "保留最近 192 份（覆盖 48 小时）", "应对突发断电、操作员严重误操作或数据误覆盖，实现超低 RPO（<=15分钟）。"],
        ["每日归档基线", "每日深夜业务低谷期自动归档一份全量基线", "保留最近 14 份（覆盖 14 天）", "支撑半个月内的按天级完整历史状态回溯与财务对账。"],
        ["周度历史快照", "每周日深夜执行周度归档并压缩", "保留最近 12 份（覆盖一季度 3 个月）", "支撑季度财务审计与阶段性经营成果归档。"],
        ["月度长周期归档", "每月最后一日深夜执行月度完整快照", "保留最近 12 份（覆盖全年 12 个月）", "满足企业财税法定审计与跨年度历史数据留存合规要求。"]
    ]
    add_table(doc, backup_ret_headers, backup_ret_data, [1.4, 2.3, 1.8, 2.0])

    add_h2(doc, "16.3 客户端原生一键灾难恢复协议")
    add_p(doc, "当遭遇硬盘逻辑坏道、勒索病毒攻击或极端人为误删时，系统在桌面客户端与 Web 运维中枢提供了一键式恢复协议：")
    
    code_restore_protocol = """
    +-----------------------------------------------------------------------------------+
    |                              客户端原生灾难恢复协议流程                           |
    +-----------------------------------------------------------------------------------+
    1. 操作员在客户端界面选择目标备份快照，输入超级管理员密码进行二次强鉴权;
    2. 系统安全停止当前正在运行的 Go 后端服务，并获取数据目录的排他独占文件锁 (File Lock);
    3. [自保机制] 将当前故障数据库 (ruilian.db, -wal, -shm) 完整拷贝至 pre_restore_backup 目录，
       确保即使恢复失败也能保全现场;
    4. 执行物理替换：将目标快照解压并原子重命名覆盖当前的 ruilian.db 文件，并清理陈旧 WAL/SHM;
    5. 重新载入底层数据库，执行 PRAGMA quick_check 验证物理结构完好性;
    6. 重新校验安装记录与授权状态 (system_installation & license_local);
    7. 恢复完成，向 operation_log 写入灾难恢复审计日志，客户端自动平滑重载回到工作台。
    +-----------------------------------------------------------------------------------+
"""
    add_code_block(doc, code_restore_protocol)

    # ==========================================
    # 十七、软件授权与防破解安全体系设计
    # ==========================================
    add_h1(doc, "十七、软件授权与防破解安全体系设计")

    add_h2(doc, "17.1 双模态授权架构设计")
    add_p(doc, "为满足商业化推广与客户私有化交付诉求，系统在底层构建了标准化的双模态授权流转体系：")
    add_bullet(doc, "系统在首次初始化安装时，在 `system_installation` 表中记录初始运行时间戳，自动开启 10 天（10×24 = 240 小时）的全功能无限制试用。登录页与系统顶部常驻试用倒计时横幅（TrialBanner），提醒用户剩余天数并提供一键授权入口。", bold_prefix="1. 10 天免费安全试用模式（Trial Mode）：")
    add_bullet(doc, "客户向软件发行方购买商业授权后，发行方使用独立的授权生成工具（licensegen）签署并下发激活凭证。客户在系统后台导入激活码后，系统激活为永久商业授权状态，顶部试用条自动消失。", bold_prefix="2. 永久商业授权模式（Permanent Mode）：")

    add_h2(doc, "17.2 机器硬件指纹绑定（Installation ID）")
    add_p(doc, "为防范激活码被客户在多台未授权计算机上随意复制盗用，系统在客户端采集硬件特征生成唯一且不可逆的设备指纹：")
    add_bullet(doc, "主板序列号（Motherboard UUID） + CPU 唯一处理器序列号 + 主网卡 MAC 地址硬件特征。", bold_prefix="硬件特征采集项：")
    add_bullet(doc, "通过 SHA-256 慢哈希进行多次不可逆散列运算，生成标准 32 位机器安装标识符（如 `RL-8F2A-9C3D-E451-B206`），存入 `system_installation.installation_id`。", bold_prefix="不可伪造指纹：")

    add_h2(doc, "17.3 Ed25519 非对称数字签名与私钥绝对物理隔离")
    add_p(doc, "传统的对称加密（如 AES/DES）需要将解密密钥编译在客户端中，极易被逆向工程逆向提取。睿链在架构上实施严格的非对称现代密码学体系：")
    
    code_ed25519 = """
    +----------------------------------------+        +----------------------------------------+
    |       发行方环境 (Publisher Only)      |        |        客户运行环境 (Client App)       |
    |      [完全物理隔离，绝不进入代码库]    |        |                                        |
    +----------------------------------------+        +----------------------------------------+
                        |                                                  |
     1. 生成 256 位 Ed25519 私钥/公钥对;                                   |
        - 私钥: 存放于发行方离线高密保险箱;                                |
        - 公钥: 硬编码内嵌于客户端 LicenseService; ------------------------>+ [内置验签公钥]
                        |                                                  |
     2. 发行方授权工具 (licensegen) 组装 Payload:                          |
        {                                                                  |
           "customer": "北京某商贸有限公司",                                |
           "installation_id": "RL-8F2A-...",                              |
           "product": "RUILIAN_ERP",                                       |
           "version_range": "1.0~2.0",                                     |
           "expire_at": "9999-12-31"                                       |
        }                                                                  |
                        |                                                  |
     3. 使用离线私钥对 Payload 进行 Ed25519 数字签名:                      |
        Signature = Ed25519Sign(PrivateKey, Payload)                       |
                        |                                                  |
     4. 生成授权许可证文件 / 激活码字符串                                   |
                        |                                                  |
                        +---------------- 发送给客户 --------------------->+
                                                                           |
                                                     5. 客户端执行非对称验签:
                                                        Valid = Ed25519Verify(PublicKey, Payload, Sig)
                                                        IF Valid && Payload.installation_id == LocalID:
                                                            激活成功！写入 license_local 表;
"""
    add_code_block(doc, code_ed25519)

    add_h2(doc, "17.4 防系统时间恶意篡改与倒退攻击（Anti-Clock Tampering）")
    add_p(doc, "试用期软件极易遭受恶意用户通过“将计算机系统时钟调回过去”来无限延长试用期。系统设计了单向递增防篡改封印机制：")
    add_bullet(doc, "系统在每次运行时，以 UTC 时间戳更新 `system_installation.last_run`，并结合内部混淆盐值计算 HMAC-SHA256 签名（seal 字段）。", bold_prefix="运行时间水印：")
    add_bullet(doc, "每次系统启动或执行核心写操作时，校验当前系统时间 $T_{now}$ 是否小于记录的 $T_{last}$。若发现时钟倒退超过 60 秒的合理误差容限，系统立即判定为恶意篡改攻击，锁定系统并在 `license_event_log` 中记录篡改报警，阻断试用期倒退。", bold_prefix="倒退熔断拦截：")

    add_h2(doc, "17.5 授权到期降级安全底线")
    add_p(doc, "当试用期届满（> 10 天）且用户未输入合法激活码时，系统恪守商业软件的安全伦理底线：")
    add_callout(doc, 
                "授权到期行为规范：\n"
                "1. 写操作全面拦截：严格拦截所有新增采购、入库、销售、出库、调拨、收付款等非幂等写操作，提示「试用期已结束，请购买正版授权」；\n"
                "2. 数据资产永不扣押：系统永久保留客户已有历史数据的只读查询、历史单据查看、财务报表导出为 Excel 以及完整数据库一键备份功能；\n"
                "3. 绝对禁止以任何形式损坏、格式化或恶意加密客户已有经营数据。",
                title="商业授权伦理与安全底线")

    # ==========================================
    # 十八、SaaS 多租户物理隔离架构与超级管理员中枢设计
    # ==========================================
    add_h1(doc, "十八、SaaS 多租户物理隔离架构与超级管理员中枢设计")

    add_h2(doc, "18.1 Database-per-Tenant 物理数据库独立架构")
    add_p(doc, "在集团多组织与 SaaS 云化架构下，传统的「共享库表、以 tenant_id 字段过滤」的逻辑隔离方案存在巨大的安全隐患——一旦研发人员在某处手写 SQL 遗漏了 `WHERE tenant_id = ?`，将直接导致不可挽回的跨企业数据穿透与商业机密泄漏。系统坚决推行 Database-per-Tenant 物理级强隔离架构：")

    tenant_dir_code = """
    data/
    |-- ruilian.db                 # [平台主管理库] 仅超级管理员 huxinkui 与全局总控使用
    |-- ruilian.db-wal             # 主管理库预写日志 (WAL 模式)
    |-- ruilian.db-shm             # 主管理库共享内存
    |-- backups/                   # 全局与主空间备份归档目录
    |-- logs/                      # 系统运行日志与安全审计日志
    `-- tenants/                   # [租户独立沙箱根目录]
        |-- xuyonghong/            # 租户专属物理目录 (以唯一租户代码 tenant_code 命名)
        |   |-- ruilian.db         # 该租户专属的独立 SQLite 物理数据库
        |   |-- ruilian.db-wal     # 租户独立 WAL 日志文件
        |   `-- ruilian.db-shm     # 租户独立共享内存文件
        |-- corp_bj/               # 分支机构 A 独立沙箱
        |   `-- ruilian.db
        `-- corp_sh/               # 分支机构 B 独立沙箱
            `-- ruilian.db
"""
    add_code_block(doc, tenant_dir_code)

    add_h2(doc, "18.2 动态租户路由中间件（Tenant Routing Middleware）")
    add_p(doc, "系统在后端通过 `TenantManager` 维护租户 `App` 实例的动态缓存池，实现零感知动态请求分发：")
    add_bullet(doc, "分发规则按优先级识别：HTTP Header `X-Tenant-Code` > Cookie `ruilian_tenant` > URL Query 参数 `?tenant=xxx`。若未指定或为 default，则分发至平台主空间。", bold_prefix="多通道识别：")
    add_bullet(doc, "TenantManager 内部采用读写锁（sync.RWMutex）保护 App 实例池。当首次请求某个租户时，按需懒加载并初始化该租户的独立物理数据库连接池，保证高并发线程安全。", bold_prefix="并发安全实例池：")

    add_h2(doc, "18.3 物理存储配额与写操作拦截引擎（Storage Quota Engine）")
    add_p(doc, "在多租户平台环境下，单个租户的恶意写入或数据急剧膨胀极易耗尽服务器整机磁盘，拖垮其他正常企业。系统在底层实现毫秒级物理穿透统计与写拦截：")
    add_bullet(doc, "租户注册主表中定义 `max_storage_mb` 字段（支持 200MB、500MB、1GB、2GB 或 0 不限容量）。", bold_prefix="配额定义：")
    add_bullet(doc, "系统通过 `GetTenantStorageUsage` 穿透物理文件系统，递归累加租户沙箱目录下主库、WAL 日志、SHM 共享内存及日志文件的真实物理字节总和。", bold_prefix="穿透统计：")
    add_bullet(doc, "在所有非幂等写操作（POST, PUT, DELETE, PATCH）到达业务层之前，中间件执行 `CheckStorageQuota`。一旦发现实际占用 >= 配额上限，即刻阻断请求并返回 `403 STORAGE_QUOTA_EXCEEDED`，同时将租户降级为只读模式，有力保护宿主机硬件安全。", bold_prefix="毫秒级写拦截：")

    add_h2(doc, "18.4 默认管理员账号注销与超级管理员专属化（huxinkui）")
    add_p(doc, "针对公网与局域网长期遭受针对通用用户名（admin/administrator）的大规模自动化爬虫扫描与暴力破解风险，系统在 V1.2 中实施了坚决的安全重构：")

    admin_headers = ["安全维度", "旧版机制 (V1.0/V1.1)", "新版架构标准 (V1.2)", "安全提升与防御价值"]
    admin_data = [
        ["超级管理员用户名", "默认通用 admin", "专属安全标识 huxinkui", "彻底抹平互联网扫描工具与弱密码字典的探测入口。"],
        ["默认 admin 账号行为", "允许登录，弱口令高危", "全面注销废弃，登录一律直接报错阻断", "消除默认账户暴露面，杜绝撞库扫描风险。"],
        ["租户空间账号规范", "租户库中曾残留 admin", "强制清退，严禁 admin/huxinkui 存在", "各租户仅保留与其代码同名的管理员（如 xuyonghong）。"],
        ["防暴力破解机制", "仅依赖基础单向哈希", "IPLoginLimiter 针对 IP 频次统计封禁", "单 IP 连续失败 8 次自动封禁 15 分钟，阻断自动化脚本。"],
        ["图形安全验证码", "无验证码", "CaptchaStore 字符随机扰动与干扰线", "内存单次消费机制，抵御 OCR 识别与接口自动化撞库。"]
    ]
    add_table(doc, admin_headers, admin_data, [1.3, 1.8, 2.3, 2.1])

    add_h2(doc, "18.5 权限边界与多租户管理中枢强隔离")
    add_p(doc, "为保证普通租户专注于自身业务，严防垂直越权与横向越权，系统构筑了前后端一体的双重防护城墙：")
    add_bullet(doc, "普通租户登录后，侧边栏导航自动剔除「多租户管理」菜单项；顶部多租户切换器对租户完全不渲染，租户感知不到平台其他企业的存在。", bold_prefix="前端界面完全隐形：")
    add_bullet(doc, "Vue Router 全局前置守卫拦截对 `/tenants` 的一切访问，一旦识别非超级管理员身份，立即弹出警告通知并重定向回业务首页。", bold_prefix="前端路由全天候强守卫：")
    add_bullet(doc, "后端 `/api/system/tenants` 接口全量挂载 `requireMasterAdmin` 中间件，强制校验 `(u.IsMasterAdmin || u.Username == 'huxinkui') && a.TenantCode == 'default'`。租户尝试直连请求一律返回 `403 Forbidden`，彻底杜绝越权操作。", bold_prefix="后端 API 强鉴权中间件：")

    add_h2(doc, "18.6 超级管理员专属平台总控运维控制台（Admin Dashboard）")
    add_p(doc, "系统在前端为不同身份提供了清晰的「视界分离」体验：")
    add_bullet(doc, "展示平台运维 Hero 中枢、超级管理员专属标识、实时脉冲健康呼吸灯、四维运维 KPI 网格（租户总数、物理存储占用、容量预警租户、核心防御在线状态）、ECharts 租户物理体积占用比例饼环图。", bold_prefix="超级管理员视界（Admin Dashboard）：")
    add_bullet(doc, "呈现完整的租户空间管控舰队（Tenant Fleet）。支持按租户代码、名称搜索与状态过滤；以动态颜色进度条透视物理存储占用与路径（`data/tenants/{code}/ruilian.db`）；提供一键跨空间巡检、动态调整配额、重置租户管理员密码、启停租户状态等运维操作。", bold_prefix="租户空间管控舰队：")
    add_bullet(doc, "超级管理员在总控台点击「跨空间巡检」后，无缝进入任意子租户的业务系统，页面顶部常驻红色监管横幅：“`跨租户监管中: <租户名> [代码]`”并附带“`返回平台总控台`”按钮，实现跨空间监管与平台中枢的顺畅穿透与回跳。", bold_prefix="跨空间监管巡检联动：")

# 睿链进销存与往来管理系统 - 租户端功能全景与 REST API 开发规范指南
> 适用对象：安卓移动端（Android App）工程师、工业 PDA 客户端开发者、UI/UX 设计师、后端联调人员  
> 版本：V2.0-Enterprise  
> 归属项目：睿链进销存 (RUILIAN_ERP) 多租户企业级系统

---

## 目录
1. [规范背景与移动端设计原则](#1-规范背景与移动端设计原则)
2. [多租户架构与网络通信协议规范](#2-多租户架构与网络通信协议规范)
   - [2.1 租户空间逻辑与物理隔离](#21-租户空间逻辑与物理隔离)
   - [2.2 移动端网络通信头规范 (Headers)](#22-移动端网络通信头规范-headers)
   - [2.3 统一响应报文与错误处理规范](#23-统一响应报文与错误处理规范)
   - [2.4 移动端长效会话与设备管理](#24-移动端长效会话与设备管理)
3. [模块页面内容、截图参照与接口映射全集](#3-模块页面内容截图参照与接口映射全集)
   - [01 登录与空间认证 (Auth & Tenant Realm)](#01-登录与空间认证-auth--tenant-realm)
   - [02 经营工作台 (Operations Dashboard)](#02-经营工作台-operations-dashboard)
   - [03 基础资料中心 (Master Data Management)](#03-基础资料中心-master-data-management)
   - [04 采购管理 (Procurement Management)](#04-采购管理-procurement-management)
   - [05 销售管理 (Sales & Outbound Management)](#05-销售管理-sales--outbound-management)
   - [06 库存管理 (Inventory & Warehouse Management)](#06-库存管理-inventory--warehouse-management)
   - [07 往来资金管理 (Finance & AR/AP Settlements)](#07-往来资金管理-finance--arap-settlements)
   - [08 价格管理中心 (Pricing Strategies)](#08-价格管理中心-pricing-strategies)
   - [09 报表中心 (Business Intelligence & Reports)](#09-报表中心-business-intelligence--reports)
   - [10 预警中心 (Alerts & Monitoring)](#10-预警中心-alerts--monitoring)
   - [11 系统管理 (Tenant System Settings)](#11-系统管理-tenant-system-settings)
   - [12 弹窗与通用交互组件 (Dialogs & Components)](#12-弹窗与通用交互组件-dialogs--components)
4. [移动端专属高性能作业接口](#4-移动端专属高性能作业接口)
   - [4.1 条码/二维码毫秒级极速查品 (`/api/mobile/scan`)](#41-条码二维码毫秒级极速查品-apimobilescan)
   - [4.2 移动现场快速扫码盘点 (`/api/mobile/quick-inventory`)](#42-移动现场快速扫码盘点-apimobilequick-inventory)
   - [4.3 现场销售极速出库开单 (`/api/mobile/quick-order`)](#43-现场销售极速出库开单-apimobilequick-order)
   - [4.4 现场拍照与凭证上传 (`/api/mobile/upload`)](#44-现场拍照与凭证上传-apimobileupload)
   - [4.5 离线增量数据同步 (`/api/mobile/sync/*`)](#45-离线增量数据同步-apimobilesync)
5. [安卓客户端（Kotlin / Jetpack Compose）开发落地建议](#5-安卓客户端kotlin--jetpack-compose开发落地建议)
   - [5.1 推荐应用架构与技术栈](#51-推荐应用架构与技术栈)
   - [5.2 工业 PDA 红外扫头与通用扫码集成](#52-工业-pda-红外扫头与通用扫码集成)
   - [5.3 蓝牙便携小票机 / 送货单热敏打印](#53-蓝牙便携小票机--送货单热敏打印)
   - [5.4 离线离网开单与后台 WorkManager 补偿机制](#54-离线离网开单与后台-workmanager-补偿机制)

---

## 1. 规范背景与移动端设计原则

本规范为“睿链进销存与往来管理系统”的**租户端**原生安卓客户端（Android App）及工业 PDA 客户端的设计与工程研发提供标准依据。

在当前数字化仓储与移动销售场景下，仓管人员需要手持 PDA 在立体货架间完成**扫码出入库、货位调拨与极速盘点**；外勤业务员需要随时在客户现场**查价格、验库存、录入销售订单并随身蓝牙打印收据**；管理人员需要随时随地通过手机查看**实时资金流水与经营预警**。

因此，移动客户端设计需遵循以下核心原则：
1. **高频作业扫码优先**：商品选择、盘点输入、仓库移库全面支持红外激光/摄像头连续扫码，减少软键盘打字。
2. **离网可用与增量同步**：在冷库、地下仓库等弱网/断网环境下支持本地商品与客户数据秒查，单据支持离线缓存并在网络恢复时自动上传。
3. **超长免登与生物识别**：移动端原生接入 30 天超长会话机制，并结合 Android 指纹/面部识别快速解锁进入作业。
4. **强安全性与租户边界**：严格隔离各企业租户的数据沙箱，API 请求携带租户凭证与防重放幂等令牌（`Idempotency-Key`）。

---

## 2. 多租户架构与网络通信协议规范

### 2.1 租户空间逻辑与物理隔离
系统采用完全物理隔离与动态挂载的多租户架构：
- 每一个租户拥有专属的存储命名空间（例如 `xuyonghong`），其业务数据（商品、往来、单据、库存、流水）存放在独立的 SQLite 实例及文件目录中。
- 移动端每次请求必须通过 Header 或 URL 参数声明当前操作的租户代码（`X-Tenant-Code`）。服务端网关根据租户代码自动路由至租户上下文，绝无跨租户越权漏洞。

### 2.2 移动端网络通信头规范 (Headers)

所有由 Android 客户端发出的 HTTP 请求必须包含以下标准请求头：

| 请求头 (Header) | 必须 | 示例值 | 业务说明 |
| :--- | :---: | :--- | :--- |
| `Authorization` | 登录后必传 | `Bearer 64位十六进制Token` | 登录鉴权凭证 |
| `X-Session-Token` | 推荐 | `64位十六进制Token` | 备用会话凭证（与 Authorization 等效） |
| `X-Tenant-Code` | **是** | `xuyonghong` | 目标租户空间代码，确保路由正确 |
| `X-Platform` | **是** | `android` 或 `pda` | 声明客户端操作系统平台 |
| `X-Device-Id` | **是** | `8a3b2c1d-ef45-6789-abcd-0123456789ab` | 硬件唯一识别码（UUID / AndroidID），用于设备审计与会话绑定 |
| `Idempotency-Key` | 变动类接口必传 | `UUIDv4` 字符串 | 防重复提交令牌。对于 `POST/PUT/DELETE` 请求，网络超时重试时保持相同 Key，防止因网络抖动造成重复入库或重复扣款 |
| `Content-Type` | JSON请求必传 | `application/json` | 数据编码格式 |

### 2.3 统一响应报文与错误处理规范

服务端所有 API 均统一封装为 JSON 响应报文：

#### 成功响应格式 (HTTP 200)
```json
{
  "ok": true,
  "data": { ... }
}
```

#### 失败响应格式 (HTTP 4xx / 5xx)
```json
{
  "ok": false,
  "error": {
    "code": "VALIDATION",
    "message": "库存不足：商品【不锈钢紧固螺栓】在当前仓库仅剩 15 件，申请出库 20 件"
  }
}
```

#### 常见业务错误码表 (error.code)
- `VALIDATION`: 数据校验不通过（如负库存拦截、超信用额度阻断、格式错误）
- `FORBIDDEN`: 当前操作员角色缺少必要权限（如业务员尝试查看敏感进价）
- `UNAUTHENTICATED`: 会话已过期或无效，移动端应引导重新登录或触发静默刷新
- `NOT_FOUND`: 请求的单据或基础资料实体不存在
- `NETWORK`: 客户端网络连接超时或无法触达服务

#### 配额预警响应头 (`X-Quota-Warning`)
当租户的数据库与附件存储用量达到租户配额的 **85%** 时，服务端将在每个 API 响应头中注入：
```http
X-Quota-Warning: 87.5%
```
Android 端应在界面顶部弹出非阻塞横幅或轻量提示：“当前租户存储空间占用已达 87.5%，已接近配额上限，请注意归档历史数据或联系管理员扩容”。

### 2.4 移动端长效会话与设备管理
- **长效免登**：在调用登录接口 `POST /api/auth/login` 时传入 `client_type: "android"`，后端自动下发 **30 天有效期**的 Session Token。
- **设备绑定**：登录成功后，移动端应自动调用 `POST /api/mobile/device/register` 上报设备型号（如 `Zebra TC52`）、安卓系统版本与推送 Token，便于管理员进行手持终端资产审计与远程注销。

---

## 3. 模块页面内容、截图参照与接口映射全集

本章对应 `tenant_screenshots/` 目录中的全部 35 张高清功能截图，逐一提供界面字段、用户操作流转及对应后端 REST API 规范。

---

### 01 登录与空间认证 (Auth & Tenant Realm)

#### 界面截图参照
- 桌面端登录界面：`tenant_screenshots/01_登录与空间认证/01_01_租户登录界面_桌面端.png`
- 空间代码输入态：`tenant_screenshots/01_登录与空间认证/01_02_租户空间代码输入态.png`
- 移动端仿真视图：`tenant_screenshots/01_登录与空间认证/01_03_租户登录界面_移动端仿真.png`

#### 界面内容与字段清单
| 字段 / UI 元素 | 表现形式 | 说明与交互逻辑 |
| :--- | :--- | :--- |
| **租户空间代码** | 输入框 + 历史联想 + 扫码按钮 | 必填。如 `xuyonghong`。支持扫二维码自动填入，本地记住历史租户列表 |
| **用户账号** | 输入框 | 必填。租户专属账号（如 `xuyonghong` 或业务员子账号），禁止输入平台超管 `admin` |
| **登录密码** | 密码输入框（明密文切换） | 必填。操作员密码 |
| **图形验证码** | 4位字符输入框 + SVG 图片显示 | 必填。点击图片调用接口无刷新更新，连续输错 5 次将封禁 IP 30 分钟 |
| **记住租户与账号** | 复选框 Checkbox | 勾选后保存在 Android DataStore 中 |
| **生物识别快捷登录** | 按钮 / 自动弹出提示 | 若本地已保存 Token 且未过期，直接唤起指纹/人脸解锁进入工作台 |

#### 对应后端接口

##### 1. 获取图形验证码
- **方法与路径**: `GET /api/auth/captcha`
- **请求头**: 无需鉴权
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "captcha_id": "c9a8f21e-d47b-402a-9f5b-112233445566",
    "svg": "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"120\" height=\"40\">...</svg>"
  }
}
```

##### 2. 租户用户登录鉴权
- **方法与路径**: `POST /api/auth/login`
- **请求参数 (JSON Body)**:
```json
{
  "tenant_code": "xuyonghong",
  "username": "xuyonghong",
  "password": "Password123!",
  "captcha_id": "c9a8f21e-d47b-402a-9f5b-112233445566",
  "captcha_code": "7a8b",
  "client_type": "android",
  "platform": "android",
  "device_id": "8a3b2c1d-ef45-6789-abcd-0123456789ab"
}
```
- **成功响应示例**:
```json
{
  "ok": true,
  "data": {
    "token": "d7a8e5f321094ba8b615cc8912ef01923485abdf761042398571029348571029",
    "tenant_code": "xuyonghong",
    "expires_in": 2592000,
    "client_type": "android",
    "user": {
      "id": 1,
      "username": "xuyonghong",
      "name": "许永红",
      "role_id": 1,
      "role_name": "系统管理员",
      "permissions": ["*"],
      "warehouse_ids": [],
      "data_scope": "all",
      "is_master_admin": false
    }
  }
}
```

##### 3. 获取当前登录用户信息与权限
- **方法与路径**: `GET /api/me`
- **请求头**: `Authorization: Bearer <token>`, `X-Tenant-Code: xuyonghong`
- **作用**: 用于进入 App 后验证 Token 有效性并加载操作员的角色权限列表与数据权限范围。

##### 4. 退出登录
- **方法与路径**: `POST /api/auth/logout`

---

### 02 经营工作台 (Operations Dashboard)

#### 界面截图参照
- 全景概览：`tenant_screenshots/02_经营工作台/02_01_经营工作台_全景概览.png`
- 快捷开单菜单：`tenant_screenshots/02_经营工作台/02_02_快捷开单下拉菜单.png`
- 移动端竖屏布局：`tenant_screenshots/02_经营工作台/02_03_经营工作台_移动端视图.png`

#### 界面内容与字段清单
| 模块区 | 界面元素 | 说明 |
| :--- | :--- | :--- |
| **顶部状态栏** | 租户名称、当前仓库、在线状态、网络延时 | 显示当前所属企业名称与已授权仓库 |
| **核心指标卡片** | 1. 今日销售额 (`today_sales`)<br>2. 今日毛利 (`today_profit`)<br>3. 本月累计销售 (`month_sales`)<br>4. 应收账款余额 (`receivable_total`)<br>5. 实时库存资产总值 (`inventory_total`)<br>6. 现有在库 SKU 种数 (`active_skus`) | 移动端支持横向滑动或 2x3 网格卡片展示。数字支持根据角色金额权限自动遮蔽 |
| **30天经营趋势图** | 折线面积图 | 近 30 天每日销售额走势与采购金额对比 |
| **待处理待办流** | 列表项（单号、金额、提交人、时间） | 待审核采购入库单、待审核销售出库单、待审核收款单。支持直接点击展开审核 |
| **资产与往来占比** | 环形占比图 | 仓库库存金额占比、前五大欠款客户占比 |
| **快捷悬浮按钮 (FAB)** | 悬浮展开菜单 (SpeedDial) | 1. 快捷销售开单<br>2. 快捷采购入库<br>3. 扫码查品/查价<br>4. 快速盘点 |

#### 对应后端接口

##### 1. 获取全量经营仪表盘数据
- **方法与路径**: `GET /api/dashboard`
- **权限要求**: 需登录
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "kpis": {
      "today_sales": "15420.00",
      "today_profit": "3850.00",
      "month_sales": "426800.00",
      "receivable_total": "128600.00",
      "inventory_total": "586200.00",
      "active_skus": 142
    },
    "sales_trend": [
      {"date": "2026-10-01", "sales": "12400.00", "purchase": "8500.00"},
      {"date": "2026-10-15", "sales": "15420.00", "purchase": "0.00"}
    ],
    "pending_reviews": [
      {
        "id": 105,
        "number": "SD20261016001",
        "kind": "sales_delivery",
        "partner_name": "杭州西湖精工机械制造有限公司",
        "amount": "12500.00",
        "created_at": "2026-10-16T08:30:00Z"
      }
    ]
  }
}
```

##### 2. 移动端极速轻量驾驶舱
- **方法与路径**: `GET /api/mobile/dashboard`
- **说明**: 移动端专属聚合接口，剔除大体积历史走势点阵，优先返回小屏卡片指标与 10 条高优先级待办，首屏加载速度小于 100ms。

---

### 03 基础资料中心 (Master Data Management)

#### 界面截图参照
- 商品资料列表：`tenant_screenshots/03_基础资料/03_01_商品资料列表.png`
- 商品资料编辑：`tenant_screenshots/03_基础资料/03_02_商品资料_新增编辑表单.png`
- 客户资料列表：`tenant_screenshots/03_基础资料/03_03_客户资料列表.png`
- 客户资料编辑：`tenant_screenshots/03_基础资料/03_04_客户资料_新增编辑表单.png`
- 供应商资料列表：`tenant_screenshots/03_基础资料/03_05_供应商资料列表.png`
- 仓库资料列表：`tenant_screenshots/03_基础资料/03_06_仓库资料列表.png`
- 商品分类列表：`tenant_screenshots/03_基础资料/03_07_商品分类列表.png`
- 计量单位列表：`tenant_screenshots/03_基础资料/03_08_计量单位列表.png`

#### 界面内容与字段字典

##### A. 商品档案 (kind = `product`)
- `id`: 商品主键 ID (int64)
- `code`: 商品编码 (必填，唯一，如 `P1001`)
- `name`: 商品全称 (必填，如 `高精度数控合金铣刀`)
- `data.barcode`: 69码或内部条码（必填，用于扫码枪识别）
- `data.spec`: 规格型号（如 `D12*75L*4F 钨钢涂层`）
- `data.category`: 商品分类名称
- `data.unit`: 计量单位（如 `把`、`件`）
- `data.purchase_price`: 采购基准参考进价（受 `cost.view` 权限控制）
- `data.sales_price`: 基准销售单价
- `data.retail_price`: 零售限价
- `data.min_price`: 业务员最低授权售价（低于此价格开单将被系统拦截）
- `data.batch_tracking`: 是否启用批次管理 (bool)
- `data.shelf_life_days`: 保质期天数 (int)
- `data.min_stock`: 安全库存下限（低于触发报警）
- `data.max_stock`: 库存上限（高于触发积压报警）
- `active`: 启用/停用状态 (bool)

##### B. 客户档案 (kind = `customer`)
- `code`: 客户编码 (如 `C1001`)
- `name`: 客户单位全称 (如 `杭州西湖精工机械制造有限公司`)
- `data.contact`: 主要联系人姓名
- `data.phone`: 手机号（移动端支持点击一键拨号）
- `data.address`: 详细送货地址（移动端支持调用高德/腾讯地图导航）
- `data.credit_limit`: 信用额度金额（元，开单超出此额度且未收款时阻断）
- `data.credit_days`: 约定账期天数（如 `30` 天，用于计算到期日与账龄）
- `data.discount_bps`: 默认折扣基点 (100 bps = 1%)

##### C. 供应商档案 (kind = `supplier`)
- `code`, `name`, `data.contact`, `data.phone`, `data.bank_name`, `data.bank_account`, `data.credit_days`

##### D. 仓库档案 (kind = `warehouse`)
- `code`, `name`, `data.address`, `data.consignment`: 是否为委托代销仓 (bool)

#### 对应后端接口

##### 1. 查询基础资料列表
- **方法与路径**: `GET /api/masters/:kind`
  - `:kind` 取值：`product` / `customer` / `supplier` / `warehouse` / `category` / `unit` / `employee` / `location`
- **Query 参数**:
  - `search`: 模糊搜索关键词（匹配编码、名称、条码、手机号）
  - `active`: 布尔值，传 `true` 时仅返回有效资料
- **权限标识**: `base.view`
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "id": 1,
        "kind": "product",
        "code": "P1001",
        "name": "高精度数控合金铣刀",
        "data": {
          "barcode": "6921345678901",
          "spec": "D12*75L*4F 钨钢涂层",
          "unit": "把",
          "category": "五金工具",
          "purchase_price": "85.0000",
          "sales_price": "145.0000",
          "retail_price": "168.0000",
          "min_price": "130.0000",
          "batch_tracking": true,
          "min_stock": 20,
          "max_stock": 500
        },
        "active": true,
        "updated_at": "2026-10-15T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

##### 2. 新增基础资料
- **方法与路径**: `POST /api/masters/:kind`
- **权限标识**: `base.edit`
- **请求 Body**:
```json
{
  "kind": "product",
  "code": "P1006",
  "name": "超细纳米金刚石研磨膏",
  "data": {
    "barcode": "6978123456789",
    "spec": "W3.5 针筒装 5g",
    "unit": "支",
    "category": "耗材",
    "purchase_price": "28.5000",
    "sales_price": "55.0000",
    "retail_price": "60.0000",
    "min_price": "48.0000"
  },
  "active": true
}
```

##### 3. 修改基础资料
- **方法与路径**: `PUT /api/masters/:kind/:id`
- **权限标识**: `base.edit`

---

### 04 采购管理 (Procurement Management)

#### 界面截图参照
- 采购入库单列表：`tenant_screenshots/04_采购管理/04_01_采购入库单列表.png`
- 采购开单录入编辑器：`tenant_screenshots/04_采购管理/04_03_采购入库_开单录入编辑器.png`

#### 业务单据类型定义 (`kind`)
- `purchase_order`: 采购订单（约定采购意向，不影响库存与往来账）
- `purchase_receipt`: 采购入库单（增加在库库存，更新移动加权成本，生成供应商应付账款）
- `purchase_return`: 采购退货单（扣减在库库存，核销或抵扣应付账款）
- `purchase_exchange`: 采购换货单（同时包含入库行与出库行）

#### 开单录入抽屉字段全集
1. **单据主表头**：
   - 业务日期 (`date`): 必填，默认为当天 `YYYY-MM-DD`
   - 供应商 (`partner_id`): 必选，下拉搜索或扫码选择
   - 业务仓库 (`warehouse_id`): 必选，收货入库的目标仓库
   - 约定到期付款日 (`due_date`): 可选，未填时按供应商账期天数自动推算
   - 业务员 (`employee_id`): 经办采购员
   - 业务备注 (`note`): 随货凭证号、交货说明
2. **明细表格行 (`items`)**：
   - 商品选择 (`product_id`): 扫码自动带出编码、名称、单位
   - 库位 (`location_id`): 入库货位，默认 0 为默认主货位
   - 批次号 (`batch`): 若商品启用批次跟踪则必填，可扫码录入
   - 序列号 (`serials`): 逐个扫描唯一序列号
   - 采购数量 (`quantity`): 支持最多 4 位小数
   - 采购单价 (`price`): 进货单价，默认带出基准进价
   - 小计金额 (`amount`): 自动联动计算 `quantity * price`
3. **单据生命周期状态 (`status`)**：
   - `draft` (草稿) $\rightarrow$ `submitted` (已提交) $\rightarrow$ `approved` (已审核记账) $\rightarrow$ `closed` (已关闭) / `cancelled` (作废)

#### 对应后端接口

##### 1. 查询采购单据列表
- **方法与路径**: `GET /api/documents`
- **Query 参数**:
  - `module=purchase`
  - `kind=purchase_receipt` (可选：purchase_order / purchase_receipt / purchase_return)
  - `search=`: 单号、供应商名称关键词
  - `status=`: draft / submitted / approved
  - `page=1&limit=20`
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "id": 201,
        "number": "PR20261016001",
        "kind": "purchase_receipt",
        "kind_name": "采购入库",
        "status": "approved",
        "partner_id": 3,
        "partner_name": "上海精工切削刀具集团有限公司",
        "warehouse_id": 1,
        "warehouse_name": "主仓库（杭州总仓）",
        "date": "2026-10-16",
        "amount": "12750.00",
        "creator": "许永红",
        "created_at": "2026-10-16T09:15:00Z"
      }
    ],
    "total": 1
  }
}
```

##### 2. 获取单据全量详情（含明细、流水与审批日志）
- **方法与路径**: `GET /api/documents/:id`
- **响应包含**:
  - `document`: 单据主表信息及 `items` 明细列表
  - `transactions`: 审核后生成的库存增减记录与成本核算变动
  - `related`: 上下游关联单据（如关联的采购订单或付款单）
  - `history`: 经办人操作审计日志

##### 3. 新建采购单据（保存草稿）
- **方法与路径**: `POST /api/documents`
- **请求头**: 需带 `Idempotency-Key`
- **请求 Body**:
```json
{
  "kind": "purchase_receipt",
  "partner_id": 3,
  "warehouse_id": 1,
  "date": "2026-10-16",
  "due_date": "2026-11-15",
  "note": "十月份第一批机加工刀具入库",
  "items": [
    {
      "product_id": 1,
      "location_id": 0,
      "batch": "20261016-01",
      "quantity": "150",
      "price": "85.0000",
      "serials": [],
      "note": ""
    }
  ]
}
```

##### 4. 单据动作流转（提交、审核、退回、红冲、作废）
- **方法与路径**: `POST /api/documents/:id/actions/:action`
  - `:action` 取值：
    - `submit`: 提交审核
    - `approve`: 审核入账（必须有 `purchase.approve` 权限）
    - `reject`: 退回草稿
    - `cancel`: 作废单据
    - `reverse`: 红冲单据（反向生成红字凭单冲减库存与往来）
    - `close`: 提前关闭订单
- **请求 Body**:
```json
{
  "reason": "货品经质检验收合格，确认入库入账"
}
```

---

### 05 销售管理 (Sales & Outbound Management)

#### 界面截图参照
- 销售出库单列表：`tenant_screenshots/05_销售管理/05_01_销售出库单列表.png`
- 销售开单录入编辑器：`tenant_screenshots/05_销售管理/05_05_销售开单录入编辑器.png`

#### 业务单据类型定义 (`kind`)
- `sales_order`: 销售订单（意向锁定库存，支持下推销售出库）
- `sales_delivery`: 销售出库单（扣减在库库存、核算销售成本、生成客户应收款）
- `retail_sale`: 零售销售（现场现销，出库并直接生成收款单）
- `sales_return`: 销售退货（客户退回商品，入库并冲减应收）
- `consignment_ship`: 委托发货（移库至委托仓）
- `consignment_settlement`: 代销结算（确认代销销售收入）

#### 移动销售开单核心逻辑
1. **智能价格策略引擎**：
   选择客户和商品后，App 自动调用 `/api/prices/quote` 接口获取该客户的约定售价；若用户手动修改且低于最低限价 `min_price`，系统拦截并提示“售价低于最低限价，需主管特权”。
2. **负库存校验阻断**：
   若租户系统设置中关闭了“允许负库存”，开单数量大于当前在库可用数量时，保存将被服务端拦截。
3. **现场现销勾选**：
   勾选 `cash_sale: true` 时，审核通过的同时会自动生成一张已核销的 `ar_receipt` 客户收款单。

#### 对应后端接口

##### 1. 销售智能价格试算接口
- **方法与路径**: `GET /api/prices/quote`
- **Query 参数**:
  - `product_id=1`: 商品 ID
  - `customer_id=2`: 客户 ID
  - `quantity=10`: 销售数量
  - `date=2026-10-16`: 销售日期
  - `channel=wholesale`: 渠道（wholesale 批发 / retail 零售）
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "price": "142.0000",
    "source": "客户等级折扣价 (98折)",
    "rule_id": 12
  }
}
```

##### 2. 创建销售出库单
- **方法与路径**: `POST /api/documents`
- **请求 Body**:
```json
{
  "kind": "sales_delivery",
  "partner_id": 2,
  "warehouse_id": 1,
  "date": "2026-10-16",
  "data": {
    "cash_sale": false,
    "employee_id": 1
  },
  "note": "发往西湖精工一号车间",
  "items": [
    {
      "product_id": 1,
      "location_id": 0,
      "batch": "20261016-01",
      "quantity": "20",
      "price": "145.0000"
    }
  ]
}
```

##### 3. 单据下推转换（如下推生成退货单）
- **方法与路径**: `GET /api/documents/:id/convert?target=sales_return`
- **作用**: 提取原出库单中未退货的剩余明细与原单单价，直接生成退货单预填草稿，防止篡改价格或超额退货。

---

### 06 库存管理 (Inventory & Warehouse Management)

#### 界面截图参照
- 库存结存与在库成本：`tenant_screenshots/06_库存管理/06_01_库存结存与在库成本.png`
- 库存出入库流水明细：`tenant_screenshots/06_库存管理/06_02_库存出入库流水明细.png`

#### 界面内容与字段清单

##### A. 实时在库结存表
- 维度：商品编码、名称、规格、仓库、库位、批次
- 数据指标：
  - `quantity`: 账面实物结存数量
  - `avg_cost`: 移动加权单位成本（元，受 `cost.view` 权限控制）
  - `total_cost`: 库存资产总估值金额 (`quantity * avg_cost`)
  - `updated_at`: 最后一次出入库发生时间

##### B. 出入库流水溯源表
- 记录每一笔导致库存发生变动的原始交易轨迹：
  - `id`: 流水记录唯一 ID
  - `document_number`: 关联业务单据号（支持点击穿透查看原单）
  - `product_name`: 商品名称
  - `warehouse_name`: 仓库名称
  - `before_quantity`: 变动前数量
  - `delta`: 变动量（正数入库，负数出库）
  - `after_quantity`: 变动后结存数量
  - `cost_delta`: 成本金额变动
  - `operator`: 经办人

#### 对应后端接口

##### 1. 查询实时库存结存
- **方法与路径**: `GET /api/inventory/balances`
- **Query 参数**:
  - `warehouse_id`: 仓库 ID 过滤
  - `search`: 商品名称/编码/批次模糊查询
- **权限标识**: `inventory.view`
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "product_id": 1,
        "product_code": "P1001",
        "product_name": "高精度数控合金铣刀",
        "spec": "D12*75L*4F 钨钢涂层",
        "unit": "把",
        "warehouse_id": 1,
        "warehouse_name": "主仓库（杭州总仓）",
        "location_id": 0,
        "location_name": "默认库位",
        "batch": "20261016-01",
        "quantity": "130.0000",
        "avg_cost": "85.0000",
        "total_cost": "11050.00"
      }
    ],
    "total": 1
  }
}
```

##### 2. 查询出入库流水
- **方法与路径**: `GET /api/inventory/transactions`
- **Query 参数**: `document_id=`, `warehouse_id=`, `search=`

---

### 07 往来资金管理 (Finance & AR/AP Settlements)

#### 界面截图参照
- 客户应收账单明细：`tenant_screenshots/07_往来管理/07_01_客户应收账单明细.png`
- 供应商应付账单明细：`tenant_screenshots/07_往来管理/07_02_供应商应付账单明细.png`
- 应收账龄穿透分析表：`tenant_screenshots/07_往来管理/07_03_应收账龄穿透分析表.png`

#### 业务模型说明
1. **账单生成**：销售出库单审核后生成客户应收账单 (`side: customer`)；采购入库单审核后生成供应商应付账单 (`side: supplier`)。
2. **结算与核销**：录入收款单 (`ar_receipt`) 审核后，可调用核销接口将收款金额分摊分配到一张或多张未结清账单上。
3. **红字对冲**：销售退货单生成的贷项红字账单，可与正向应收账单执行一键对冲 (`offset`)。

#### 对应后端接口

##### 1. 查询应收/应付账单明细
- **方法与路径**: `GET /api/finance/bills`
- **Query 参数**:
  - `side=customer` (客户应收) 或 `side=supplier` (供应商应付)
  - `partner_id=`: 往来单位 ID 过滤
  - `open=true`: 仅返回未结清（有未核销余额）的账单
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "id": 501,
        "document_id": 202,
        "document_number": "SD20261016001",
        "partner_id": 2,
        "partner_name": "杭州西湖精工机械制造有限公司",
        "date": "2026-10-16",
        "due_date": "2026-11-15",
        "amount": "2900.00",
        "paid_amount": "0.00",
        "balance": "2900.00",
        "status": "unpaid",
        "overdue_days": 0
      }
    ],
    "total": 1
  }
}
```

##### 2. 查询往来账龄穿透分析表
- **方法与路径**: `GET /api/finance/aging`
- **Query 参数**: `side=customer` 或 `side=supplier`
- **响应报文包含各区间分布**:
  - `not_due`: 未到期金额
  - `days_1_30`: 逾期 1~30 天金额
  - `days_31_60`: 逾期 31~60 天金额
  - `days_61_90`: 逾期 61~90 天金额
  - `days_over_90`: 逾期 90 天以上高风险坏账预警金额

##### 3. 执行往来款项核销
- **方法与路径**: `POST /api/finance/writeoff`
- **权限标识**: `finance.approve`
- **请求 Body**:
```json
{
  "settlement_id": 301,
  "allocations": [
    {
      "bill_id": 501,
      "amount": "2900.00"
    }
  ]
}
```

##### 4. 红字账单对冲 (Offset)
- **方法与路径**: `POST /api/finance/offset`
- **请求 Body**:
```json
{
  "positive_bill_id": 501,
  "negative_bill_id": 502,
  "amount": "500.00"
}
```

---

### 08 价格管理中心 (Pricing Strategies)

#### 界面截图参照
- 价格管理中心：`tenant_screenshots/08_价格管理/08_01_商品价格管理中心.png`

#### 业务内容
维护各类灵活定价策略：
- 客户等级定价（如一级批发商享受进价加成 15% 或指导价 9折）
- 批量阶梯定价（如采购超过 100 件单价直降 5 元）
- 限时特惠价格规则

#### 对应后端接口
- 查询规则列表：`GET /api/prices`
- 新增价格规则：`POST /api/prices`
- 修改价格规则：`PUT /api/prices/:id`

---

### 09 报表中心 (Business Intelligence & Reports)

#### 界面截图参照
- 进销存综合报表中心：`tenant_screenshots/09_报表中心/09_01_进销存综合报表中心.png`

#### 核心报表类别
1. **销售毛利汇总表 (`category=sales`)**：统计销售金额、销售成本、销售毛利与毛利率。
2. **商品收发存明细表 (`category=inventory`)**：期初结存 $\rightarrow$ 本期入库 $\rightarrow$ 本期出库 $\rightarrow$ 期末结存。
3. **客户/业务员销售业绩表 (`group=customer` 或 `group=employee`)**。

#### 对应后端接口
- **方法与路径**: `GET /api/reports`
- **Query 参数**:
  - `category`: `sales` / `inventory` / `purchase`
  - `group`: `date` (按日) / `product` (按商品) / `customer` (按客户) / `warehouse` (按仓库)
  - `start`: 开始日期 (`YYYY-MM-DD`)
  - `end`: 截止日期 (`YYYY-MM-DD`)
- **权限标识**: `reports.view`

---

### 10 预警中心 (Alerts & Monitoring)

#### 界面截图参照
- 经营预警监控中心：`tenant_screenshots/10_预警中心/10_01_经营预警监控中心.png`

#### 预警触发类型
- `stock_min`: 库存不足预警（当前可用库存 < 安全库存下限 `min_stock`）
- `stock_max`: 呆滞积压预警（当前在库库存 > 库存上限 `max_stock`）
- `ar_overdue`: 应收账款逾期预警（账单超过到期日且未结清）

#### 对应后端接口

##### 1. 查询待处理预警列表
- **方法与路径**: `GET /api/warnings`
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "items": [
      {
        "key": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        "kind": "stock_min",
        "title": "库存短缺预警",
        "message": "商品【高精度数控合金铣刀】在主仓库仅存 15 件，已低于预警线 20 件",
        "severity": "high",
        "created_at": "2026-10-16T08:00:00Z"
      }
    ],
    "total": 1
  }
}
```

##### 2. 确认/忽略预警
- **方法与路径**: `POST /api/warnings/ack`
- **请求 Body**: `{"key": "64位哈希预警Key"}`

---

### 11 系统管理 (Tenant System Settings)

#### 界面截图参照
- 企业与业务参数设置：`tenant_screenshots/11_系统管理/11_01_企业与业务参数设置.png`
- 租户用户管理：`tenant_screenshots/11_系统管理/11_02_租户用户管理.png`
- 角色与权限配置：`tenant_screenshots/11_系统管理/11_03_角色与权限配置.png`
- 单据编号规则设置：`tenant_screenshots/11_系统管理/11_04_单据编号规则设置.png`
- 操作审计日志：`tenant_screenshots/11_系统管理/11_05_操作审计日志.png`
- 数据备份与快照：`tenant_screenshots/11_系统管理/11_06_数据备份与快照.png`

#### 核心业务参数字典 (Settings)
- `allow_negative_stock`: 是否允许负库存出库 (bool)
- `enforce_credit_limit`: 超信用额度是否强制阻断开单 (bool)
- `block_overdue_customer`: 存在逾期账款的客户是否禁止继续开单 (bool)
- `cost_method`: 成本核算方法（如 `moving_average` 移动加权平均）
- `stagnant_days`: 呆滞库存天数警戒阈值（默认 90 天）

#### 对应后端接口
- 获取系统参数：`GET /api/system/settings`
- 更新系统参数：`PUT /api/system/settings`
- 查询租户操作员：`GET /api/system/users`
- 查询角色权限树：`GET /api/system/roles`
- 移动端版本检测：`GET /api/mobile/version/check`

---

### 12 弹窗与通用交互组件 (Dialogs & Components)

#### 界面截图参照
- 全局智能检索：`tenant_screenshots/12_弹窗与交互组件/12_01_全局智能检索_Ctrl_K.png`
- 用户个人中心菜单：`tenant_screenshots/12_弹窗与交互组件/12_02_用户个人中心下拉菜单.png`
- 修改密码弹窗：`tenant_screenshots/12_弹窗与交互组件/12_03_修改密码弹窗.png`

#### 对应后端接口

##### 1. 操作员修改个人登录密码
- **方法与路径**: `POST /api/auth/password`
- **请求 Body**:
```json
{
  "old_password": "OldPassword123!",
  "new_password": "NewPassword456@"
}
```

---

## 4. 移动端专属高性能作业接口

为了满足手持工业 PDA 及移动手机在扫码开单、仓库快盘、现场签收等高频作业时的极致性能要求，服务端专门构建了 `/api/mobile/*` 优化路由组。

### 4.1 条码/二维码毫秒级极速查品 (`/api/mobile/scan`)
- **场景**：仓库收发货扫码、PDA 查库位、商品货架查现存。
- **方法与路径**: `GET /api/mobile/scan`
- **Query 参数**: `barcode=6921345678901`
- **性能指标**: 服务端走 B-Tree 索引，响应耗时 < 15ms。
- **响应示例**:
```json
{
  "ok": true,
  "data": {
    "product": {
      "id": 1,
      "code": "P1001",
      "name": "高精度数控合金铣刀",
      "spec": "D12*75L*4F 钨钢涂层",
      "barcode": "6921345678901",
      "unit": "把",
      "sales_price": "145.0000",
      "purchase_price": "85.0000"
    },
    "stocks": [
      {
        "warehouse_id": 1,
        "warehouse_name": "主仓库（杭州总仓）",
        "quantity": "130.0000",
        "batch": "20261016-01",
        "location_name": "A区-01货架"
      }
    ],
    "total_quantity": "130.0000"
  }
}
```

### 4.2 移动现场快速扫码盘点 (`/api/mobile/quick-inventory`)
- **场景**：仓管手持 PDA 在货架间边走边扫条码，扫一次累加实盘数量，全部扫完后一键提交。
- **方法与路径**: `POST /api/mobile/quick-inventory`
- **请求 Body**:
```json
{
  "warehouse_id": 1,
  "auto_submit": true,
  "note": "PDA 盘点 A 库区五金刀具",
  "items": [
    {
      "product_id": 1,
      "batch": "20261016-01",
      "counted_quantity": "128",
      "location_id": 0
    }
  ]
}
```
- **服务端处理**: 自动比对数据库账面在库数，计算盘盈/盘亏差异，生成正式盘点单。

### 4.3 现场销售极速出库开单 (`/api/mobile/quick-order`)
- **场景**：业务员在展会或客户工厂现场开单，扫描客户二维码与商品条码，立即完成扣库。
- **方法与路径**: `POST /api/mobile/quick-order`
- **请求 Body**:
```json
{
  "kind": "sales_delivery",
  "partner_id": 2,
  "warehouse_id": 1,
  "auto_submit": true,
  "note": "现场拜访直接交货",
  "items": [
    {
      "product_id": 1,
      "quantity": "5",
      "price": "145.0000",
      "batch": "20261016-01"
    }
  ]
}
```

### 4.4 现场拍照与凭证上传 (`/api/mobile/upload`)
- **场景**：采购送货单纸质单据拍照、客户签收单回执拍照、转账回单上传。
- **方法与路径**: `POST /api/mobile/upload`
- **格式**: `multipart/form-data`
- **字段**: `file` (图片文件二进制流)
- **响应报文**:
```json
{
  "ok": true,
  "data": {
    "file_url": "/api/mobile/uploads/xuyonghong/202610/receipt_photo_101.jpg",
    "filename": "receipt_photo_101.jpg",
    "size": 1845200,
    "sha256": "4a7b9c..."
  }
}
```

### 4.5 离线增量数据同步 (`/api/mobile/sync/*`)
- **场景**：App 启动或定时后台拉取有变动的商品与客户资料，保存在本地 Room 数据库中。
- **接口 1**: `GET /api/mobile/sync/products?since=2026-10-15T08:00:00Z`
- **接口 2**: `GET /api/mobile/sync/customers?since=2026-10-15T08:00:00Z`
- **响应报文**: 返回自 `since` 时间戳以来发生过新增或修改的记录全集，以及当前服务器时间戳 `last_sync_time`。

---

## 5. 安卓客户端（Kotlin / Jetpack Compose）开发落地建议

### 5.1 推荐应用架构与技术栈
- **语言**：Kotlin 1.9+
- **界面开发**：Jetpack Compose + Material 3（声明式 UI，极其适合手持 PDA 多分辨率屏幕适配）
- **架构模式**：MVI (Model-View-Intent) 或 MVVM + Clean Architecture
- **网络层**：Retrofit2 + OkHttp4
  - 在 OkHttp 拦截器（`Interceptor`）中统一注入 `X-Tenant-Code`、`Authorization`、`Idempotency-Key`、`X-Platform: android`
  - 统一捕获 HTTP 401 触发登录页重定向或调用 `/api/mobile/auth/refresh`
- **本地存储**：Room Database（离线单据草稿、商品档案、客户档案离线缓存）+ DataStore Preferences（租户代码、最后登录账号）
- **依赖注入**：Hilt / Koin

### 5.2 工业 PDA 红外扫头与通用扫码集成
工业手持机（如霍尼韦尔 Honeywell、斑马 Zebra、优博讯 Urovo、销邦 Newland）通常通过系统广播（Broadcast）派发条码扫描结果，无需打开摄像头即可实现 50ms 级瞬间扫描：
```kotlin
// Android 广播接收器监听 PDA 激光扫码广播示例
class PdaScanReceiver(private val onBarcodeScanned: (String) -> Unit) : BroadcastReceiver() {
    override fun onReceive(context: Context?, intent: Intent?) {
        // 适配各主流厂商标准 Action
        val barcode = intent?.getStringExtra("scannerdata") // 优博讯
            ?: intent?.getStringExtra("barcode_string")    // 斑马
            ?: intent?.getStringExtra("data")              // 霍尼韦尔
            ?: intent?.getStringExtra("SCAN_BARCODE1")     // 销邦
        if (!barcode.isNullOrBlank()) {
            onBarcodeScanned(barcode.trim())
        }
    }
}
```
对于普通安卓手机，使用 **CameraX + Google ML Kit Barcode Scanning** 实现连续快速取景识别。

### 5.3 蓝牙便携小票机 / 送货单热敏打印
业务员随身携带便携 58mm / 80mm 蓝牙热敏打印机，开单成功后通过 Bluetooth SPP 协议直连发送 ESC/POS 指令集：
- 打印联次：存根联、客户签收联
- 打印内容：
  - 租户企业名称、单据编号（附带 Code128 条形码，方便下游扫码对账）
  - 业务往来单位、出库仓库、开单日期
  - 商品明细清单（序号、名称、规格、数量、单价、金额）
  - 合计金额、制单人、客户签字横线

### 5.4 离线离网开单与后台 WorkManager 补偿机制
1. **弱网开单暂存**：
   当检测到网络不可用时，允许业务员在 App 录入单据并保存为“本地待同步草稿”，状态标记为 `local_pending`。
2. **后台无感上传**：
   使用 Android Jetpack `WorkManager` 注册网络约束任务（`Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED)`）。一旦网络连通，后台队列自动以相同的 `Idempotency-Key` 依次请求服务端 `/api/documents` 或 `/api/mobile/quick-order`，保证数据不丢、不重。

---

## 6. 附录：截图与文件路径映射全览

所有界面设计截图均存放于项目根目录 `tenant_screenshots/` 中，文件路径清单如下：

```text
tenant_screenshots/
├── 01_登录与空间认证/
│   ├── 01_01_租户登录界面_桌面端.png
│   ├── 01_02_租户空间代码输入态.png
│   └── 01_03_租户登录界面_移动端仿真.png
├── 02_经营工作台/
│   ├── 02_01_经营工作台_全景概览.png
│   ├── 02_02_快捷开单下拉菜单.png
│   └── 02_03_经营工作台_移动端视图.png
├── 03_基础资料/
│   ├── 03_01_商品资料列表.png
│   ├── 03_02_商品资料_新增编辑表单.png
│   ├── 03_03_客户资料列表.png
│   ├── 03_04_客户资料_新增编辑表单.png
│   ├── 03_05_供应商资料列表.png
│   ├── 03_06_仓库资料列表.png
│   ├── 03_07_商品分类列表.png
│   └── 03_08_计量单位列表.png
├── 04_采购管理/
│   ├── 04_01_采购入库单列表.png
│   └── 04_03_采购入库_开单录入编辑器.png
├── 05_销售管理/
│   ├── 05_01_销售出库单列表.png
│   └── 05_05_销售开单录入编辑器.png
├── 06_库存管理/
│   ├── 06_01_库存结存与在库成本.png
│   └── 06_02_库存出入库流水明细.png
├── 07_往来管理/
│   ├── 07_01_客户应收账单明细.png
│   ├── 07_02_供应商应付账单明细.png
│   └── 07_03_应收账龄穿透分析表.png
├── 08_价格管理/
│   └── 08_01_商品价格管理中心.png
├── 09_报表中心/
│   └── 09_01_进销存综合报表中心.png
├── 10_预警中心/
│   └── 10_01_经营预警监控中心.png
├── 11_系统管理/
│   ├── 11_01_企业与业务参数设置.png
│   ├── 11_02_租户用户管理.png
│   ├── 11_03_角色与权限配置.png
│   ├── 11_04_单据编号规则设置.png
│   ├── 11_05_操作审计日志.png
│   └── 11_06_数据备份与快照.png
└── 12_弹窗与交互组件/
    ├── 12_01_全局智能检索_Ctrl_K.png
    ├── 12_02_用户个人中心下拉菜单.png
    └── 12_03_修改密码弹窗.png
```

---
*文档编制完成，符合睿链进销存 V2.0 多租户架构标准规范，可作为安卓移动端与工业 PDA 客户端的正式产品设计与接口联调白皮书。*

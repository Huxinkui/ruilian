# 进销存与往来管理系统 V1.1

依据当前目录中的《睿链进销存与往来管理系统_架构与实现设计说明书_V1.1_含授权体系.docx》实现。数据库按要求改为 **SQLite**，采用 Go + Gin 服务层和 Vue 3 + TypeScript + Element Plus + ECharts 界面。

## 安装使用

双击 `release/进销存管理系统安装程序-1.1.0-x64.exe`，安装后从桌面“进销存管理系统”打开。客户端自带 Electron 运行环境，支持 Windows 10 / 11 x64，离线使用，不依赖浏览器或另行安装数据库。

默认用户名、密码均为 `admin`。登录后在右上角账号菜单修改密码。所有页面顶部显示试用期和一键授权入口；支持 10×24 小时试用和离线永久授权。

SQLite 数据保存在 `%APPDATA%\RuilianERP\data\ruilian.db`。客户端退出时安全停止数据服务；升级、卸载保留业务数据。“文件 → 从备份恢复”提供原生文件选择、校验、恢复和自动重启。

软件名称、简称和 Logo 可在“系统管理 → 企业与业务参数”设置。默认采用通用名称；安装包和快捷方式名称可编辑 `branding.json` 后重新打包。正文和图表采用内置 Noto Sans SC（OFL 1.1），界面图标为 Element Plus Icons（MIT）。

- 操作说明：`docs/使用说明.md`
- 品牌配置与素材许可：`docs/字体图标与品牌配置.md`
- 设计对照：`docs/实现与设计对照.md`
- 验收记录：`docs/验收记录.md`
- 发行方工具：`publisher-private/授权管理工具.exe`，说明见 `docs/发行方授权工具说明.md`

`publisher-private` 仅供发行方保存，客户包不含私钥或签发工具。构建会核对已有公私钥，更新版本继续使用原密钥。

## 已实现业务

- 工作台：销售、采购、应收应付、库存金额、毛利、趋势、账龄与待审核事项。
- 基础资料：商品、分类、品牌、单位、客户、供应商、仓库、库位、员工、组织及数据字典。
- 采购：订单、入库、退货、换货、原单转换和价格查询。
- 销售：订单、批发出库、零售现销、退换货、委托发货、代销结算、委托退回。
- 库存：余额、可用与占用、调拨、盘点、组装、拆分、批次、序列号、期初库存、完整流水和移动加权成本。
- 往来：应收应付、收付款、预收预付、退款、部分 / 多账单核销、贷项冲账、核销撤销、信用额度、账期、账龄和对账。
- 价格：客户专属、客户等级、促销、数量阶梯、基础价、零售价、指导价、客户基础折扣、最低售价控制。
- 报表：日 / 月报、商品 / 客户 / 供应商 / 业务员排行、退货分析、库存、盘点差异、账龄、经营毛利、回款率和库存周转。
- 预警：库存上下限、呆滞、信用超额、应收逾期、应付到期和最低售价异常。
- 系统：用户角色、菜单按钮权限、本人数据范围、仓库范围、金额 / 成本权限、审计、编号、自动备份、验证及恢复。
- 授权：10×24 小时试用、安装标识、回拨检测、签名激活、离线永久授权和独立发行工具。

## 数据与一致性

金额保存为整数分，数量和单价保存为 1/10000 的整数；服务端执行十进制定点运算。审核、库存流水、余额、往来账和核销在同一事务中完成。SQLite 使用 WAL、`synchronous=FULL`、外键、忙等待和 `BEGIN IMMEDIATE`。进程锁防止同一数据目录被两个服务或恢复工具同时写入。关键业务接口持久保存幂等键。

已审核单据不能直接编辑。退换货须关联原单，累计数量与序列号不能重复执行。红冲生成独立凭证；已有后续库存流水或下游单据时要求先处理关联业务。

SQLite 不采用 PostgreSQL 的 `FOR UPDATE`、WAL 归档或服务器备份命令。本版本使用事务级写锁和经一致性校验的在线快照。默认运行期间每 15 分钟生成快照，并保留日、周、月备份。独立磁盘和异地目录由企业配置，界面会提示未配置的副本位置。

## 源码与构建

```text
desktop/                   Electron 桌面入口、原生恢复与安装配置
frontend/src/              Vue 3 中文界面
frontend/public/fonts/     Noto Sans SC、OFL 与校验来源
frontend/tests/e2e.mjs      桌面业务、密码、品牌与字体验收
frontend/tests/client.mjs   桌面重启、授权、原生恢复与迁移验收
main.go                    隐藏本机数据服务与资源内嵌
internal/app/              SQLite、业务、权限、备份及 REST API
internal/branding/         打包默认品牌与兼容迁移
internal/licensecode/      Ed25519 验签及公钥
cmd/licensegen/            独立发行方 GUI/CLI
branding.json              默认名称、安装文件名、快捷方式和 Logo
scripts/build.ps1          配置品牌、检查、编译、NSIS 安装包
scripts/verify_release.py   实际安装、客户端验收和卸载验证
```

开发环境：Go 1.24+、Node.js 22+、npm、Python 3 + Pillow、Windows windres/gcc。依赖锁定在 go.sum、frontend/package-lock.json 和 desktop/package-lock.json。最终用户无需这些开发工具。

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\build.ps1
```

已安装依赖后可加 `-SkipInstall`；只生成展开客户端可加 `-UnpackedOnly`。脚本保留既有私钥和用户数据。字体已随源码提供，重新获取时可用 `scripts/fetch_fonts.py`，来源与 SHA-256 记录在字体目录。

```powershell
go test ./... -count=1
go vet ./...
python -X utf8 scripts/verify_release.py
```

发行验收使用 artifacts 下独立的安装与数据目录，在阻断外网资源的实际 Electron 窗口内操作。它会安装刚生成的 EXE、完成业务和授权/恢复测试，检查客户资源不含私钥或测试数据库，再卸载测试副本并检查数据保留。需要本机前端测试依赖及发行方密钥；不操作现有业务数据库。

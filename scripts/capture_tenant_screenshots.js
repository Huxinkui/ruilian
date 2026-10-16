const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { execFileSync } = require('child_process');
const { chromium } = require('/root/git/ruilian/frontend/node_modules/playwright');

const ROOT_DIR = '/root/git/ruilian';
const SCREENSHOTS_DIR = path.join(ROOT_DIR, 'tenant_screenshots');

// Ensure base directories exist
const SUBDIRS = [
  '01_登录与空间认证',
  '02_经营工作台',
  '03_基础资料',
  '04_采购管理',
  '05_销售管理',
  '06_库存管理',
  '07_往来管理',
  '08_价格管理',
  '09_报表中心',
  '10_预警中心',
  '11_系统管理',
  '12_弹窗与交互组件'
];

for (const sub of SUBDIRS) {
  fs.mkdirSync(path.join(SCREENSHOTS_DIR, sub), { recursive: true });
}

// 1. Generate active session in tenant database
const token = crypto.randomBytes(32).toString('hex');
const tokenHash = crypto.createHash('sha256').update(token).digest('hex');

execFileSync('python3', ['-c', `
import sqlite3
conn = sqlite3.connect('/root/git/ruilian/data/tenants/xuyonghong/ruilian.db')
conn.cursor().execute('INSERT INTO sessions(token_hash, user_id, expires_at, created_at) VALUES(?, 2, datetime("now", "+1 day"), datetime("now"))', ('${tokenHash}',))
conn.commit()
`]);

console.log('Tenant session prepared successfully:', token.slice(0, 8) + '...');

async function main() {
  const browser = await chromium.launch({ headless: true });

  // ----------------------------------------------------
  // Helper: Create Authenticated Desktop Page
  // ----------------------------------------------------
  async function createAuthPage(isMobile = false) {
    const viewport = isMobile ? { width: 390, height: 844 } : { width: 1440, height: 900 };
    const context = await browser.newContext({
      viewport,
      deviceScaleFactor: isMobile ? 2 : 1.25,
      isMobile,
      cookies: [
        { name: 'ruilian_session', value: token, domain: '127.0.0.1', path: '/' },
        { name: 'ruilian_tenant', value: 'xuyonghong', domain: '127.0.0.1', path: '/' }
      ]
    });
    const page = await context.newPage();
    await page.addInitScript(({ token }) => {
      localStorage.setItem('ruilian_token', token);
      localStorage.setItem('ruilian_tenant', 'xuyonghong');
    }, { token });
    return { context, page };
  }

  // ----------------------------------------------------
  // 1. 登录与空间认证
  // ----------------------------------------------------
  console.log('Capturing: 01_登录与空间认证...');
  {
    // Unauthenticated context
    const anonContext = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1.25 });
    const anonPage = await anonContext.newPage();
    await anonPage.goto('http://127.0.0.1:3001/#/');
    await anonPage.waitForTimeout(1500);
    await anonPage.screenshot({ path: path.join(SCREENSHOTS_DIR, '01_登录与空间认证/01_01_租户登录界面_桌面端.png'), fullPage: true });

    // Fill sample tenant and username
    try {
      await anonPage.fill('input[placeholder*="租户空间代码"]', 'xuyonghong');
      await anonPage.waitForTimeout(300);
      await anonPage.screenshot({ path: path.join(SCREENSHOTS_DIR, '01_登录与空间认证/01_02_租户空间代码输入态.png') });
    } catch (e) {
      console.warn('Tenant input filling note:', e.message);
    }
    await anonContext.close();

    // Mobile login
    const mobAnonContext = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true });
    const mobAnonPage = await mobAnonContext.newPage();
    await mobAnonPage.goto('http://127.0.0.1:3001/#/');
    await mobAnonPage.waitForTimeout(1500);
    await mobAnonPage.screenshot({ path: path.join(SCREENSHOTS_DIR, '01_登录与空间认证/01_03_租户登录界面_移动端仿真.png'), fullPage: true });
    await mobAnonContext.close();
  }

  // ----------------------------------------------------
  // 2. 经营工作台 (Dashboard)
  // ----------------------------------------------------
  console.log('Capturing: 02_经营工作台...');
  {
    const { context, page } = await createAuthPage();
    await page.goto('http://127.0.0.1:3001/#/');
    await page.waitForTimeout(2500); // Wait for ECharts to render
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '02_经营工作台/02_01_经营工作台_全景概览.png'), fullPage: true });

    // Click "新建业务单据" dropdown to show shortcuts
    try {
      const btn = page.locator('button:has-text("新建业务单据")');
      if (await btn.count() > 0) {
        await btn.click();
        await page.waitForTimeout(500);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '02_经营工作台/02_02_快捷开单下拉菜单.png') });
        await page.keyboard.press('Escape');
      }
    } catch {}

    await context.close();

    // Mobile Dashboard
    const { context: mobCtx, page: mobPage } = await createAuthPage(true);
    await mobPage.goto('http://127.0.0.1:3001/#/');
    await mobPage.waitForTimeout(2500);
    await mobPage.screenshot({ path: path.join(SCREENSHOTS_DIR, '02_经营工作台/02_03_经营工作台_移动端视图.png'), fullPage: true });
    await mobCtx.close();
  }

  // ----------------------------------------------------
  // 3. 基础资料 (Masters)
  // ----------------------------------------------------
  console.log('Capturing: 03_基础资料...');
  {
    const { context, page } = await createAuthPage();

    // 商品资料
    await page.goto('http://127.0.0.1:3001/#/masters/product');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_01_商品资料列表.png'), fullPage: true });

    // 商品新增弹窗
    try {
      const addBtn = page.locator('button:has-text("新增商品")');
      if (await addBtn.count() > 0) {
        await addBtn.click();
        await page.waitForTimeout(800);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_02_商品资料_新增编辑表单.png') });
        // Close dialog
        await page.keyboard.press('Escape');
        await page.waitForTimeout(500);
      }
    } catch (e) {
      console.warn('Master dialog click note:', e.message);
    }

    // 客户资料
    await page.goto('http://127.0.0.1:3001/#/masters/customer');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_03_客户资料列表.png'), fullPage: true });

    // 客户新增弹窗
    try {
      const addBtn = page.locator('button:has-text("新增客户")');
      if (await addBtn.count() > 0) {
        await addBtn.click();
        await page.waitForTimeout(800);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_04_客户资料_新增编辑表单.png') });
        await page.keyboard.press('Escape');
        await page.waitForTimeout(500);
      }
    } catch {}

    // 供应商资料
    await page.goto('http://127.0.0.1:3001/#/masters/supplier');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_05_供应商资料列表.png'), fullPage: true });

    // 仓库资料
    await page.goto('http://127.0.0.1:3001/#/masters/warehouse');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_06_仓库资料列表.png'), fullPage: true });

    // 商品分类
    await page.goto('http://127.0.0.1:3001/#/masters/category');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_07_商品分类列表.png'), fullPage: true });

    // 计量单位
    await page.goto('http://127.0.0.1:3001/#/masters/unit');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '03_基础资料/03_08_计量单位列表.png'), fullPage: true });

    await context.close();
  }

  // ----------------------------------------------------
  // 4. 采购管理 (Purchase)
  // ----------------------------------------------------
  console.log('Capturing: 04_采购管理...');
  {
    const { context, page } = await createAuthPage();

    // 采购入库单列表
    await page.goto('http://127.0.0.1:3001/#/purchase');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '04_采购管理/04_01_采购入库单列表.png'), fullPage: true });

    // 打开采购单据详情查看
    try {
      const viewBtn = page.locator('button:has-text("查看单据"), button:has-text("查看")').first();
      if (await viewBtn.count() > 0) {
        await viewBtn.click();
        await page.waitForTimeout(1000);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '04_采购管理/04_02_采购入库单_详情查看与明细.png') });
        await page.keyboard.press('Escape');
        await page.waitForTimeout(600);
      }
    } catch {}

    // 点击“新建采购单据”
    try {
      const createBtn = page.locator('button:has-text("新建采购"), button:has-text("新建单据")').first();
      if (await createBtn.count() > 0) {
        await createBtn.click();
        await page.waitForTimeout(1000);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '04_采购管理/04_03_采购入库_开单录入编辑器.png') });
        await page.keyboard.press('Escape');
        await page.waitForTimeout(600);
      }
    } catch {}

    // 采购订单 Tab
    try {
      const orderTab = page.locator('.subnav button:has-text("采购订单")');
      if (await orderTab.count() > 0) {
        await orderTab.click();
        await page.waitForTimeout(1200);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '04_采购管理/04_04_采购订单单据列表.png'), fullPage: true });
      }
    } catch {}

    await context.close();
  }

  // ----------------------------------------------------
  // 5. 销售管理 (Sales)
  // ----------------------------------------------------
  console.log('Capturing: 05_销售管理...');
  {
    const { context, page } = await createAuthPage();

    // 销售出库单列表
    await page.goto('http://127.0.0.1:3001/#/sales');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '05_销售管理/05_01_销售出库单列表.png'), fullPage: true });

    // 查看销售出库详情
    try {
      const viewBtn = page.locator('button:has-text("查看单据"), button:has-text("查看")').first();
      if (await viewBtn.count() > 0) {
        await viewBtn.click();
        await page.waitForTimeout(1000);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '05_销售管理/05_02_销售出库单_详情查看.png') });
        await page.keyboard.press('Escape');
        await page.waitForTimeout(600);
      }
    } catch {}

    // 销售订单 Tab (含待审核单据)
    try {
      const orderTab = page.locator('.subnav button:has-text("销售订单")');
      if (await orderTab.count() > 0) {
        await orderTab.click();
        await page.waitForTimeout(1200);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '05_销售管理/05_03_销售订单列表_含待审核单据.png'), fullPage: true });

        // 查看待审核销售订单
        const pendingBtn = page.locator('button:has-text("处理"), button:has-text("查看单据")').first();
        if (await pendingBtn.count() > 0) {
          await pendingBtn.click();
          await page.waitForTimeout(1000);
          await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '05_销售管理/05_04_销售订单详情_待审核与操作流.png') });
          await page.keyboard.press('Escape');
          await page.waitForTimeout(600);
        }
      }
    } catch {}

    // 销售开单编辑器
    try {
      const newBtn = page.locator('button:has-text("新建销售"), button:has-text("新建单据")').first();
      if (await newBtn.count() > 0) {
        await newBtn.click();
        await page.waitForTimeout(1000);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '05_销售管理/05_05_销售开单录入编辑器.png') });
        await page.keyboard.press('Escape');
        await page.waitForTimeout(600);
      }
    } catch {}

    await context.close();
  }

  // ----------------------------------------------------
  // 6. 库存管理 (Inventory)
  // ----------------------------------------------------
  console.log('Capturing: 06_库存管理...');
  {
    const { context, page } = await createAuthPage();

    // 库存结存列表
    await page.goto('http://127.0.0.1:3001/#/inventory');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '06_库存管理/06_01_库存结存与在库成本.png'), fullPage: true });

    // 切换到“库存流水”
    try {
      const txTab = page.locator('.subnav button:has-text("收发明细"), .subnav button:has-text("库存明细"), .subnav button:has-text("流水")').first();
      if (await txTab.count() > 0) {
        await txTab.click();
        await page.waitForTimeout(1200);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '06_库存管理/06_02_库存出入库流水明细.png'), fullPage: true });
      }
    } catch {}

    await context.close();
  }

  // ----------------------------------------------------
  // 7. 往来管理 (Finance)
  // ----------------------------------------------------
  console.log('Capturing: 07_往来管理...');
  {
    const { context, page } = await createAuthPage();

    // 客户应收明细
    await page.goto('http://127.0.0.1:3001/#/finance');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '07_往来管理/07_01_客户应收账单明细.png'), fullPage: true });

    // 供应商应付明细
    await page.goto('http://127.0.0.1:3001/#/finance?view=ap');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '07_往来管理/07_02_供应商应付账单明细.png'), fullPage: true });

    // 账龄分析
    await page.goto('http://127.0.0.1:3001/#/finance?view=aging');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '07_往来管理/07_03_应收账龄穿透分析表.png'), fullPage: true });

    await context.close();
  }

  // ----------------------------------------------------
  // 8. 价格管理 (Prices)
  // ----------------------------------------------------
  console.log('Capturing: 08_价格管理...');
  {
    const { context, page } = await createAuthPage();

    await page.goto('http://127.0.0.1:3001/#/prices');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '08_价格管理/08_01_商品价格管理中心.png'), fullPage: true });

    await context.close();
  }

  // ----------------------------------------------------
  // 9. 报表中心 (Reports)
  // ----------------------------------------------------
  console.log('Capturing: 09_报表中心...');
  {
    const { context, page } = await createAuthPage();

    await page.goto('http://127.0.0.1:3001/#/reports');
    await page.waitForTimeout(2000);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '09_报表中心/09_01_进销存综合报表中心.png'), fullPage: true });

    await context.close();
  }

  // ----------------------------------------------------
  // 10. 预警中心 (Warnings)
  // ----------------------------------------------------
  console.log('Capturing: 10_预警中心...');
  {
    const { context, page } = await createAuthPage();

    await page.goto('http://127.0.0.1:3001/#/warnings');
    await page.waitForTimeout(1800);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '10_预警中心/10_01_经营预警监控中心.png'), fullPage: true });

    await context.close();
  }

  // ----------------------------------------------------
  // 11. 系统管理 (System)
  // ----------------------------------------------------
  console.log('Capturing: 11_系统管理...');
  {
    const { context, page } = await createAuthPage();

    // 参数设置
    await page.goto('http://127.0.0.1:3001/#/system?tab=settings');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '11_系统管理/11_01_企业与业务参数设置.png'), fullPage: true });

    // 用户管理
    await page.goto('http://127.0.0.1:3001/#/system?tab=users');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '11_系统管理/11_02_租户用户管理.png'), fullPage: true });

    // 角色权限
    await page.goto('http://127.0.0.1:3001/#/system?tab=roles');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '11_系统管理/11_03_角色与权限配置.png'), fullPage: true });

    // 单据编号
    await page.goto('http://127.0.0.1:3001/#/system?tab=numbering');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '11_系统管理/11_04_单据编号规则设置.png'), fullPage: true });

    // 审计日志
    await page.goto('http://127.0.0.1:3001/#/system?tab=audit');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '11_系统管理/11_05_操作审计日志.png'), fullPage: true });

    // 备份恢复
    await page.goto('http://127.0.0.1:3001/#/system?tab=backups');
    await page.waitForTimeout(1500);
    await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '11_系统管理/11_06_数据备份与快照.png'), fullPage: true });

    await context.close();
  }

  // ----------------------------------------------------
  // 12. 弹窗与交互组件 (Global Modals)
  // ----------------------------------------------------
  console.log('Capturing: 12_弹窗与交互组件...');
  {
    const { context, page } = await createAuthPage();
    await page.goto('http://127.0.0.1:3001/#/');
    await page.waitForTimeout(1500);

    // 全局快捷搜索 (Ctrl + K)
    try {
      const searchBtn = page.locator('.global-search');
      if (await searchBtn.count() > 0) {
        await searchBtn.click();
        await page.waitForTimeout(800);
        // 输入搜索词展示动态命中
        await page.keyboard.type('平板');
        await page.waitForTimeout(800);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '12_弹窗与交互组件/12_01_全局智能检索_Ctrl_K.png') });
        await page.keyboard.press('Escape');
        await page.waitForTimeout(500);
      }
    } catch (e) {
      console.warn('Search dialog note:', e.message);
    }

    // 用户下拉菜单 -> 修改密码
    try {
      const userMenu = page.locator('.user-menu');
      if (await userMenu.count() > 0) {
        await userMenu.click();
        await page.waitForTimeout(500);
        await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '12_弹窗与交互组件/12_02_用户个人中心下拉菜单.png') });

        const pwdItem = page.locator('.el-dropdown-menu__item:has-text("修改密码")');
        if (await pwdItem.count() > 0) {
          await pwdItem.click();
          await page.waitForTimeout(600);
          await page.screenshot({ path: path.join(SCREENSHOTS_DIR, '12_弹窗与交互组件/12_03_修改密码弹窗.png') });
          await page.keyboard.press('Escape');
        }
      }
    } catch (e) {
      console.warn('User menu note:', e.message);
    }

    await context.close();
  }

  await browser.close();
  console.log('All screenshots captured successfully!');
}

main().catch(err => {
  console.error('Fatal error during screenshot capture:', err);
  process.exit(1);
});

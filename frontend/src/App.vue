<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { DataBoard, Box, ShoppingCart, Sell, House, Wallet, PriceTag, TrendCharts, Bell, Setting, Search, ArrowDown, Refresh, QuestionFilled, ArrowRight, SwitchButton, Lock, Fold, Expand, OfficeBuilding, Monitor, WarningFilled } from '@element-plus/icons-vue'
import { useApp } from './store'
import { api, setToken, setTenantCode } from './api'
import Login from './components/Login.vue'
import BrandMark from './components/BrandMark.vue'
import DocEditor from './components/DocEditor.vue'
const app = useApp(), route = useRoute(), router = useRouter(), desktop = window.ruilianDesktop
const collapsed = ref(false), searchOpen = ref(false), keyword = ref(''), searchRows = ref<any[]>([]), searchBusy = ref(false), help = ref(false), passwordOpen = ref(false), busy = ref(false), stopped = ref(false), bootstrapError = ref(false)
const password = reactive({ old_password: '', new_password: '' })
const isMasterAdmin = computed(() => app.isMasterAdmin)
const isMasterAdminInMain = computed(() => {
  return (app.user?.username === 'huxinkui' || app.isMasterAdmin) && (app.tenant?.code === 'default' || !app.tenant?.code) && !app.isImpersonating
})
async function handleSwitchTenant(code: string) {
  try {
    await app.switchTenant(code)
    ElMessage.success(`已切换至租户空间：${app.tenant?.name || code}`)
  } catch (err: any) {
    ElMessage.error(err?.message || '切换租户空间失败')
  }
}
async function handleExitImpersonation() {
  try {
    await app.exitImpersonation()
    ElMessage.success('已安全退出跨空间巡检，已返回平台总控台')
    router.push('/')
  } catch (err: any) {
    ElMessage.error(err?.message || '退出巡检失败')
  }
}
const nav = computed(() => {
  // 当超级管理员在主管理空间时，展示平台总控运维专属导航
  if (isMasterAdminInMain.value) {
    return [
      { path: '/', label: '平台控制台', icon: Monitor, permission: '' },
      { path: '/tenants', label: '多租户管理', icon: OfficeBuilding, permission: 'system.settings' },
      { path: '/system', label: '系统与安全', icon: Setting, permission: '' }
    ]
  }

  // 普通租户空间（或管理员跨租户巡检）展示企业进销存业务导航
  // 普通租户绝不展示租户管理页面
  return [
    { path: '/', label: '经营工作台', icon: DataBoard, permission: '' },
    { path: '/masters/product', match: '/masters', label: '基础资料', icon: Box, permission: 'base.view' },
    { path: '/purchase', label: '采购管理', icon: ShoppingCart, permission: 'purchase.view' },
    { path: '/sales', label: '销售管理', icon: Sell, permission: 'sales.view' },
    { path: '/inventory', label: '库存管理', icon: House, permission: 'inventory.view' },
    { path: '/finance', label: '往来管理', icon: Wallet, permission: 'finance.view' },
    { path: '/prices', label: '价格管理', icon: PriceTag, permission: 'price.view' },
    { path: '/reports', label: '报表中心', icon: TrendCharts, permission: 'reports.view' },
    { path: '/warnings', label: '预警中心', icon: Bell, permission: 'warnings.view' },
    { path: '/system', label: '系统设置', icon: Setting, permission: '' }
  ].filter(n => !n.permission || app.can(n.permission))
})
const currentDate = new Date().toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' })
let timer: ReturnType<typeof setTimeout>, refreshTimer: ReturnType<typeof setInterval>
watch(keyword, value => { clearTimeout(timer); if (!value.trim()) { searchRows.value = []; return } timer = setTimeout(async () => { searchBusy.value = true; try { searchRows.value = await api('/search?q=' + encodeURIComponent(value)) } catch {} finally { searchBusy.value = false } }, 250) })
function selectResult(row: any) { searchOpen.value = false; if (row.type === 'document') app.openDoc(row.id); else router.push({ path: '/masters/' + row.kind, query: { search: row.name } }) }
function onKey(e: KeyboardEvent) { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); if (app.user) searchOpen.value = true } }
function resetSessionState() {
  setToken('')
  setTenantCode('')
  app.user = null
  app.tenantList = []
  app.tenant = { code: 'default', name: '默认租户', max_storage_mb: 500, used_storage_mb: 0, storage_percent: 0 }
  app.editor.visible = false
  router.replace('/')
}
function sessionExpired() { resetSessionState() }
function reconnect() { window.location.reload() }
async function menu(command: string) {
  if (command === 'shutdown' && desktop) { await desktop.quit(); return }
  if (command === 'password') { password.old_password = ''; password.new_password = ''; passwordOpen.value = true; return }
  if (command === 'logout') { try { await api('/auth/logout', 'POST') } finally { resetSessionState() } }
}
async function changePassword() { busy.value = true; try { await api('/auth/password', 'POST', password); passwordOpen.value = false; resetSessionState(); ElMessage.success('密码已修改，请重新登录') } catch {} finally { busy.value = false } }
onMounted(async () => { window.addEventListener('keydown', onKey); window.addEventListener('session-expired', sessionExpired); try { await app.bootstrap() } catch { bootstrapError.value = true }; refreshTimer = setInterval(() => { if (app.user && !stopped.value) { app.changed() } }, 60000) })
onBeforeUnmount(() => { clearTimeout(timer); clearInterval(refreshTimer); window.removeEventListener('keydown', onKey); window.removeEventListener('session-expired', sessionExpired) })
</script>
<template>
  <div v-if="!app.ready" class="boot"><BrandMark/><h3>正在连接 {{ app.branding.app_name }}…</h3></div>
  <div v-else-if="bootstrapError || stopped" class="boot"><BrandMark/><h2>{{ stopped ? '服务端已停止' : '暂时无法连接服务端' }}</h2><p class="muted">请检查服务端程序是否已启动（默认端口 3001）。</p><el-button @click="reconnect">重新连接</el-button></div>
  <Login v-else-if="!app.user" />
  <div v-else class="layout" :class="{ collapsed }">
    <aside class="sidebar">
      <router-link class="brand" to="/">
        <BrandMark/>
        <div v-if="!collapsed" :title="app.branding.app_name">{{ app.branding.brand_short }}<small>{{ isMasterAdminInMain ? '平台运维中枢' : '进销存与往来管理' }}</small></div>
      </router-link>
      <div v-if="!collapsed" class="workspace-label">{{ isMasterAdminInMain ? '平台总控与运维中心' : '企业经营管理平台' }}</div>
      <nav>
        <router-link v-for="item in nav" :key="item.path" :to="item.path" class="nav-item" :class="{ active: item.path === '/' ? route.path === '/' : route.path.startsWith(item.match || item.path) }" :title="item.label">
          <el-icon><component :is="item.icon" /></el-icon>
          <span v-if="!collapsed">{{ item.label }}</span>
          <i v-if="item.path === '/warnings' && app.warningCount" class="nav-dot"></i>
        </router-link>
      </nav>
      <div class="sidebar-footer">
        <span class="online-dot"></span>
        <template v-if="!collapsed"><span>Web 版 <small>V1.1.0</small></span></template>
      </div>
    </aside>
    <div class="main-shell">
      <!-- 超级管理员跨空间穿透巡检警示横幅 -->
      <div v-if="app.isImpersonating" class="impersonation-warning-bar">
        <div class="imp-content">
          <el-icon class="imp-icon"><WarningFilled /></el-icon>
          <div class="imp-text">
            <b>【超级管理员跨空间巡检中】</b>
            当前租户空间：<span class="imp-highlight">{{ app.tenant?.name || app.impersonatedTenant }} [{{ app.impersonatedTenant }}]</span>
            <span class="imp-sep">|</span>
            巡检模式：<el-tag size="small" :type="app.impersonationMode === 'readonly' ? 'warning' : 'danger'" effect="dark">{{ app.impersonationMode === 'readonly' ? '只读巡检 (安全拦截写操作)' : '运维排障读写 (写操作已代维审计)' }}</el-tag>
            <span class="imp-sep">|</span>
            临时凭证：<b>30分钟有效</b>
          </div>
        </div>
        <el-button type="danger" size="small" plain @click="handleExitImpersonation">一键退出巡检 · 返回平台总控台</el-button>
      </div>

      <header class="topbar">
        <div class="topbar-left">
          <el-button text circle :icon="collapsed ? Expand : Fold" aria-label="收起导航" @click="collapsed = !collapsed"/>
          <span class="breadcrumb">{{ app.branding.brand_short }} <el-icon><ArrowRight /></el-icon> <b>{{ isMasterAdminInMain && route.path === '/' ? '平台总控控制台' : route.meta.title }}</b></span>
        </div>
        <div class="topbar-right">
          <button class="global-search" @click="searchOpen = true"><el-icon><Search /></el-icon><span>搜索商品、客户或单据</span><kbd>Ctrl K</kbd></button>
          <el-button v-if="app.can('warnings.view')" text circle :icon="Bell" aria-label="预警中心" @click="router.push('/warnings')"/>
          <el-button text circle :icon="QuestionFilled" aria-label="帮助" @click="help = true"/>
          <span class="topbar-divider"></span>
          <el-dropdown @command="menu">
            <div class="user-menu">
              <span class="avatar">{{ app.user.name.slice(0, 1) }}</span>
              <div><b>{{ app.user.name }}</b><small>{{ isMasterAdminInMain ? '平台超级管理员' : app.user.role_name }}</small></div>
              <el-icon><ArrowDown /></el-icon>
            </div>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="password" :icon="Lock">修改密码</el-dropdown-item>
                <el-dropdown-item command="logout" :icon="SwitchButton">退出登录</el-dropdown-item>
                <el-dropdown-item v-if="desktop" command="shutdown" divided>退出软件</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </header>
      <div class="context-bar">
        <div class="context-left">
          <span v-if="isMasterAdminInMain">
            <b>🛡️ 平台总控管理中枢</b><i></i>{{ currentDate }}<i></i><small style="color:#64748b">管理员专属安全工作台 · 已阻断非法探测</small>
          </span>
          <span v-else>
            {{ app.company }}<i></i>{{ currentDate }}<i v-if="app.tenant"></i><small v-if="app.tenant" style="color:#64748b">租户空间: <b>{{ app.tenant.name || app.tenant.code }}</b> (已用 {{ (app.tenant.used_storage_mb||0).toFixed(1) }} MB / {{ app.tenant.max_storage_mb ? app.tenant.max_storage_mb+' MB' : '不限' }})</small>
          </span>
        </div>
        <div v-if="isMasterAdmin" class="tenant-switcher-wrap">
          <el-tag v-if="app.tenant?.code && app.tenant?.code !== 'default'" type="danger" effect="dark" size="small" class="tenant-active-tag">
            跨租户监管中: {{ app.tenant.name || app.tenant.code }}
          </el-tag>
          <el-button v-if="app.tenant?.code && app.tenant?.code !== 'default'" size="small" type="primary" plain @click="handleSwitchTenant('default')">
            返回平台控制台
          </el-button>
          <el-select
            :model-value="app.tenant?.code || 'default'"
            size="small"
            class="tenant-select"
            placeholder="切换租户空间"
            @change="handleSwitchTenant"
          >
            <el-option
              v-for="t in app.tenantList"
              :key="t.code"
              :label="t.name + (t.code === 'default' ? ' [主管理空间]' : ' [' + t.code + ']')"
              :value="t.code"
            >
              <div style="display: flex; justify-content: space-between; align-items: center; width: 100%;">
                <span>{{ t.name }}</span>
                <el-tag size="small" :type="t.code === 'default' ? 'primary' : 'info'" style="margin-left: 8px;">
                  {{ t.code === 'default' ? '主空间' : t.code }}
                </el-tag>
              </div>
            </el-option>
          </el-select>
        </div>
      </div>
      <main class="page-content"><router-view v-slot="{ Component }"><component :is="Component" :key="(route.path.startsWith('/masters') ? 'masters' : route.path) + '-' + (app.tenant?.code || 'default')" /></router-view><footer class="page-footer"><span>{{ app.branding.app_name }}</span><span>业务单据驱动 · 数据全程可追溯</span></footer></main>
    </div><DocEditor />
  </div>
  <el-dialog v-model="searchOpen" title="快速查找" width="660px" destroy-on-close><el-input v-model="keyword" :prefix-icon="Search" size="large" placeholder="输入商品名、客户名、编码或单据编号" autofocus clearable/><div v-loading="searchBusy" class="search-results"><button v-for="row in searchRows" :key="row.type + row.id" @click="selectResult(row)"><el-icon><component :is="row.type === 'document' ? DataBoard : Box"/></el-icon><div><b>{{ row.name }}</b><small>{{ row.code }}</small></div><el-tag size="small" type="info">{{ row.type === 'document' ? '业务单据' : '基础资料' }}</el-tag></button><el-empty v-if="!searchRows.length" :description="keyword ? '没有找到匹配的记录' : '输入关键词，快速定位业务数据'" :image-size="70"/></div></el-dialog>
  <el-dialog v-model="passwordOpen" title="修改登录密码" width="430px"><el-form label-position="top"><el-form-item label="原密码"><el-input v-model="password.old_password" type="password" show-password autocomplete="current-password"/></el-form-item><el-form-item label="新密码"><el-input v-model="password.new_password" type="password" show-password placeholder="至少 8 位，包含数字和非数字字符" autocomplete="new-password"/></el-form-item></el-form><template #footer><el-button @click="passwordOpen = false">取消</el-button><el-button type="primary" :loading="busy" @click="changePassword">保存并重新登录</el-button></template></el-dialog>
  <el-dialog v-model="help" :title="'开始使用' + app.branding.brand_short" width="660px"><div class="help-content"><h3>从基础资料开始，完成第一笔业务</h3><ol><li>在“基础资料”建立商品、客户、供应商与仓库。</li><li>如已有业务余额，先录入期初库存、期初应收和期初应付。</li><li>创建采购入库或销售出库单，保存草稿后提交，再审核入账。</li><li>在“往来管理”录入收付款；系统按到期日自动核销，超额作为预收 / 预付保留。</li><li>在“系统管理 → 备份恢复”下载备份，并配置独立磁盘与异地副本目录。</li></ol><el-alert title="审核后的单据不可直接修改" type="info" :closable="false" description="请使用关联原单的退货 / 换货业务；无后续流水时，可填写原因执行红冲。"/><p class="muted">数量与单价支持四位小数，账务金额保留两位。本系统基于 B/S 架构，支持浏览器多端协同，业务操作完成后可点击右上角安全退出登录。</p></div></el-dialog>
</template>

<style scoped>
.context-bar {
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 12px;
}
.context-left {
  display: flex;
  align-items: center;
  min-width: 0;
}
.tenant-switcher-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tenant-select {
  width: 220px;
}
.tenant-active-tag {
  font-weight: 600;
}
.impersonation-warning-bar {
  background: linear-gradient(90deg, #7c2d12 0%, #991b1b 100%);
  color: #fff;
  padding: 8px 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
  z-index: 100;
  font-size: 13px;
}
.imp-content {
  display: flex;
  align-items: center;
  gap: 10px;
}
.imp-icon {
  font-size: 18px;
  color: #fde047;
  animation: pulse 2s infinite;
}
.imp-text {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.imp-highlight {
  color: #fef08a;
  font-weight: bold;
}
.imp-sep {
  color: rgba(255, 255, 255, 0.4);
}
@keyframes pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.7; transform: scale(1.1); }
}
</style>

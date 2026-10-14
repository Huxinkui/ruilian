<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { DataBoard, Box, ShoppingCart, Sell, House, Wallet, PriceTag, TrendCharts, Bell, Setting, Search, ArrowDown, Refresh, QuestionFilled, ArrowRight, SwitchButton, Lock, Fold, Expand } from '@element-plus/icons-vue'
import { useApp } from './store'
import { api } from './api'
import Login from './components/Login.vue'
import TrialBanner from './components/TrialBanner.vue'
import BrandMark from './components/BrandMark.vue'
import DocEditor from './components/DocEditor.vue'
const app = useApp(), route = useRoute(), router = useRouter(), desktop = window.ruilianDesktop
const collapsed = ref(false), searchOpen = ref(false), keyword = ref(''), searchRows = ref<any[]>([]), searchBusy = ref(false), help = ref(false), passwordOpen = ref(false), busy = ref(false), stopped = ref(false), bootstrapError = ref(false)
const password = reactive({ old_password: '', new_password: '' })
const nav = computed(() => [
  { path: '/', label: '经营工作台', icon: DataBoard, permission: '' }, { path: '/masters/product', match: '/masters', label: '基础资料', icon: Box, permission: 'base.view' },
  { path: '/purchase', label: '采购管理', icon: ShoppingCart, permission: 'purchase.view' }, { path: '/sales', label: '销售管理', icon: Sell, permission: 'sales.view' },
  { path: '/inventory', label: '库存管理', icon: House, permission: 'inventory.view' }, { path: '/finance', label: '往来管理', icon: Wallet, permission: 'finance.view' },
  { path: '/prices', label: '价格管理', icon: PriceTag, permission: 'price.view' }, { path: '/reports', label: '报表中心', icon: TrendCharts, permission: 'reports.view' },
  { path: '/warnings', label: '预警中心', icon: Bell, permission: 'warnings.view' }, { path: '/system', label: '系统管理', icon: Setting, permission: '' }
].filter(n => !n.permission || app.can(n.permission)))
const currentDate = new Date().toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' })
let timer: ReturnType<typeof setTimeout>, refreshTimer: ReturnType<typeof setInterval>
watch(keyword, value => { clearTimeout(timer); if (!value.trim()) { searchRows.value = []; return } timer = setTimeout(async () => { searchBusy.value = true; try { searchRows.value = await api('/search?q=' + encodeURIComponent(value)) } catch {} finally { searchBusy.value = false } }, 250) })
function selectResult(row: any) { searchOpen.value = false; if (row.type === 'document') app.openDoc(row.id); else router.push({ path: '/masters/' + row.kind, query: { search: row.name } }) }
function onKey(e: KeyboardEvent) { if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); if (app.user) searchOpen.value = true } }
function sessionExpired() { app.user = null; app.editor.visible = false }
function reconnect() { window.location.reload() }
async function menu(command: string) {
  if (command === 'shutdown' && desktop) { await desktop.quit(); return }
  if (command === 'password') { password.old_password = ''; password.new_password = ''; passwordOpen.value = true; return }
  if (command === 'logout') { try { await api('/auth/logout', 'POST') } finally { app.user = null; app.editor.visible = false } }
  if (command === 'shutdown') { try { await ElMessageBox.confirm('停止后可再次双击 EXE 启动。正在编辑且未保存的内容将不会保留。', '停止本机服务', { type: 'warning', confirmButtonText: '停止服务' }); await api('/system/shutdown', 'POST'); stopped.value = true } catch {} }
}
async function changePassword() { busy.value = true; try { await api('/auth/password', 'POST', password); passwordOpen.value = false; app.user = null; ElMessage.success('密码已修改，请重新登录') } catch {} finally { busy.value = false } }
onMounted(async () => { window.addEventListener('keydown', onKey); window.addEventListener('session-expired', sessionExpired); try { await app.bootstrap() } catch { bootstrapError.value = true }; refreshTimer = setInterval(() => { if (app.user && !stopped.value) { app.changed(); api('/license', 'GET', undefined, true).then(l => app.license = l).catch(() => {}) } }, 60000) })
onBeforeUnmount(() => { clearTimeout(timer); clearInterval(refreshTimer); window.removeEventListener('keydown', onKey); window.removeEventListener('session-expired', sessionExpired) })
</script>
<template>
  <div v-if="!app.ready" class="boot"><BrandMark/><h3>正在连接 {{ app.branding.app_name }}…</h3></div>
  <div v-else-if="bootstrapError || stopped" class="boot"><BrandMark/><h2>{{ stopped ? '本机服务已安全停止' : '暂时无法连接本机服务' }}</h2><p class="muted">重新打开桌面客户端后，即可继续使用。</p><el-button @click="reconnect">重新连接</el-button></div>
  <Login v-else-if="!app.user" />
  <div v-else class="layout" :class="{ collapsed }">
    <aside class="sidebar"><router-link class="brand" to="/"><BrandMark/><div v-if="!collapsed" :title="app.branding.app_name">{{ app.branding.brand_short }}<small>进销存与往来管理</small></div></router-link><div v-if="!collapsed" class="workspace-label">企业经营管理平台</div><nav><router-link v-for="item in nav" :key="item.path" :to="item.path" class="nav-item" :class="{ active: item.path === '/' ? route.path === '/' : route.path.startsWith(item.match || item.path) }" :title="item.label"><el-icon><component :is="item.icon" /></el-icon><span v-if="!collapsed">{{ item.label }}</span><i v-if="item.path === '/warnings' && app.warningCount" class="nav-dot"></i></router-link></nav><div class="sidebar-footer"><span class="online-dot"></span><template v-if="!collapsed"><span>本地版 <small>V1.1.0</small></span></template></div></aside>
    <div class="main-shell"><header class="topbar"><div class="topbar-left"><el-button text circle :icon="collapsed ? Expand : Fold" aria-label="收起导航" @click="collapsed = !collapsed"/><span class="breadcrumb">{{ app.branding.brand_short }} <el-icon><ArrowRight /></el-icon> <b>{{ route.meta.title }}</b></span></div><div class="topbar-right"><button class="global-search" @click="searchOpen = true"><el-icon><Search /></el-icon><span>搜索商品、客户或单据</span><kbd>Ctrl K</kbd></button><el-button v-if="app.can('warnings.view')" text circle :icon="Bell" aria-label="预警中心" @click="router.push('/warnings')"/><el-button text circle :icon="QuestionFilled" aria-label="帮助" @click="help = true"/><span class="topbar-divider"></span><el-dropdown @command="menu"><div class="user-menu"><span class="avatar">{{ app.user.name.slice(0, 1) }}</span><div><b>{{ app.user.name }}</b><small>{{ app.user.role_name }}</small></div><el-icon><ArrowDown /></el-icon></div><template #dropdown><el-dropdown-menu><el-dropdown-item command="password" :icon="Lock">修改密码</el-dropdown-item><el-dropdown-item command="logout" :icon="SwitchButton">退出登录</el-dropdown-item><el-dropdown-item v-if="desktop || app.can('system.settings')" command="shutdown" divided>{{ desktop ? '退出软件' : '停止本机服务' }}</el-dropdown-item></el-dropdown-menu></template></el-dropdown></div></header>
      <div class="context-bar"><span>{{ app.company }}<i></i>{{ currentDate }}</span><button v-if="app.license.status === 'active'" class="trial-link" :class="{ warning: !app.license.writable }" @click="router.push('/system?tab=license')"><span class="online-dot" :class="{ amber: app.license.status !== 'active' }"></span>{{ app.license.status === 'active' ? '永久授权' : app.license.status === 'trial' ? `完整试用 · 剩余 ${app.license.days_remaining} 天` : '授权需要处理' }}<el-icon><ArrowRight /></el-icon></button></div>
      <TrialBanner/>
      <main class="page-content"><el-alert v-if="!app.license.writable" class="license-alert" type="warning" show-icon :closable="false" :title="app.license.message" description="经营数据仍可查看、导出与备份。请前往系统管理 → 软件授权激活。"/><router-view v-slot="{ Component }"><component :is="Component" :key="route.path.startsWith('/masters') ? 'masters' : route.path" /></router-view><footer class="page-footer"><span>{{ app.branding.app_name }}</span><span>业务单据驱动 · 数据全程可追溯</span></footer></main>
    </div><DocEditor />
  </div>
  <el-dialog v-model="searchOpen" title="快速查找" width="660px" destroy-on-close><el-input v-model="keyword" :prefix-icon="Search" size="large" placeholder="输入商品名、客户名、编码或单据编号" autofocus clearable/><div v-loading="searchBusy" class="search-results"><button v-for="row in searchRows" :key="row.type + row.id" @click="selectResult(row)"><el-icon><component :is="row.type === 'document' ? DataBoard : Box"/></el-icon><div><b>{{ row.name }}</b><small>{{ row.code }}</small></div><el-tag size="small" type="info">{{ row.type === 'document' ? '业务单据' : '基础资料' }}</el-tag></button><el-empty v-if="!searchRows.length" :description="keyword ? '没有找到匹配的记录' : '输入关键词，快速定位业务数据'" :image-size="70"/></div></el-dialog>
  <el-dialog v-model="passwordOpen" title="修改登录密码" width="430px"><el-form label-position="top"><el-form-item label="原密码"><el-input v-model="password.old_password" type="password" show-password autocomplete="current-password"/></el-form-item><el-form-item label="新密码"><el-input v-model="password.new_password" type="password" show-password placeholder="至少 8 位，包含数字和非数字字符" autocomplete="new-password"/></el-form-item></el-form><template #footer><el-button @click="passwordOpen = false">取消</el-button><el-button type="primary" :loading="busy" @click="changePassword">保存并重新登录</el-button></template></el-dialog>
  <el-dialog v-model="help" :title="'开始使用' + app.branding.brand_short" width="660px"><div class="help-content"><h3>从基础资料开始，完成第一笔业务</h3><ol><li>在“基础资料”建立商品、客户、供应商与仓库。</li><li>如已有业务余额，先录入期初库存、期初应收和期初应付。</li><li>创建采购入库或销售出库单，保存草稿后提交，再审核入账。</li><li>在“往来管理”录入收付款；系统按到期日自动核销，超额作为预收 / 预付保留。</li><li>在“系统管理 → 备份恢复”下载备份，并配置独立磁盘与异地副本目录。</li></ol><el-alert title="审核后的单据不可直接修改" type="info" :closable="false" description="请使用关联原单的退货 / 换货业务；无后续流水时，可填写原因执行红冲。"/><p class="muted">数量与单价支持四位小数，账务金额保留两位。{{ desktop ? '关闭客户端窗口或选择“退出软件”，会安全停止数据服务；请先保存正在编辑的单据。' : '通过右上角账号菜单可以安全停止本机服务。' }}</p></div></el-dialog>
</template>

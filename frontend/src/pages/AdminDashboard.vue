<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  OfficeBuilding,
  Plus,
  Refresh,
  Setting,
  Lock,
  Search,
  Key,
  Switch,
  Warning,
  ArrowRight,
  Monitor,
  Cpu,
  Folder,
  CircleCheck,
  DataAnalysis,
  User,
  DocumentCopy,
  Connection
} from '@element-plus/icons-vue'
import { api } from '../api'
import { useApp } from '../store'
import Chart from '../components/Chart.vue'

const app = useApp()
const router = useRouter()
const busy = ref(false)
const saving = ref(false)

// 数据状态
const tenants = ref<any[]>([])
const tenantStats = ref<{ total: number; total_used_mb: number; total_quota_mb: number }>({
  total: 0,
  total_used_mb: 0,
  total_quota_mb: 0
})
const searchKeyword = ref('')
const statusFilter = ref('')

// 弹窗控制
const tenantOpen = ref(false)
const storageOpen = ref(false)
const pwdOpen = ref(false)

// 新建租户表单
const tenantForm = reactive({
  code: '',
  name: '',
  max_storage_mb: 500,
  admin_password: 'qq13608472109',
  note: ''
})

// 配额调整表单
const storageForm = reactive({
  code: '',
  name: '',
  max_storage_mb: 500
})

// 密码重置表单
const pwdForm = reactive({
  code: '',
  name: '',
  password: 'qq13608472109'
})

// 快捷配额预设
const quotaPresets = [
  { label: '200 MB', value: 200 },
  { label: '500 MB (标准)', value: 500 },
  { label: '1024 MB (1 GB)', value: 1024 },
  { label: '2048 MB (2 GB)', value: 2048 },
  { label: '不限容量', value: 0 }
]

async function load() {
  busy.value = true
  try {
    const res = await api('/system/tenants')
    tenants.value = res.items || res.tenants || []
    tenantStats.value = {
      total: res.total || tenants.value.length,
      total_used_mb: res.total_used_mb || 0,
      total_quota_mb: res.total_quota_mb || 0
    }
    app.tenantList = tenants.value
  } catch (err: any) {
    ElMessage.error(err?.message || '加载平台租户数据失败')
  } finally {
    busy.value = false
  }
}

onMounted(load)
watch(() => app.tick, load)

// 活跃与告警计算
const activeCount = computed(() => tenants.value.filter(t => t.status === 'active').length)
const disabledCount = computed(() => tenants.value.filter(t => t.status === 'disabled').length)
const warningTenants = computed(() => {
  return tenants.value.filter(t => {
    if (!t.max_storage_mb || t.max_storage_mb <= 0) return false
    const used = t.used_storage_mb || 0
    return (used / t.max_storage_mb) >= 0.8
  })
})

const overallUsagePercent = computed(() => {
  if (!tenantStats.value.total_quota_mb || tenantStats.value.total_quota_mb <= 0) return 0
  const pct = Math.round((tenantStats.value.total_used_mb / tenantStats.value.total_quota_mb) * 100)
  return Math.min(100, Math.max(0, pct))
})

// 过滤后的租户列表
const filteredTenants = computed(() => {
  return tenants.value.filter(t => {
    const q = searchKeyword.value.toLowerCase().trim()
    const matchSearch = !q ||
      t.code.toLowerCase().includes(q) ||
      t.name.toLowerCase().includes(q) ||
      (t.note && t.note.toLowerCase().includes(q))
    const matchStatus = !statusFilter.value || t.status === statusFilter.value
    return matchSearch && matchStatus
  })
})

// 图表配置：租户空间占用比例
const pieOption = computed(() => {
  const data = tenants.value.map(t => ({
    name: t.name || t.code,
    value: Number((t.used_storage_mb || 0).toFixed(2))
  })).filter(item => item.value > 0)

  if (!data.length) {
    return {
      title: { text: '暂无租户存储数据', left: 'center', top: 'middle', textStyle: { color: '#94a3b8', fontSize: 13 } }
    }
  }

  return {
    tooltip: { trigger: 'item', formatter: '{b}: {c} MB ({d}%)' },
    legend: { orient: 'vertical', right: 10, top: 'middle', textStyle: { fontSize: 11, color: '#64748b' } },
    series: [
      {
        name: '物理存储占用',
        type: 'pie',
        radius: ['45%', '72%'],
        center: ['38%', '50%'],
        avoidLabelOverlap: false,
        itemStyle: { borderRadius: 6, borderColor: '#fff', borderWidth: 2 },
        label: { show: false },
        emphasis: { label: { show: true, fontSize: 13, fontWeight: 'bold' } },
        data
      }
    ]
  }
})

// 跨租户快速监管
async function handleSwitch(code: string) {
  try {
    await app.switchTenant(code)
    ElMessage.success(`已切换至租户空间：${code}`)
    router.push('/')
  } catch (err: any) {
    ElMessage.error(err?.message || '跨租户切换失败')
  }
}

// 快速新建租户
function openCreateTenant() {
  tenantForm.code = ''
  tenantForm.name = ''
  tenantForm.max_storage_mb = 500
  tenantForm.admin_password = 'qq13608472109'
  tenantForm.note = ''
  tenantOpen.value = true
}

async function handleCreateTenant() {
  const code = tenantForm.code.trim()
  const name = tenantForm.name.trim()
  if (!code || !name) {
    ElMessage.warning('请填写租户代码与租户名称')
    return
  }
  saving.value = true
  try {
    await api('/system/tenants', 'POST', tenantForm)
    ElMessage.success(`租户空间【${name}】创建成功，专属用户ID为：${code}`)
    tenantOpen.value = false
    await load()
  } catch (err: any) {
    ElMessage.error(err?.message || '创建租户失败')
  } finally {
    saving.value = false
  }
}

// 快捷修改配额
function openStorageModal(t: any) {
  storageForm.code = t.code
  storageForm.name = t.name
  storageForm.max_storage_mb = t.max_storage_mb
  storageOpen.value = true
}

async function handleSaveStorage() {
  saving.value = true
  try {
    await api(`/system/tenants/${storageForm.code}/storage`, 'PUT', { max_storage_mb: storageForm.max_storage_mb })
    ElMessage.success(`租户【${storageForm.name}】配额调整成功`)
    storageOpen.value = false
    await load()
  } catch (err: any) {
    ElMessage.error(err?.message || '配额调整失败')
  } finally {
    saving.value = false
  }
}

// 快捷重置租户密码
function openPasswordModal(t: any) {
  pwdForm.code = t.code
  pwdForm.name = t.name
  pwdForm.password = 'qq13608472109'
  pwdOpen.value = true
}

async function handleResetPassword() {
  if (!pwdForm.password || pwdForm.password.length < 8) {
    ElMessage.warning('重置密码长度至少须为 8 位')
    return
  }
  saving.value = true
  try {
    await api(`/system/tenants/${pwdForm.code}/reset-password`, 'POST', { password: pwdForm.password })
    ElMessage.success(`租户【${pwdForm.name}】专属管理员密码已重置`)
    pwdOpen.value = false
  } catch (err: any) {
    ElMessage.error(err?.message || '重置密码失败')
  } finally {
    saving.value = false
  }
}

// 切换启停状态
async function handleToggleStatus(t: any) {
  const isDisable = t.status === 'active'
  const actionText = isDisable ? '停用（禁止登录）' : '启用'
  try {
    await ElMessageBox.confirm(
      `确定要将租户【${t.name}】(${t.code}) 状态设置为：${actionText} 吗？`,
      '修改租户状态',
      { type: isDisable ? 'warning' : 'info', confirmButtonText: '确定' }
    )
    await api(`/system/tenants/${t.code}/status`, 'POST', { status: isDisable ? 'disabled' : 'active' })
    ElMessage.success(`已${actionText}租户【${t.name}】`)
    await load()
  } catch {}
}
</script>

<template>
  <div v-loading="busy" class="admin-dashboard">
    <!-- 顶部超级管理员专属中枢标题条 -->
    <div class="admin-hero">
      <div class="hero-left">
        <div class="admin-shield-icon">
          <el-icon><Monitor /></el-icon>
        </div>
        <div class="hero-title-box">
          <div class="hero-eyebrow">
            <span class="pulse-dot"></span>
            PLATFORM SUPERADMINISTRATION & TENANT FLEET CONSOLE
          </div>
          <h1>平台总控管理中枢 · 安全运维控制台</h1>
          <p class="hero-desc">
            欢迎管理员 <b>huxinkui</b>。平台已配置 Database-per-Tenant 物理数据库隔离，各租户在独立沙箱中安全运行。
          </p>
        </div>
      </div>
      <div class="hero-actions">
        <el-button type="primary" size="large" :icon="Plus" @click="openCreateTenant">
          新建租户空间
        </el-button>
        <el-button size="large" :icon="Setting" @click="router.push('/tenants')">
          多租户中心
        </el-button>
        <el-button size="large" :icon="Refresh" @click="load">
          刷新状态
        </el-button>
      </div>
    </div>

    <!-- 核心安全防御与加固横幅 -->
    <div class="security-banner">
      <div class="sec-left">
        <div class="sec-badge">
          <el-icon><Lock /></el-icon>
          安全加固已生效
        </div>
        <div class="sec-text">
          <b>超级管理员账号已配置为专属身份 <code>huxinkui</code></b>：默认 <code>admin</code> 账号已彻底封禁与注销，全面阻断自动化针对 admin 的字典爆破和公网探测扫描；IP 防暴力破解（8 次封禁 15 分钟）全面运行中。
        </div>
      </div>
      <div class="sec-tags">
        <el-tag type="success" effect="light" size="small">admin 已停用阻断</el-tag>
        <el-tag type="primary" effect="light" size="small">物理数据库隔离</el-tag>
        <el-tag type="warning" effect="light" size="small">租户管理页面已对租户隐藏</el-tag>
      </div>
    </div>

    <!-- 四大核心运营指标网格 -->
    <div class="admin-metric-grid">
      <div class="admin-metric-card">
        <div class="metric-top">
          <span class="m-label">租户空间总数</span>
          <div class="m-icon-box blue"><el-icon><OfficeBuilding /></el-icon></div>
        </div>
        <div class="m-num">{{ tenantStats.total }}<small>家</small></div>
        <div class="m-footer">
          <span>正常运行 <b>{{ activeCount }}</b> · 停用 <b>{{ disabledCount }}</b></span>
          <el-tag size="small" type="success">多空间健康</el-tag>
        </div>
      </div>

      <div class="admin-metric-card">
        <div class="metric-top">
          <span class="m-label">物理存储已用总容量</span>
          <div class="m-icon-box green"><el-icon><Folder /></el-icon></div>
        </div>
        <div class="m-num">{{ tenantStats.total_used_mb.toFixed(2) }}<small>MB</small></div>
        <div class="m-footer">
          <span>总配额 {{ tenantStats.total_quota_mb ? tenantStats.total_quota_mb + ' MB' : '不限' }} (使用率 {{ overallUsagePercent }}%)</span>
          <el-tag size="small" :type="overallUsagePercent > 80 ? 'danger' : 'primary'">动态分配</el-tag>
        </div>
      </div>

      <div class="admin-metric-card">
        <div class="metric-top">
          <span class="m-label">存储容量预警租户</span>
          <div class="m-icon-box" :class="warningTenants.length ? 'orange' : 'teal'">
            <el-icon><Warning /></el-icon>
          </div>
        </div>
        <div class="m-num" :style="{ color: warningTenants.length ? '#ea580c' : '#059669' }">
          {{ warningTenants.length }}<small>家</small>
        </div>
        <div class="m-footer">
          <span>{{ warningTenants.length ? '存在使用率 ≥ 80% 租户' : '所有租户配额均在安全阈值内' }}</span>
          <el-tag size="small" :type="warningTenants.length ? 'warning' : 'success'">
            {{ warningTenants.length ? '需扩容' : '状态良好' }}
          </el-tag>
        </div>
      </div>

      <div class="admin-metric-card">
        <div class="metric-top">
          <span class="m-label">平台防御与运行机制</span>
          <div class="m-icon-box purple"><el-icon><Cpu /></el-icon></div>
        </div>
        <div class="m-num text-guard">强效防御中</div>
        <div class="m-footer">
          <span>超级管理员专属：<b>huxinkui</b></span>
          <el-tag size="small" type="primary">WAL 高可用</el-tag>
        </div>
      </div>
    </div>

    <!-- 中间区域：租户空间快速总控与状态列表 -->
    <div class="fleet-section">
      <div class="fleet-header">
        <div class="fh-left">
          <h2>租户空间管控舰队 (Tenant Fleet)</h2>
          <p>实时监控平台下所有租户的物理数据库体积、配额使用率与运行状态，支持一键无感跨空间监管。</p>
        </div>
        <div class="fh-right">
          <el-input
            v-model="searchKeyword"
            :prefix-icon="Search"
            placeholder="搜索租户代码、名称或备注"
            clearable
            style="width: 260px;"
          />
          <el-select v-model="statusFilter" placeholder="状态筛选" clearable style="width: 120px;">
            <el-option label="全部状态" value="" />
            <el-option label="正常运行" value="active" />
            <el-option label="已停用" value="disabled" />
          </el-select>
        </div>
      </div>

      <el-table :data="filteredTenants" class="fleet-table" stripe border>
        <el-table-column label="租户代码与空间" min-width="160">
          <template #default="{ row }">
            <div class="fleet-tenant-code">
              <b>{{ row.code }}</b>
              <el-tag v-if="row.code === 'default'" size="small" type="primary">主管理空间</el-tag>
            </div>
            <div class="fleet-db-path">
              <code>{{ row.code === 'default' ? 'data/ruilian.db' : `data/tenants/${row.code}/ruilian.db` }}</code>
            </div>
          </template>
        </el-table-column>

        <el-table-column prop="name" label="企业/租户名称" min-width="160">
          <template #default="{ row }">
            <span class="tenant-display-name">{{ row.name }}</span>
            <div v-if="row.note" class="tenant-note">{{ row.note }}</div>
          </template>
        </el-table-column>

        <el-table-column label="管理员登录用户 ID" min-width="140">
          <template #default="{ row }">
            <div class="admin-user-pill">
              <el-icon><User /></el-icon>
              <span>{{ row.admin_user || (row.code === 'default' ? 'huxinkui' : row.code) }}</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="存储占用与配额" min-width="220">
          <template #default="{ row }">
            <div class="storage-cell">
              <div class="storage-text">
                <span>{{ (row.used_storage_mb || 0).toFixed(2) }} MB</span>
                <small>/ {{ row.max_storage_mb ? row.max_storage_mb + ' MB' : '不限容量' }}</small>
                <b :class="{ 'text-danger': row.storage_percent >= 80 }">({{ row.storage_percent || 0 }}%)</b>
              </div>
              <el-progress
                :percentage="row.storage_percent || 0"
                :status="row.storage_percent >= 95 ? 'exception' : row.storage_percent >= 80 ? 'warning' : ''"
                :stroke-width="6"
                :show-text="false"
              />
            </div>
          </template>
        </el-table-column>

        <el-table-column label="运行状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'danger'" size="small">
              {{ row.status === 'active' ? '正常运行' : '已停用' }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column label="管理操作" width="280" align="right">
          <template #default="{ row }">
            <div class="fleet-actions">
              <el-button
                v-if="row.code !== 'default'"
                size="small"
                type="primary"
                plain
                @click="handleSwitch(row.code)"
              >
                跨空间巡检
              </el-button>
              <el-button size="small" :icon="Setting" @click="openStorageModal(row)">
                配额
              </el-button>
              <el-button
                v-if="row.code !== 'default'"
                size="small"
                :icon="Key"
                @click="openPasswordModal(row)"
              >
                重置密码
              </el-button>
              <el-button
                v-if="row.code !== 'default'"
                size="small"
                :type="row.status === 'active' ? 'danger' : 'success'"
                link
                @click="handleToggleStatus(row)"
              >
                {{ row.status === 'active' ? '停用' : '启用' }}
              </el-button>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 下方分析网格：图表与安全架构体系 -->
    <div class="insights-grid">
      <!-- 存储分布图表 -->
      <div class="insight-panel">
        <div class="panel-header">
          <h3>各租户物理存储占用分布</h3>
          <span class="panel-sub">各租户 SQLite 数据文件物理占用</span>
        </div>
        <div class="chart-wrapper">
          <Chart :option="pieOption" :height="260" />
        </div>
      </div>

      <!-- 平台安全与架构说明 -->
      <div class="insight-panel security-info-panel">
        <div class="panel-header">
          <h3>平台安全与多租户架构标准</h3>
          <span class="panel-sub">SaaS 级强隔离与防攻击规范</span>
        </div>
        <div class="sec-points">
          <div class="sec-point-item">
            <div class="point-icon"><el-icon><Lock /></el-icon></div>
            <div class="point-content">
              <b>管理员专属账号防护体系</b>
              <p>平台管理员账号已变更为 <code>huxinkui</code>，原有 <code>admin</code> 账号全面作废阻断，杜绝黑客扫描器利用默认账号发起的大规模撞库与扫描风险。</p>
            </div>
          </div>

          <div class="sec-point-item">
            <div class="point-icon"><el-icon><Folder /></el-icon></div>
            <div class="point-content">
              <b>Database-per-Tenant 物理数据库独立</b>
              <p>各租户拥有独立的 SQLite 数据库文件（如 <code>data/tenants/xuyonghong/ruilian.db</code>），实现真正的物理文件级数据隔离，彻底消除逻辑隔离的越权漏洞。</p>
            </div>
          </div>

          <div class="sec-point-item">
            <div class="point-icon"><el-icon><Lock /></el-icon></div>
            <div class="point-content">
              <b>租户权限边界与控制台隐藏</b>
              <p>所有普通租户均无法看到多租户管理页面，路由已配置强校验拦截，后端 API 仅允许超级管理员（huxinkui）访问，多租户管理中枢对租户完全透明隔离。</p>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 新建租户弹窗 -->
    <el-dialog v-model="tenantOpen" title="新建独立租户空间" width="560px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="租户代码 (系统唯一标识，用于生成租户独立数据库目录)" required>
          <el-input
            v-model="tenantForm.code"
            placeholder="例如：corp_bj、xuyonghong（字母、数字、下划线）"
            maxlength="32"
          />
        </el-form-item>
        <el-form-item label="企业 / 租户名称" required>
          <el-input v-model="tenantForm.name" placeholder="例如：北京分公司、徐永红商行" maxlength="60" />
        </el-form-item>
        <el-form-item label="存储配额 (MB)">
          <div style="display: flex; gap: 8px; align-items: center; width: 100%;">
            <el-input-number v-model="tenantForm.max_storage_mb" :min="0" :max="102400" :step="100" style="width: 180px;" />
            <span style="color: #64748b; font-size: 13px;">0 为不限制存储容量</span>
          </div>
          <div style="margin-top: 8px; display: flex; gap: 6px; flex-wrap: wrap;">
            <el-button
              v-for="p in quotaPresets"
              :key="p.value"
              size="small"
              :type="tenantForm.max_storage_mb === p.value ? 'primary' : 'default'"
              @click="tenantForm.max_storage_mb = p.value"
            >
              {{ p.label }}
            </el-button>
          </div>
        </el-form-item>
        <el-form-item label="租户专属管理员初始密码" required>
          <el-input v-model="tenantForm.admin_password" type="password" show-password placeholder="不少于 8 位，包含数字和非数字字符" />
          <div class="form-tip">
            该租户专属登录用户ID为 <code>{{ tenantForm.code.trim() || '租户代码' }}</code>。租户不可使用系统管理员账号 (huxinkui) 登录。
          </div>
        </el-form-item>
        <el-form-item label="备注说明">
          <el-input v-model="tenantForm.note" type="textarea" :rows="2" placeholder="填写租户签约、业务归属或备注信息" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="tenantOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleCreateTenant">确认创建</el-button>
      </template>
    </el-dialog>

    <!-- 调整配额弹窗 -->
    <el-dialog v-model="storageOpen" :title="'调整存储配额 - ' + storageForm.name" width="460px">
      <el-form label-position="top">
        <el-form-item label="存储空间上限 (MB)">
          <el-input-number v-model="storageForm.max_storage_mb" :min="0" :max="102400" :step="100" style="width: 100%;" />
        </el-form-item>
        <div style="margin-top: 8px; display: flex; gap: 6px; flex-wrap: wrap;">
          <el-button
            v-for="p in quotaPresets"
            :key="p.value"
            size="small"
            :type="storageForm.max_storage_mb === p.value ? 'primary' : 'default'"
            @click="storageForm.max_storage_mb = p.value"
          >
            {{ p.label }}
          </el-button>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="storageOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleSaveStorage">保存配额</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码弹窗 -->
    <el-dialog v-model="pwdOpen" :title="'重置管理员密码 - ' + pwdForm.name" width="440px">
      <el-form label-position="top">
        <el-alert
          :title="`正在为租户专属管理员【${pwdForm.code}】重置登录密码`"
          type="warning"
          :closable="false"
          style="margin-bottom: 14px;"
        />
        <el-form-item label="新密码" required>
          <el-input v-model="pwdForm.password" type="password" show-password placeholder="至少 8 位，包含数字和非数字字符" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="pwdOpen = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="handleResetPassword">确认重置</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.admin-dashboard {
  display: flex;
  flex-direction: column;
  gap: 20px;
}

/* 顶部超级管理员专属中枢标题条 */
.admin-hero {
  background: linear-gradient(135deg, #1e293b 0%, #0f172a 100%);
  border-radius: 12px;
  padding: 24px 28px;
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: space-between;
  box-shadow: 0 4px 16px rgba(15, 23, 42, 0.12);
  flex-wrap: wrap;
  gap: 16px;
}
.hero-left {
  display: flex;
  align-items: center;
  gap: 20px;
}
.admin-shield-icon {
  width: 56px;
  height: 56px;
  background: linear-gradient(135deg, #3b82f6 0%, #1d4ed8 100%);
  border-radius: 14px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 28px;
  color: #fff;
  box-shadow: 0 4px 12px rgba(59, 130, 246, 0.35);
}
.hero-eyebrow {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 11px;
  letter-spacing: 1px;
  color: #94a3b8;
  font-weight: 600;
  margin-bottom: 4px;
}
.pulse-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #22c55e;
  box-shadow: 0 0 0 3px rgba(34, 197, 94, 0.3);
  animation: pulse 2s infinite;
}
@keyframes pulse {
  0% { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0.5); }
  70% { box-shadow: 0 0 0 8px rgba(34, 197, 94, 0); }
  100% { box-shadow: 0 0 0 0 rgba(34, 197, 94, 0); }
}
.hero-title-box h1 {
  margin: 0;
  font-size: 22px;
  font-weight: 700;
  color: #f8fafc;
}
.hero-desc {
  margin: 6px 0 0;
  font-size: 13px;
  color: #cbd5e1;
}
.hero-desc b {
  color: #60a5fa;
}
.hero-actions {
  display: flex;
  gap: 10px;
}

/* 安全加固横幅 */
.security-banner {
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  border-left: 4px solid #3b82f6;
  border-radius: 8px;
  padding: 14px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}
.sec-left {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.sec-badge {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  background: #eff6ff;
  color: #1d4ed8;
  border: 1px solid #bfdbfe;
  font-size: 12px;
  font-weight: 600;
  padding: 3px 8px;
  border-radius: 4px;
}
.sec-text {
  font-size: 13px;
  color: #334155;
}
.sec-text code {
  background: #e2e8f0;
  padding: 1px 5px;
  border-radius: 3px;
  color: #0f172a;
  font-weight: bold;
}
.sec-tags {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

/* 四大指标卡片 */
.admin-metric-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 16px;
}
@media (max-width: 1100px) {
  .admin-metric-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}
@media (max-width: 600px) {
  .admin-metric-grid {
    grid-template-columns: 1fr;
  }
}
.admin-metric-card {
  background: #fff;
  border-radius: 10px;
  border: 1px solid #e2e8f0;
  padding: 18px 20px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.02);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  transition: all 0.2s;
}
.admin-metric-card:hover {
  box-shadow: 0 4px 12px rgba(0,0,0,0.06);
  border-color: #cbd5e1;
}
.metric-top {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.m-label {
  font-size: 13px;
  color: #64748b;
  font-weight: 500;
}
.m-icon-box {
  width: 36px;
  height: 36px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
}
.m-icon-box.blue { background: #eff6ff; color: #3b82f6; }
.m-icon-box.green { background: #f0fdf4; color: #22c55e; }
.m-icon-box.orange { background: #fff7ed; color: #f97316; }
.m-icon-box.teal { background: #f0fdfa; color: #0d9488; }
.m-icon-box.purple { background: #faf5ff; color: #a855f7; }

.m-num {
  font-size: 28px;
  font-weight: 700;
  color: #0f172a;
  margin: 12px 0 8px;
  line-height: 1;
}
.m-num small {
  font-size: 14px;
  font-weight: 400;
  color: #94a3b8;
  margin-left: 4px;
}
.m-num.text-guard {
  font-size: 20px;
  color: #2563eb;
  padding-top: 6px;
}
.m-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 12px;
  color: #64748b;
  border-top: 1px solid #f1f5f9;
  padding-top: 10px;
}
.m-footer b {
  color: #0f172a;
}

/* 租户管控舰队 */
.fleet-section {
  background: #fff;
  border-radius: 10px;
  border: 1px solid #e2e8f0;
  padding: 20px 24px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.02);
}
.fleet-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 18px;
  flex-wrap: wrap;
  gap: 12px;
}
.fh-left h2 {
  margin: 0;
  font-size: 17px;
  font-weight: 700;
  color: #0f172a;
}
.fh-left p {
  margin: 4px 0 0;
  font-size: 13px;
  color: #64748b;
}
.fh-right {
  display: flex;
  gap: 10px;
}
.fleet-tenant-code {
  display: flex;
  align-items: center;
  gap: 8px;
}
.fleet-db-path code {
  font-size: 11px;
  color: #64748b;
  background: #f1f5f9;
  padding: 1px 4px;
  border-radius: 3px;
}
.tenant-display-name {
  font-weight: 600;
  color: #1e293b;
}
.tenant-note {
  font-size: 11px;
  color: #94a3b8;
  margin-top: 2px;
}
.admin-user-pill {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  background: #f8fafc;
  border: 1px solid #e2e8f0;
  padding: 2px 8px;
  border-radius: 12px;
  font-size: 12px;
  color: #334155;
  font-weight: 500;
}
.storage-cell {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.storage-text {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
  color: #334155;
}
.storage-text small {
  color: #94a3b8;
}
.text-danger {
  color: #ef4444;
}
.fleet-actions {
  display: flex;
  justify-content: flex-end;
  gap: 6px;
}

/* 分析网格 */
.insights-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}
@media (max-width: 900px) {
  .insights-grid {
    grid-template-columns: 1fr;
  }
}
.insight-panel {
  background: #fff;
  border-radius: 10px;
  border: 1px solid #e2e8f0;
  padding: 20px 24px;
}
.panel-header {
  margin-bottom: 14px;
}
.panel-header h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 700;
  color: #0f172a;
}
.panel-sub {
  font-size: 12px;
  color: #64748b;
}
.chart-wrapper {
  min-height: 260px;
}
.sec-points {
  display: flex;
  flex-direction: column;
  gap: 14px;
  margin-top: 8px;
}
.sec-point-item {
  display: flex;
  gap: 12px;
  align-items: flex-start;
  background: #f8fafc;
  padding: 12px 14px;
  border-radius: 8px;
  border: 1px solid #f1f5f9;
}
.point-icon {
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: #eff6ff;
  color: #2563eb;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 16px;
  flex-shrink: 0;
}
.point-content b {
  font-size: 13px;
  color: #0f172a;
  display: block;
  margin-bottom: 3px;
}
.point-content p {
  margin: 0;
  font-size: 12px;
  color: #64748b;
  line-height: 1.5;
}
.point-content code {
  background: #e2e8f0;
  padding: 1px 4px;
  border-radius: 3px;
  color: #0f172a;
}
.form-tip {
  margin-top: 6px;
  font-size: 12px;
  color: #64748b;
}
.form-tip code {
  background: #f1f5f9;
  padding: 1px 4px;
  border-radius: 3px;
  color: #2563eb;
  font-weight: bold;
}
</style>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Lock, User, OfficeBuilding, ArrowRight, Connection, CircleCheck, Files, Key, RefreshRight } from '@element-plus/icons-vue'
import { api, getTenantCode, setTenantCode, setToken } from '../api'
import { useApp } from '../store'
import BrandMark from './BrandMark.vue'

const app = useApp(), router = useRouter(), route = useRoute(), busy = ref(false)
const savedTenant = getTenantCode()
const initialTenant = savedTenant === 'default' ? '' : savedTenant
const form = reactive({
  tenant: initialTenant,
  username: initialTenant ? initialTenant : 'huxinkui',
  password: '',
  confirm: '',
  company_name: '',
  captcha_id: '',
  captcha_code: ''
})

// 当用户输入或修改租户空间代码时，智能同步专属用户 ID
watch(() => form.tenant, (newVal, oldVal) => {
  const t = (newVal || '').trim()
  const old = (oldVal || '').trim()
  if (t && t !== 'default' && (!form.username || form.username === 'huxinkui' || form.username === 'admin' || form.username === old)) {
    form.username = t
  } else if (!t && form.username === old) {
    form.username = 'huxinkui'
  }
})

const captchaImage = ref('')
const captchaLoading = ref(false)
const lockErrorMessage = ref('')

async function refreshCaptcha() {
  captchaLoading.value = true
  try {
    const res = await api<{ captcha_id: string; captcha_image: string }>('/auth/captcha', 'GET', undefined, true)
    form.captcha_id = res.captcha_id
    captchaImage.value = res.captcha_image
    form.captcha_code = ''
  } catch (err) {
    console.error('获取验证码失败', err)
  } finally {
    captchaLoading.value = false
  }
}

onMounted(() => {
  refreshCaptcha()
})

const valid = computed(() =>
  form.username.trim().length >= 2 &&
  form.password.length > 0 &&
  form.captcha_code.trim().length >= 4 &&
  (!app.needsSetup || (form.password.length >= 8 && form.company_name.trim() && form.password === form.confirm))
)

async function login() {
  if (!valid.value || busy.value) return
  const tenantCode = form.tenant.trim()
  const uname = form.username.trim()
  if (tenantCode && tenantCode !== 'default' && (uname === 'huxinkui' || uname === 'admin')) {
    ElMessage.warning('租户不能以管理员用户登录，管理员仅供系统后台登录；租户请使用专属用户ID登录')
    return
  }
  busy.value = true
  try {
    if (app.needsSetup) { await api('/setup', 'POST', form); app.needsSetup = false }
    const res = await api('/auth/login', 'POST', {
      ...form,
      username: uname,
      tenant: tenantCode,
      tenant_code: tenantCode
    })
    if (res.token) setToken(res.token)
    if (res.tenant_code) {
      setTenantCode(res.tenant_code)
      form.tenant = res.tenant_code === 'default' ? '' : res.tenant_code
    }
    if (res.user) app.user = res.user
    if (res.license) app.license = res.license
    await app.refreshMe().catch(() => {})
    ElMessage.success('登录成功')
    await router.push('/')
  } catch (err: any) {
    const msg = err?.message || '登录失败，请检查用户名、密码或验证码'
    ElMessage.error(msg)
    if (msg.includes('禁止登录') || msg.includes('锁定')) {
      lockErrorMessage.value = msg
    } else {
      lockErrorMessage.value = ''
    }
    await refreshCaptcha()
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login-root"><div class="login-screen">
    <section class="login-story">
      <div class="brand large"><BrandMark/><div :title="app.branding.app_name">{{ app.branding.brand_short }}<small>进销存与往来管理</small></div></div>
      <div class="story-body">
        <div class="eyebrow">CONNECTED BUSINESS, CLEARER DECISIONS</div>
        <h1>把业务连起来，<br>让经营看得清。</h1>
        <p>从每一次采购，到每一笔回款。<br>让商品、库存与往来账务，形成完整的业务闭环。</p>
        <div class="story-features">
          <span><el-icon><Connection /></el-icon>多租户空间隔离</span>
          <span><el-icon><CircleCheck /></el-icon>每笔业务可追溯</span>
          <span><el-icon><Lock /></el-icon>配额与空间管理</span>
        </div>
        <div class="story-art">
          <i></i><i></i><i></i>
          <div class="art-card">
            <span>BUSINESS IN SYNC</span>
            <b>采购 → 库存 → 销售</b>
            <small>应付 · 付款 &nbsp; / &nbsp; 应收 · 回款</small>
          </div>
        </div>
      </div>
      <footer>{{ app.branding.app_name }} <span>V1.1</span></footer>
    </section>

    <section class="login-form-side">
      <div class="login-card">
        <div class="login-badge">{{ app.needsSetup ? '开启 10 天完整试用' : (app.tenant?.name || app.company) }}</div>
        <h2>{{ app.needsSetup ? '欢迎使用' + app.branding.brand_short : '欢迎回来' }}</h2>
        <p class="muted">{{ app.needsSetup ? '设置企业信息与管理员账号，开始管理您的业务。' : '登录租户账号，查看今天的经营动态。' }}</p>

        <el-alert
          v-if="lockErrorMessage"
          :title="lockErrorMessage"
          type="error"
          show-icon
          :closable="false"
          style="margin-bottom: 18px;"
        />

        <el-form label-position="top" @submit.prevent="login" class="login-form">
          <el-form-item label="企业/租户空间">
            <el-input v-model="form.tenant" :prefix-icon="Files" placeholder="留空由用户ID自动识别，或输入租户空间代码 (如 xuyonghong)" size="large" maxlength="32" clearable @keyup.enter="login" />
          </el-form-item>

          <el-form-item v-if="app.needsSetup" label="企业名称">
            <el-input v-model="form.company_name" :prefix-icon="OfficeBuilding" placeholder="请输入企业名称" size="large" maxlength="80" @keyup.enter="login" />
          </el-form-item>

          <el-form-item label="用户名 / 租户用户ID">
            <el-input
              v-model="form.username"
              :prefix-icon="User"
              :placeholder="form.tenant ? '请输入租户用户ID (如 ' + form.tenant + ')' : '租户用户ID (如 xuyonghong) 或管理员 huxinkui'"
              size="large"
              autocomplete="username"
              maxlength="64"
              @keyup.enter="login"
            />
          </el-form-item>

          <el-form-item label="密码">
            <el-input v-model="form.password" :prefix-icon="Lock" :placeholder="app.needsSetup ? '至少 8 位，包含数字和非数字字符' : '请输入密码'" type="password" show-password size="large" :autocomplete="app.needsSetup ? 'new-password' : 'current-password'" @keyup.enter="login" />
          </el-form-item>

          <el-form-item v-if="app.needsSetup" label="确认密码">
            <el-input v-model="form.confirm" :prefix-icon="Lock" placeholder="再次输入密码" type="password" show-password size="large" autocomplete="new-password" @keyup.enter="login" />
          </el-form-item>

          <el-form-item label="安全验证码">
            <div class="captcha-row">
              <el-input
                v-model="form.captcha_code"
                :prefix-icon="Key"
                placeholder="请输入4位验证码"
                size="large"
                maxlength="6"
                autocomplete="off"
                @keyup.enter="login"
              />
              <div
                class="captcha-img-box"
                :class="{ loading: captchaLoading }"
                title="看不清？点击刷新验证码"
                @click="refreshCaptcha"
              >
                <img v-if="captchaImage" :src="captchaImage" alt="验证码" class="captcha-img" />
                <span v-else class="captcha-placeholder">加载中...</span>
                <el-icon class="captcha-refresh-icon"><RefreshRight /></el-icon>
              </div>
            </div>
          </el-form-item>

          <el-button class="login-submit" type="primary" size="large" :loading="busy" :disabled="!valid" native-type="submit" @click="login">
            {{ app.needsSetup ? '初始化并进入系统' : '登录系统' }}
            <el-icon><ArrowRight /></el-icon>
          </el-button>
        </el-form>

        <p class="login-note"><el-icon><Lock /></el-icon>多租户 B/S 架构 · 各租户专属登录 · 物理数据库隔离</p>
      </div>
      <div class="login-copyright">{{ app.branding.app_name }}</div>
    </section>
  </div></div>
</template>

<style scoped>
.login-root { min-height: 100vh; display: flex; flex-direction: column; }
.login-root .login-screen { min-height: 0; flex: 1; }
@media (max-width: 680px) { .login-root .login-form-side { min-height: calc(100vh - 74px); } }

.captcha-row {
  display: flex;
  gap: 10px;
  align-items: center;
  width: 100%;
}
.captcha-row :deep(.el-input) {
  flex: 1;
}
.captcha-img-box {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  height: 40px;
  width: 130px;
  min-width: 130px;
  cursor: pointer;
  background: #f8fafc;
  border: 1px solid #cbd5e1;
  border-radius: 6px;
  overflow: hidden;
  position: relative;
  user-select: none;
  transition: all 0.2s ease;
  box-sizing: border-box;
}
.captcha-img-box:hover {
  border-color: #3b82f6;
  box-shadow: 0 0 0 2px rgba(59, 130, 246, 0.15);
}
.captcha-img-box.loading {
  opacity: 0.6;
  pointer-events: none;
}
.captcha-img {
  width: 100%;
  height: 100%;
  display: block;
}
.captcha-refresh-icon {
  position: absolute;
  right: 4px;
  bottom: 4px;
  font-size: 13px;
  color: #475569;
  background: rgba(255, 255, 255, 0.9);
  border-radius: 50%;
  padding: 2px;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1);
  transition: transform 0.3s ease, color 0.2s ease;
}
.captcha-img-box:hover .captcha-refresh-icon {
  color: #2563eb;
  transform: rotate(180deg);
}
.captcha-placeholder {
  font-size: 12px;
  color: #94a3b8;
}
</style>

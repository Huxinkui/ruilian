<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Clock, Key } from '@element-plus/icons-vue'
import { useApp } from '../store'
const app = useApp(), router = useRouter()
const trial = computed(() => app.license.status === 'trial')
const deadline = computed(() => app.license.trial_end ? new Date(app.license.trial_end).toLocaleString('zh-CN', { year:'numeric', month:'2-digit', day:'2-digit', hour:'2-digit', minute:'2-digit', hour12:false }) : '')
function activate() {
  app.editor.visible = false
  router.push('/system?tab=license')
  if (!app.user) ElMessage.info('请先登录，登录后将自动打开软件授权页面')
}
</script>
<template>
  <div v-if="app.license.status && app.license.status !== 'active'" class="trial-banner" :class="{ expired: !trial }" role="status" aria-label="软件试用与授权提示">
    <el-icon><Clock/></el-icon>
    <div class="trial-banner-copy"><strong>{{ trial ? `试用期：剩余 ${app.license.days_remaining} 天` : app.license.status === 'expired' ? '试用期已结束' : '软件授权需要处理' }}</strong><span>{{ trial ? `试用至 ${deadline}，激活后可永久使用` : '可继续查看、导出与备份，请激活正式授权' }}</span></div>
    <el-button type="warning" size="small" :icon="Key" @click="activate">立即授权</el-button>
  </div>
</template>
<style scoped>
.trial-banner{display:flex;align-items:center;gap:12px;flex-shrink:0;min-height:52px;padding:10px 28px;border-bottom:1px solid #f5d58f;background:#fff7e5;color:#825400;font-size:12px}.trial-banner>.el-icon{font-size:18px}.trial-banner-copy{display:flex;align-items:center;gap:18px;flex:1;flex-wrap:wrap}.trial-banner-copy strong{font-size:13px}.trial-banner-copy span{color:#98702c}.trial-banner.expired{background:#fff0eb;border-color:#f1c4b4;color:#ae442c}.trial-banner .el-button{font-weight:600}.trial-banner-copy{min-width:0}@media(max-width:680px){.trial-banner{padding:9px 13px;gap:8px}.trial-banner-copy{gap:3px}.trial-banner-copy span{font-size:10px}.trial-banner-copy strong{font-size:11px}}@media print{.trial-banner{display:none!important}}
</style>

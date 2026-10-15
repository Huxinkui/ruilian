import { createRouter, createWebHashHistory } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useApp } from './store'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: () => import('./pages/Dashboard.vue'), meta: { title: '工作台' } },
    { path: '/masters/:kind?', component: () => import('./pages/Masters.vue'), meta: { title: '基础资料' } },
    { path: '/purchase', component: () => import('./pages/Documents.vue'), props: { module: 'purchase' }, meta: { title: '采购管理' } },
    { path: '/sales', component: () => import('./pages/Documents.vue'), props: { module: 'sales' }, meta: { title: '销售管理' } },
    { path: '/inventory', component: () => import('./pages/Inventory.vue'), meta: { title: '库存管理' } },
    { path: '/finance', component: () => import('./pages/Finance.vue'), meta: { title: '往来管理' } },
    { path: '/prices', component: () => import('./pages/Prices.vue'), meta: { title: '价格管理' } },
    { path: '/reports', component: () => import('./pages/Reports.vue'), meta: { title: '报表中心' } },
    { path: '/warnings', component: () => import('./pages/Warnings.vue'), meta: { title: '预警中心' } },
    { path: '/tenants', component: () => import('./pages/Tenants.vue'), meta: { title: '多租户管理', requiresMasterAdmin: true } },
    { path: '/system', component: () => import('./pages/System.vue'), meta: { title: '系统管理' } },
    { path: '/:pathMatch(.*)*', redirect: '/' }
  ]
})

router.beforeEach((to, _from, next) => {
  if (to.path === '/tenants') {
    const app = useApp()
    if (app.user) {
      const isMaster = (app.user.username === 'huxinkui' || app.user.is_master_admin) && (app.tenant?.code === 'default' || !app.tenant?.code)
      if (!isMaster) {
        ElMessage.warning('无权访问：租户空间无权查看多租户管理页面')
        return next('/')
      }
    }
  }
  next()
})


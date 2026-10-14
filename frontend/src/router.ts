import { createRouter, createWebHashHistory } from 'vue-router'

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', component: () => import('./pages/Dashboard.vue'), meta: { title: '经营工作台' } },
    { path: '/masters/:kind?', component: () => import('./pages/Masters.vue'), meta: { title: '基础资料' } },
    { path: '/purchase', component: () => import('./pages/Documents.vue'), props: { module: 'purchase' }, meta: { title: '采购管理' } },
    { path: '/sales', component: () => import('./pages/Documents.vue'), props: { module: 'sales' }, meta: { title: '销售管理' } },
    { path: '/inventory', component: () => import('./pages/Inventory.vue'), meta: { title: '库存管理' } },
    { path: '/finance', component: () => import('./pages/Finance.vue'), meta: { title: '往来管理' } },
    { path: '/prices', component: () => import('./pages/Prices.vue'), meta: { title: '价格管理' } },
    { path: '/reports', component: () => import('./pages/Reports.vue'), meta: { title: '报表中心' } },
    { path: '/warnings', component: () => import('./pages/Warnings.vue'), meta: { title: '预警中心' } },
    { path: '/system', component: () => import('./pages/System.vue'), meta: { title: '系统管理' } },
    { path: '/:pathMatch(.*)*', redirect: '/' }
  ]
})

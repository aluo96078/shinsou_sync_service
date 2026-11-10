import { createApp } from 'vue'
import App from './App.vue'
import { router, loginLocation } from './router'
import { onAuthLost } from './api/session'
import { applyLocaleToDocument, t } from './i18n'
import { applyTheme } from './stores/theme'
import { loadInstance } from './stores/instance'
import './styles/main.css'

applyTheme()
applyLocaleToDocument()

onAuthLost(() => {
  const current = router.currentRoute.value
  if (current.name === 'login') return
  void router.replace(loginLocation(current.fullPath))
})

void loadInstance()

const app = createApp(App)
app.config.globalProperties.$t = t
app.use(router).mount('#app')

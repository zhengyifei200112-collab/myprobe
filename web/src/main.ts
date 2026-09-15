import { createApp } from 'vue'
import { applyCachedAppearance, updateThemeColor } from './appearance'
import 'country-flag-icons/3x2/flags.css'
import App from './App.vue'
import AdminApp from './AdminApp.vue'
import ShareApp from './ShareApp.vue'
import './design-system/tokens.css'
import './design-system/base.css'
import './style.css'
import './public-dashboard/public-dashboard.css'
import './admin-dashboard/admin-dashboard.css'
import './settings-center/settings-center.css'
import './experience.css'

applyCachedAppearance()
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', updateThemeColor)
const root = location.pathname.startsWith('/admin') ? AdminApp : location.pathname.startsWith('/share/') ? ShareApp : App
createApp(root).mount('#app')

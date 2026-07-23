import { createApp } from 'vue'
import 'country-flag-icons/3x2/flags.css'
import App from './App.vue'
import AdminApp from './AdminApp.vue'
import ShareApp from './ShareApp.vue'
import './style.css'

const root = location.pathname.startsWith('/admin') ? AdminApp : location.pathname.startsWith('/share/') ? ShareApp : App
createApp(root).mount('#app')

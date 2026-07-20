import { createApp } from 'vue'
import App from './App.vue'
import AdminApp from './AdminApp.vue'
import './style.css'

createApp(location.pathname.startsWith('/admin') ? AdminApp : App).mount('#app')

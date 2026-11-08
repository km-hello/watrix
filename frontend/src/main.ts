import { createApp } from 'vue'
import App from './App.vue'
import './styles/global.css'

// 全局基础样式先加载，再挂载应用及组件内的局部样式。
createApp(App).mount('#app')

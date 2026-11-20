import { createApp, h } from 'vue'
import { NConfigProvider, NGlobalStyle, NMessageProvider, type GlobalThemeOverrides } from 'naive-ui'
import App from './App.vue'
import './styles/global.css'

const theme: GlobalThemeOverrides = {
  common: {
    primaryColor: '#278263',
    primaryColorHover: '#226E54',
    primaryColorPressed: '#226E54',
    primaryColorSuppl: '#278263',
    bodyColor: '#F6F9F7',
    cardColor: '#FFFFFF',
    textColorBase: '#3E5147',
    textColor1: '#263D32',
    textColor2: '#3E5147',
    textColor3: '#617167',
    borderColor: '#E0E8E2',
    borderRadius: '10px',
    fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif',
  },
}
createApp({
  render: () => h(NConfigProvider, { themeOverrides: theme }, {
    default: () => [h(NGlobalStyle), h(NMessageProvider, { placement: 'top', max: 1 }, { default: () => h(App) })],
  }),
}).mount('#app')

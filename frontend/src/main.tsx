import React from 'react'
import ReactDOM from 'react-dom/client'
import './index.css'
import App from './App'
import { useUiStore } from '@/store/ui-store'

useUiStore.getState().initTheme()
useUiStore.getState().initLang()

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)

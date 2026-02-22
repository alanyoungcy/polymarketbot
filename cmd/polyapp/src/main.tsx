import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './index.css'
import { RefreshProvider } from './hooks/useApi'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <RefreshProvider>
      <App />
    </RefreshProvider>
  </StrictMode>,
)

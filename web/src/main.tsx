import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { App } from './App'
import { choose, LanguageContext } from './i18n/language'
import './styles/theme.css'

const root = document.getElementById('root')
if (root === null) {
  throw new Error('convia: the page has no root element to mount into')
}

// The page speaks the first language the browser prefers that it knows, and says which.
const language = choose(navigator.languages)
document.documentElement.lang = language.tag

createRoot(root).render(
  <StrictMode>
    <LanguageContext.Provider value={language}>
      <App />
    </LanguageContext.Provider>
  </StrictMode>,
)

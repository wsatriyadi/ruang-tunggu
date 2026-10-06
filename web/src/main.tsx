import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { ToastProvider } from './components';
import './styles/theme.css';
import './styles/global.css';
import './styles/components.css';

const container = document.getElementById('root');
if (!container) {
  // The index.html ships the element, so this can only mean a broken build.
  throw new Error('Elemen #root tidak ditemukan pada halaman.');
}

createRoot(container).render(
  <StrictMode>
    <ToastProvider>
      <App />
    </ToastProvider>
  </StrictMode>,
);
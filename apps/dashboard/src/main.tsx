import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@cofy-x/camp-platinum/styles.css';
import './app/styles.css';
import { App } from './app/App';

const projectName = 'Camp';
document.title = projectName.startsWith('__')
  ? 'Platinum Desktop'
  : projectName;
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

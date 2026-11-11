import type { AppDefinition } from '@cofy-x/camp-platinum';
import { lazyApplication } from '../../shared/desktop/lazyApplication';

const Settings = lazyApplication(
  'src/features/settings/Settings.tsx',
  () => import('./Settings'),
  (module) => module.Settings,
);

export const settingsApp: AppDefinition = {
  id: 'settings',
  title: 'Settings',
  icon: 'settings',
  component: Settings,
  windowKey: () => 'main',
  defaultSize: { width: 850, height: 650 },
  minSize: { width: 360, height: 340 },
};

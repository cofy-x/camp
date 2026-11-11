import type { AppDefinition } from '@cofy-x/camp-platinum';
import { lazyApplication } from '../../shared/desktop/lazyApplication';

const Appearance = lazyApplication(
  'src/features/appearance/Appearance.tsx',
  () => import('./Appearance'),
  (module) => module.Appearance,
);

export const appearanceApp: AppDefinition = {
  id: 'appearance',
  title: 'Appearance',
  icon: 'appearance',
  component: Appearance,
  defaultSize: { width: 440, height: 360 },
  minSize: { width: 320, height: 320 },
};

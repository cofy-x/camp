import type { AppDefinition } from '@cofy-x/camp-platinum';
import { About } from './Welcome';

export const aboutApp: AppDefinition = {
  id: 'about',
  title: 'About This Desktop',
  icon: 'info',
  launchable: false,
  component: About,
  defaultSize: { width: 360, height: 340 },
  minSize: { width: 300, height: 310 },
};

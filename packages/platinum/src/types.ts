import type { ComponentType, ReactNode } from 'react';

export type IconName =
  | 'computer'
  | 'disk'
  | 'folder'
  | 'document'
  | 'checklist'
  | 'conversation'
  | 'room'
  | 'agent'
  | 'notes'
  | 'terminal'
  | 'trash'
  | 'settings'
  | 'appearance'
  | 'gallery'
  | 'image'
  | 'network'
  | 'info'
  | 'save';

export type WindowParams = Record<string, string>;
export type Point = { x: number; y: number };
export type Size = { width: number; height: number };

/** Shell dimensions stay optional for model callers using the desktop defaults. */
export interface DesktopViewport extends Size {
  menuHeight?: number;
  stripHeight?: number;
}

export type Bounds = Point & Size;

export interface DesktopWindow extends Bounds {
  id: string;
  appId: string;
  title: string;
  params: WindowParams;
  shaded: boolean;
  zoomed: boolean;
  restoreBounds?: Bounds;
}

export interface DesktopLayout {
  version: 1;
  windows: DesktopWindow[];
  activeWindowId: string | null;
  icons: Record<string, Point>;
}

export interface MenuItem {
  id?: string;
  label?: string;
  separator?: boolean;
  disabled?: boolean;
  checked?: boolean;
  shortcut?: string;
  action?: () => void;
  children?: MenuItem[];
}

export interface MenuGroup {
  id: string;
  label: string;
  items: MenuItem[];
}

export interface DialogOptions {
  title: string;
  message?: ReactNode;
  input?: { label: string; value?: string };
  buttons?: {
    label: string;
    value: string;
    default?: boolean;
    cancel?: boolean;
  }[];
}

export type DialogResult = { button: string; value: string } | null;

export interface DesktopAPI {
  windows: DesktopWindow[];
  activeWindowId: string | null;
  openApp: (
    appId: string,
    params?: WindowParams,
    options?: { title?: string },
  ) => string;
  closeWindow: (id: string) => Promise<boolean>;
  focusWindow: (id: string) => void;
  setTitle: (id: string, title: string) => void;
  showAllWindows: () => void;
  arrangeIcons: () => void;
  contextMenu: (items: MenuItem[], point: Point) => void;
  dialog: (options: DialogOptions) => Promise<DialogResult>;
  beforeClose: (
    id: string,
    handler: () => boolean | Promise<boolean>,
  ) => () => void;
}

export interface AppWindowProps {
  window: DesktopWindow;
  desktop: DesktopAPI;
}

export interface AppDefinition {
  id: string;
  title: string;
  icon: IconName;
  /** Whether to offer this application in the system launcher; defaults to true. */
  launchable?: boolean;
  defaultSize?: Size;
  minSize?: Size;
  windowKey?: (params: WindowParams) => string;
  component: ComponentType<AppWindowProps>;
  menu?: (window: DesktopWindow, desktop: DesktopAPI) => MenuGroup[];
}

export interface DesktopIcon {
  id: string;
  label: string;
  icon: IconName;
  appId?: string;
  params?: WindowParams;
  isTrash?: boolean;
  onOpen?: () => void;
}

export interface DesktopProps {
  apps: AppDefinition[];
  icons: DesktopIcon[];
  initialLayout?: DesktopLayout;
  initialWindows?: { appId: string; params?: WindowParams; title?: string }[];
  onLayoutChange?: (layout: DesktopLayout) => void;
  onTrashIcons?: (ids: string[]) => void;
  onDesktopMenu?: (ids: string[], desktop: DesktopAPI) => MenuItem[];
  wallpaper?: 'lavender' | 'grid' | 'solid';
  controlStrip?: boolean;
  onControlStripChange?: (expanded: boolean) => void;
  iconSnap?: boolean;
  connectionStatus?: 'connected' | 'connecting' | 'offline';
  onAppearance?: () => void;
  onAbout?: () => void;
  onHelp?: () => void;
  helpLabel?: string;
  onReady?: (desktop: DesktopAPI) => void;
}

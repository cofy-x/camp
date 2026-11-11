import {
  Component,
  Suspense,
  lazy,
  useEffect,
  useState,
  type ComponentType,
  type ReactNode,
} from 'react';
import {
  Button,
  useWindowVisibility,
  type AppWindowProps,
} from '@cofy-x/camp-platinum';

interface ManifestChunk {
  file: string;
  css?: string[];
  imports?: string[];
}

class ApplicationVersionError extends Error {
  constructor() {
    super('Camp was updated. Refresh Camp to open this window.');
  }
}

async function retryApplication<Module>(source: string): Promise<Module> {
  const retry = crypto.randomUUID();
  const base = new URL(import.meta.env.BASE_URL, document.baseURI);
  let moduleURL: URL;
  if (import.meta.env.DEV) moduleURL = new URL(source, base);
  else {
    const response = await fetch(new URL('.vite/manifest.json', base), {
      cache: 'no-store',
    });
    if (!response.ok)
      throw new Error('Could not load the application manifest.');
    const manifest = (await response.json()) as Record<string, ManifestChunk>;
    const entry = document.querySelector<HTMLScriptElement>(
      'script[type="module"][src]',
    );
    if (
      !entry ||
      new URL(entry.src).origin !==
        new URL(manifest['index.html'].file, base).origin ||
      new URL(entry.src).pathname !==
        new URL(manifest['index.html'].file, base).pathname
    )
      throw new ApplicationVersionError();
    const styles = new Set<string>();
    const visited = new Set<string>();
    const collect = (key: string) => {
      if (visited.has(key)) return;
      visited.add(key);
      const chunk = manifest[key];
      for (const dependency of chunk.imports ?? []) collect(dependency);
      for (const style of chunk.css ?? []) styles.add(style);
    };
    collect(source);
    await Promise.all(
      [...styles].map(async (style) => {
        const url = new URL(style, base);
        const loaded = Array.from(
          document.querySelectorAll<HTMLLinkElement>('link[rel="stylesheet"]'),
        ).some(
          (link) => new URL(link.href).pathname === url.pathname && link.sheet,
        );
        if (loaded) return;
        url.searchParams.set('retry', retry);
        await new Promise<void>((resolve, reject) => {
          const link = document.createElement('link');
          link.rel = 'stylesheet';
          link.href = url.href;
          link.onload = () => resolve();
          link.onerror = () => {
            link.remove();
            reject(new Error('Could not load this window’s styles.'));
          };
          document.head.append(link);
        });
      }),
    );
    moduleURL = new URL(manifest[source].file, base);
  }
  moduleURL.searchParams.set('retry', retry);
  return import(/* @vite-ignore */ moduleURL.href) as Promise<Module>;
}

class ApplicationLoadBoundary extends Component<
  { children: ReactNode; retry: () => void },
  { failed: boolean; message: string }
> {
  state = { failed: false, message: 'Could not open this window.' };

  static getDerivedStateFromError(error: unknown) {
    return {
      failed: true,
      message:
        error instanceof ApplicationVersionError
          ? error.message
          : 'Could not open this window.',
    };
  }

  render() {
    if (this.state.failed)
      return (
        <div className="camp-empty" role="alert">
          <p>{this.state.message}</p>
          <Button
            onClick={() => {
              this.props.retry();
              this.setState({ failed: false });
            }}
          >
            Try Again
          </Button>
        </div>
      );
    return this.props.children;
  }
}

export function lazyApplication<Module>(
  source: string,
  load: () => Promise<Module>,
  select: (module: Module) => ComponentType<AppWindowProps>,
) {
  let accepted: ComponentType<AppWindowProps> | undefined;
  const accept = (module: Module) => {
    const component = select(module);
    accepted = component;
    return { default: component };
  };
  const initial = () =>
    accepted ? Promise.resolve({ default: accepted }) : load().then(accept);
  return function LazyApplication(props: AppWindowProps) {
    const visible = useWindowVisibility();
    const [opened, setOpened] = useState(visible);
    const [Content, setContent] = useState(() => lazy(initial));
    useEffect(() => {
      if (visible) setOpened(true);
    }, [visible]);
    if (!opened && !visible) return null;
    return (
      <ApplicationLoadBoundary
        retry={() =>
          setContent(() =>
            lazy(() => retryApplication<Module>(source).then(accept)),
          )
        }
      >
        <Suspense
          fallback={
            <div className="camp-empty" role="status">
              Opening…
            </div>
          }
        >
          <Content {...props} />
        </Suspense>
      </ApplicationLoadBoundary>
    );
  };
}

import { useState, type ReactNode } from 'react';
import './reading.css';

export function ReadingDisclosure({
  label,
  children,
  className = '',
  preview,
  hidden = false,
  defaultOpen = false,
  onOpenChange,
}: {
  label: string;
  children: ReactNode;
  className?: string;
  preview?: ReactNode;
  hidden?: boolean;
  defaultOpen?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const [visited, setVisited] = useState(defaultOpen);

  return (
    <>
      {!hidden && !open && preview}
      <details
        className={`p9-disclosure app-reading-disclosure ${className}`}
        hidden={hidden}
        open={open}
        onToggle={(event) => {
          const next = event.currentTarget.open;
          if (next === open) return;
          setOpen(next);
          if (next) setVisited(true);
          onOpenChange?.(next);
        }}
      >
        <summary>{label}</summary>
        {visited && <div className="app-reading-content">{children}</div>}
      </details>
    </>
  );
}

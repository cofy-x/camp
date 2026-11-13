import { useId, useLayoutEffect, useRef, useState } from 'react';
import { Button } from '@cofy-x/camp-platinum';
import './goal-heading.css';

export function GoalHeading({
  goal,
  level = 3,
}: {
  goal: string;
  level?: 2 | 3;
}) {
  const id = useId();
  const heading = useRef<HTMLHeadingElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflow, setOverflow] = useState(false);
  useLayoutEffect(() => {
    const node = heading.current;
    if (!node || expanded) return;
    let active = true;
    const measure = () => {
      if (active) setOverflow(node.scrollHeight > node.clientHeight + 1);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    void document.fonts.ready.then(measure);
    return () => {
      active = false;
      observer.disconnect();
    };
  }, [goal, expanded]);
  const Heading = level === 2 ? 'h2' : 'h3';

  return (
    <div className="app-goal">
      <Heading
        ref={heading}
        id={id}
        className={`app-goal-heading${expanded ? ' is-expanded' : ''}`}
      >
        {goal}
      </Heading>
      {(overflow || expanded) && (
        <Button
          aria-controls={id}
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? 'Show less' : 'Show full goal'}
        </Button>
      )}
    </div>
  );
}

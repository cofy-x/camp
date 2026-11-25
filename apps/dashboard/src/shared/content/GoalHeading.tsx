import { goalTitle } from './goal-title';
import { MarkdownContent } from './MarkdownContent';
import { ReadingDisclosure } from './ReadingDisclosure';
import './goal-heading.css';

export function GoalDisclosure({
  goal,
  title,
  hidden,
}: {
  goal: string;
  title: string;
  hidden?: boolean;
}) {
  if (goal.trim() === title) return null;
  return (
    <ReadingDisclosure
      label="Full goal"
      className="goal-disclosure"
      hidden={hidden}
    >
      <div className="goal-prose">
        <MarkdownContent content={goal} />
      </div>
    </ReadingDisclosure>
  );
}

export function GoalHeading({
  goal,
  level = 3,
}: {
  goal: string;
  level?: 2 | 3;
}) {
  const Heading = level === 2 ? 'h2' : 'h3';
  const title = goalTitle(goal);
  return (
    <div className="app-goal">
      <Heading className="app-goal-heading">{title}</Heading>
      <GoalDisclosure goal={goal} title={title} />
    </div>
  );
}

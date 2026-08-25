import {
  Bot,
  CheckCircle2,
  ChevronDown,
  Circle,
  ListChecks,
  Target
} from "lucide-react";
import {useMemo, useState} from "react";

import type {
  AgentSummary,
  SessionPlanArtifact,
  TaskSummary
} from "../protocol";

export function SessionProgress({
  plan,
  tasks,
  agents,
  onOpenTrajectory
}: {
  plan?: SessionPlanArtifact;
  tasks: readonly TaskSummary[];
  agents: readonly AgentSummary[];
  onOpenTrajectory: () => void;
}) {
  const [expanded, setExpanded] = useState(true);
  const orderedTasks = useMemo(() => [...tasks].sort((left, right) =>
    taskRank(left.state) - taskRank(right.state)
  ), [tasks]);
  const activeTasks = orderedTasks.filter((task) => !isDone(task.state));
  const done = orderedTasks.length - activeTasks.length;
  if (!plan && tasks.length === 0 && agents.length === 0) return null;

  return (
    <section className="sessionProgress" aria-label="Session progress">
      <button
        type="button"
        className="sessionProgressSummary"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        <Target size={15} />
        <span>{goalTitle(plan, activeTasks)}</span>
        {tasks.length > 0 && <small>{done}/{tasks.length}</small>}
        {agents.length > 0 && <small>{agents.length} agents</small>}
        <ChevronDown size={15} data-expanded={expanded || undefined} />
      </button>
      {expanded && (
        <div className="sessionProgressBody">
          {plan && (
            <div className="progressSection">
              <div className="progressLabel"><Target size={13} /> Goal</div>
              <p>{plan.body}</p>
            </div>
          )}
          {tasks.length > 0 && (
            <div className="progressSection">
              <div className="progressLabel">
                <ListChecks size={13} /> Todo
                <small>{done} done</small>
              </div>
              <ol className="progressTasks">
                {orderedTasks.map((task) => (
                  <li key={task.id} data-state={task.state}>
                    {isDone(task.state)
                      ? <CheckCircle2 size={14} />
                      : <Circle size={14} />}
                    <span>
                      <strong>{task.kind}</strong>
                      {(task.failure_reason || task.reason) && (
                        <small>{task.failure_reason || task.reason}</small>
                      )}
                    </span>
                    <b>{task.state.replaceAll("_", " ")}</b>
                  </li>
                ))}
              </ol>
            </div>
          )}
          {agents.length > 0 && (
            <div className="progressSection">
              <div className="progressLabel">
                <Bot size={13} /> Subagents
                <button type="button" onClick={onOpenTrajectory}>
                  Open trajectory
                </button>
              </div>
              <ul className="progressAgents">
                {agents.map((agent) => (
                  <li key={agent.id}>
                    <span data-status={agent.status} />
                    <strong>{agent.role}</strong>
                    <b>{agent.status.replaceAll("_", " ")}</b>
                    {agent.last_message && <small>{agent.last_message}</small>}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </section>
  );
}

function goalTitle(
  plan: SessionPlanArtifact | undefined,
  tasks: readonly TaskSummary[]
): string {
  const firstLine = plan?.body.split(/\r?\n/, 1)[0]?.trim();
  return firstLine || tasks[0]?.kind || "Session progress";
}

function isDone(state: string): boolean {
  return state === "succeeded" || state === "completed" || state === "canceled";
}

function taskRank(state: string): number {
  if (state === "running" || state === "claimed") return 0;
  if (state === "pending" || state === "retry_wait") return 1;
  if (state === "failed") return 2;
  return 3;
}

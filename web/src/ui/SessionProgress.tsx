import {
  Bot,
  CheckCircle2,
  ChevronDown,
  Circle,
  ListChecks,
  Play,
  Target,
  Zap
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
  onOpenTrajectory,
  onPlanTransition,
  planBusy = false
}: {
  plan?: SessionPlanArtifact;
  tasks: readonly TaskSummary[];
  agents: readonly AgentSummary[];
  onOpenTrajectory: () => void;
  onPlanTransition?: (transition: "implement" | "autopilot") => void;
  planBusy?: boolean;
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
            <div className="progressSection progressPlan">
              <div className="progressLabel">
                <Target size={13} /> Plan
                {plan.document?.revision &&
                  <small>Revision {plan.document.revision}</small>}
              </div>
              {plan.document && (
                <>
                  {plan.document.objective && <p>{plan.document.objective}</p>}
                  <ol className="progressTasks">
                    {plan.document.steps.map((step) => (
                      <li key={step.id} data-state={step.status}>
                        {step.status === "done"
                          ? <CheckCircle2 size={14} />
                          : <Circle size={14} />}
                        <span>
                          <strong>{step.title}</strong>
                          {step.expected_evidence &&
                            <small>{step.expected_evidence}</small>}
                        </span>
                        <b>{step.status.replaceAll("_", " ")}</b>
                      </li>
                    ))}
                  </ol>
                </>
              )}
              {onPlanTransition && (
                <div className="artifactActions">
                  <button
                    type="button"
                    disabled={planBusy || !plan.can_implement}
                    onClick={() => onPlanTransition("implement")}
                  >
                    <Play size={13} /> Implement
                  </button>
                  <button
                    type="button"
                    disabled={planBusy || !plan.can_autopilot}
                    onClick={() => onPlanTransition("autopilot")}
                  >
                    <Zap size={13} /> Autopilot
                  </button>
                </div>
              )}
            </div>
          )}
          {tasks.length > 0 && (
            <div className="progressSection">
              <div className="progressLabel">
                <ListChecks size={13} /> Tasks
                <small>{done}/{tasks.length} complete</small>
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
  const title = plan?.document?.title?.trim();
  if (title) return title;
  const objective = plan?.document?.objective?.trim();
  if (objective) return objective;
  const firstLine = plan?.body.split(/\r?\n/, 1)[0]?.trim();
  if (firstLine && !firstLine.startsWith("{")) return firstLine;
  return tasks[0]?.kind || "Implementation plan";
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

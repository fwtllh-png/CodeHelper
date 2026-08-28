import {
  Bot,
  CheckCircle2,
  ChevronDown,
  CircleDashed,
  ListChecks,
  LoaderCircle
} from "lucide-react";
import {useState} from "react";

import type {
  AgentSummary,
  SessionPlanArtifact
} from "../protocol";

export function SessionProgress({
  plan,
  agents,
  onOpenTrajectory
}: {
  plan?: SessionPlanArtifact;
  agents: readonly AgentSummary[];
  onOpenTrajectory: () => void;
}) {
  const [planExpanded, setPlanExpanded] = useState(true);
  const planSteps = plan?.document?.steps ?? [];
  const planDone = planSteps.filter((step) => step.status === "done").length;
  const planActive = planSteps.filter(
    (step) => step.status === "in_progress"
  ).length;
  const planPending = planSteps.length - planDone - planActive;
  const hasPlanContent = Boolean(plan?.document?.steps.length);
  if (!hasPlanContent && agents.length === 0) return null;

  return (
    <section className="sessionProgress" aria-label="Session progress">
      <div className="sessionProgressBody">
        {hasPlanContent && plan && (
          <div
            className="progressSection progressPlan"
            data-collapsed={planExpanded ? undefined : true}
          >
            <button
              type="button"
              className="planDisclosure"
              aria-label={planExpanded ? "Collapse plan" : "Expand plan"}
              aria-expanded={planExpanded}
              title={planExpanded ? "Collapse plan" : "Expand plan"}
              onClick={() => setPlanExpanded((value) => !value)}
            >
              <span className="planSummary">
                <ListChecks size={15} />
                <strong>Tasks</strong>
                <small>
                  {planDone} completed · {planActive} active · {planPending} pending
                </small>
              </span>
              <ChevronDown size={15} data-expanded={planExpanded || undefined} />
            </button>
            {planExpanded && plan.document && (
              <ol className="progressTasks">
                {plan.document.steps.map((step) => (
                  <li key={step.id} data-state={step.status}>
                    {step.status === "done"
                      ? <CheckCircle2 size={16} />
                      : step.status === "in_progress"
                        ? <LoaderCircle className="spin" size={16} />
                        : <CircleDashed size={16} />}
                    <strong>{step.title}</strong>
                  </li>
                ))}
              </ol>
            )}
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
    </section>
  );
}

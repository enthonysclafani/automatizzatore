import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { api, errorMessage } from "../../api/client";
import type { Approval, EffectiveRole } from "../../api/types";
import { formatAbsolute, isEmptyValue } from "../../lib/format";
import { useNow } from "../../lib/now";
import { keys } from "../../lib/queries";
import { approversText, canDecide } from "../../lib/roles";
import { Badge, type Tone } from "../ui/Badge";
import { Button } from "../ui/Button";
import { cx } from "../ui/cx";
import { Dialog } from "../ui/Dialog";
import { IconCheck, IconCheckCircle, IconClock, IconHourglass, IconX, IconXCircle } from "../ui/icons";
import { Field, FormError, Textarea } from "../ui/Input";
import { JsonDetails } from "../ui/JsonDetails";
import { RelativeTime } from "../ui/Time";
import { useToast } from "../ui/Toast";

const STATUS: Record<Approval["status"], { tone: Tone; label: string }> = {
  pending: { tone: "orange", label: "Pending" },
  approved: { tone: "green", label: "Approved" },
  rejected: { tone: "red", label: "Rejected" },
  expired: { tone: "gray", label: "Expired" },
};

export function ApprovalStatusBadge({ status }: { status: Approval["status"] }) {
  const s = STATUS[status];
  const icon =
    status === "pending" ? (
      <IconHourglass size={12} />
    ) : status === "approved" ? (
      <IconCheckCircle size={12} />
    ) : status === "rejected" ? (
      <IconXCircle size={12} />
    ) : (
      <IconClock size={12} />
    );
  return (
    <Badge tone={s.tone} icon={icon}>
      {s.label}
    </Badge>
  );
}

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : undefined;
}

/** `detail` is { action?, connection?, input?, ... }: show the known keys plainly, the rest as JSON. */
export function ApprovalDetail({ detail }: { detail: unknown }) {
  if (isEmptyValue(detail)) return null;
  const rec = asRecord(detail);
  if (!rec) return <JsonDetails label="Details" value={detail} />;
  const { action, connection, input, ...rest } = rec;
  const facts: { label: string; value: string }[] = [];
  if (typeof action === "string" && action) facts.push({ label: "Action", value: action });
  if (typeof connection === "string" && connection) facts.push({ label: "Connection", value: connection });
  return (
    <div className="space-y-2">
      {facts.length > 0 && (
        <dl className="flex flex-wrap gap-x-6 gap-y-1">
          {facts.map((f) => (
            <div key={f.label} className="min-w-0">
              <dt className="text-[12px] text-muted">{f.label}</dt>
              <dd className="font-mono text-[12.5px] break-all text-fg">{f.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {!isEmptyValue(input) && <JsonDetails label="Input" value={input} />}
      {Object.keys(rest).length > 0 && <JsonDetails label="Other details" value={rest} />}
    </div>
  );
}

/** "Approved by ana@example.com 5m ago" plus the comment. */
export function DecisionNote({ approval }: { approval: Approval }) {
  if (approval.status === "pending") return null;
  const verb =
    approval.status === "approved" ? "Approved" : approval.status === "rejected" ? "Rejected" : "Expired";
  return (
    <div className="text-[13px] text-fg-2">
      <p>
        {verb}
        {approval.decidedBy && approval.status !== "expired" && (
          <>
            {" "}
            by <span className="font-medium text-fg">{approval.decidedBy}</span>
          </>
        )}{" "}
        <RelativeTime iso={approval.decidedAt ?? approval.expiresAt} />
        {approval.status === "expired" && (
          <> · the step was {approval.onTimeout === "approve" ? "approved" : "rejected"} on timeout</>
        )}
      </p>
      {approval.comment && (
        <blockquote className="mt-1 border-l-2 border-line-strong pl-2.5 break-words whitespace-pre-wrap text-fg">
          {approval.comment}
        </blockquote>
      )}
    </div>
  );
}

/** Approve and Reject buttons, or a note saying who can decide. */
export function DecideActions({
  ws,
  approval,
  role,
  className,
}: {
  ws: string;
  approval: Approval;
  role: EffectiveRole | undefined;
  className?: string;
}) {
  const [decision, setDecision] = useState<"approve" | "reject" | null>(null);
  if (approval.status !== "pending") return null;
  if (!canDecide(role, approval)) {
    return (
      <p className={cx("text-[13px] text-muted", className)}>
        Only {approversText(approval.approvers)} can decide. Ask one of them, or ask an admin to change your role.
      </p>
    );
  }
  return (
    <div className={cx("flex flex-wrap gap-2", className)}>
      <Button variant="primary" size="sm" icon={<IconCheck size={14} />} onClick={() => setDecision("approve")}>
        Approve
      </Button>
      <Button size="sm" icon={<IconX size={14} />} onClick={() => setDecision("reject")}>
        Reject
      </Button>
      {decision && (
        <DecideDialog ws={ws} approval={approval} decision={decision} onClose={() => setDecision(null)} />
      )}
    </div>
  );
}

function DecideDialog({
  ws,
  approval,
  decision,
  onClose,
}: {
  ws: string;
  approval: Approval;
  decision: "approve" | "reject";
  onClose: () => void;
}) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();
  const approve = decision === "approve";

  const refresh = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: keys.approvals(ws) }),
      queryClient.invalidateQueries({ queryKey: keys.overview(ws) }),
      queryClient.invalidateQueries({ queryKey: keys.run(ws, approval.runId) }),
      queryClient.invalidateQueries({ queryKey: ["ws", ws, "runs"] }),
    ]);

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title={approve ? "Approve this step?" : "Reject this step?"}
      description={
        approve
          ? `${approval.automation} continues and runs step ${approval.stepPath}.`
          : `Step ${approval.stepPath} of ${approval.automation} does not run.`
      }
      onSubmit={async () => {
        setBusy(true);
        setError(null);
        const text = comment.trim() || undefined;
        try {
          if (approve) await api.approve(ws, approval.id, text);
          else await api.reject(ws, approval.id, text);
          toast({
            tone: "success",
            title: approve ? "Approved" : "Rejected",
            description: `${approval.automation} · ${approval.stepPath}`,
          });
          onClose();
          void refresh();
        } catch (err) {
          setError(errorMessage(err));
          setBusy(false);
          // Someone else may have decided first: show the current state.
          void refresh();
        }
      }}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant={approve ? "primary" : "danger"} loading={busy}>
            {approve ? "Approve" : "Reject"}
          </Button>
        </>
      }
    >
      <p className="text-[13px] text-fg-2">{approval.message}</p>
      <Field label="Comment (optional)" hint="Saved with the decision and shown on the run.">
        {({ id, describedBy }) => (
          <Textarea
            id={id}
            rows={3}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            aria-describedby={describedBy}
            maxLength={2000}
            data-autofocus
          />
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}

/** Requested, expiry and what happens on timeout. */
export function ApprovalTimes({ approval }: { approval: Approval }) {
  const now = useNow();
  const expired = Date.parse(approval.expiresAt) <= now;
  return (
    <p className="flex flex-wrap gap-x-3 gap-y-0.5 text-[12.5px] text-fg-2">
      <span>
        Requested <RelativeTime iso={approval.requestedAt} />
      </span>
      {approval.status === "pending" && (
        <span title={formatAbsolute(approval.expiresAt)}>
          {expired ? "Timed out" : "Expires"} <RelativeTime iso={approval.expiresAt} />
          {" · "}on timeout: <span className="font-medium text-fg">{approval.onTimeout}</span>
        </span>
      )}
    </p>
  );
}

/** One approval with its context and, when pending, the decision buttons. */
export function ApprovalCard({
  ws,
  approval,
  role,
  showRunLink = true,
  headingLevel = 2,
}: {
  ws: string;
  approval: Approval;
  role: EffectiveRole | undefined;
  showRunLink?: boolean;
  headingLevel?: 2 | 3;
}) {
  const H = headingLevel === 2 ? "h2" : "h3";
  return (
    <article className="space-y-3 rounded-lg border border-line bg-surface p-4" aria-labelledby={`approval-${approval.id}`}>
      <header className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <H id={`approval-${approval.id}`} className="flex flex-wrap items-baseline gap-x-2 text-sm font-semibold text-fg">
            <Link to="/w/$ws/automations/$name" params={{ ws, name: approval.automation }} className="hover:underline">
              {approval.automation}
            </Link>
            <span className="font-mono text-[12.5px] font-normal text-fg-2">step {approval.stepPath}</span>
          </H>
          <ApprovalTimes approval={approval} />
        </div>
        <ApprovalStatusBadge status={approval.status} />
      </header>
      <p className="text-sm break-words whitespace-pre-wrap text-fg">{approval.message}</p>
      <ApprovalDetail detail={approval.detail} />
      <DecisionNote approval={approval} />
      <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-line pt-3">
        <DecideActions ws={ws} approval={approval} role={role} />
        {showRunLink && (
          <Link
            to="/w/$ws/runs/$id"
            params={{ ws, id: approval.runId }}
            className="text-[13px] font-medium text-accent hover:underline"
          >
            Open run
          </Link>
        )}
      </footer>
    </article>
  );
}

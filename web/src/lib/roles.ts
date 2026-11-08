import type { EffectiveRole, Me, Role } from "../api/types";

const RANK: Record<EffectiveRole, number> = {
  viewer: 0,
  approver: 1,
  builder: 2,
  admin: 3,
  owner: 4,
};

export const ROLES: Role[] = ["viewer", "approver", "builder", "admin"];

export const ROLE_HINT: Record<Role, string> = {
  viewer: "Sees automations, runs and costs",
  approver: "Viewer, plus approves steps",
  builder: "Runs, pauses and resumes automations; sees secret names",
  admin: "Everything in the workspace: secrets, members, audit log",
};

export function atLeast(role: EffectiveRole | undefined, min: EffectiveRole): boolean {
  if (!role) return false;
  return RANK[role] >= RANK[min];
}

export function roleIn(me: Me | undefined, workspace: string): EffectiveRole | undefined {
  if (!me) return undefined;
  const ref = me.workspaces.find((w) => w.id === workspace);
  if (ref) return ref.role;
  return me.isOwner ? "owner" : undefined;
}

"use client";

import { useCallback } from "react";
import { toast } from "sonner";
import type { Issue, UpdateIssueRequest } from "@liexiu/core/types";
import { useWorkspacePaths } from "@liexiu/core/paths";
import { useModalStore } from "@liexiu/core/modals";
import { useUpdateIssue } from "@liexiu/core/issues/mutations";
import { copyText } from "@liexiu/ui/lib/clipboard";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import { useIssueSurfaceActionsOptional } from "../surface/actions-context";

export interface UseIssueActionsResult {
  updateField: (updates: Partial<UpdateIssueRequest>) => void;
  openInNewTab: () => void;
  copyLink: () => Promise<void>;
  openCreateSubIssue: () => void;
  openSetParent: () => void;
  removeParent: () => void;
  openAddChild: () => void;
  openDeleteConfirm: (opts?: { onDeletedFallbackPath?: string }) => void;
}

/**
 * Accepts a nullable issue so callers can invoke the hook before they've
 * early-returned on a missing issue. Returned handlers are safe no-ops when
 * `issue` is null.
 */
export function useIssueActions(issue: Issue | null): UseIssueActionsResult {
  const { t } = useT("issues");
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const updateIssue = useUpdateIssue();
  const surfaceActions = useIssueSurfaceActionsOptional();
  const openModal = useModalStore((s) => s.open);

  const issueId = issue?.id ?? null;
  const issueIdentifier = issue?.identifier ?? null;
  const issueProjectId = issue?.project_id ?? null;
  const issueAssigneeType = issue?.assignee_type ?? null;
  const issueAssigneeId = issue?.assignee_id ?? null;
  const issueStatus = issue?.status ?? null;

  const updateField = useCallback(
    (updates: Partial<UpdateIssueRequest>) => {
      if (!issueId) return;
      // Assigning to an agent may start a run. Route through the
      // pre-trigger confirm modal (preview + optional handoff note + "暂不开始"),
      // which applies the change itself — the four entry points share this one
      // backend-driven flow instead of guessing (MUL-3375). Every other field
      // change (status, priority, member assign, unassign) applies directly.
      //
      // Backlog is the parking lot: assigning a backlog issue never starts a run
      // (server/internal/service/issue_trigger.go), so the modal would only show
      // an empty "won't start" box with a single Apply button. Apply directly,
      // matching the batch backlog short-circuit in BatchActionToolbar.
      if (
        updates.assignee_type === "agent" &&
        updates.assignee_id &&
        issueStatus !== "backlog"
      ) {
        openModal("issue-run-confirm", {
          issueIds: [issueId],
          mode: "assign",
          assigneeType: updates.assignee_type,
          assigneeId: updates.assignee_id,
        });
        return;
      }
      if (surfaceActions) {
        surfaceActions.updateIssue(issueId, updates, {
          errorMessage: t(($) => $.detail.update_failed),
        });
      } else {
        updateIssue.mutate(
          { id: issueId, ...updates },
          {
            onError: (err) =>
              toast.error(
                err instanceof Error && err.message
                  ? err.message
                  : t(($) => $.detail.update_failed),
              ),
          },
        );
      }
    },
    [issueId, issueStatus, surfaceActions, updateIssue, openModal, t],
  );

  // Explicit "open it somewhere else" CTA, so the new tab takes focus
  // (`activate: true`) — the user is asking to move into the new context, not
  // to stash it for later the way modifier-click does. Same contract as the
  // table row open and the attachment preview's "Open in new tab".
  //
  // Only desktop implements `openInNewTab`; on web it is undefined and we fall
  // back to a real browser tab via the shareable URL.
  const openInNewTab = useCallback(() => {
    if (!issueId) return;
    // Identifier form, same as copyLink: on web this becomes a real browser
    // tab at the shareable URL, so it is a link the user sees and may copy out
    // of the address bar. Opening on the UUID would also make the route
    // immediately rewrite the fresh tab's URL.
    const path = paths.issueDetail(issueIdentifier || issueId);
    if (navigation.openInNewTab) {
      navigation.openInNewTab(path, issueIdentifier ?? undefined, {
        activate: true,
      });
      return;
    }
    window.open(
      navigation.getShareableUrl(path),
      "_blank",
      "noopener,noreferrer",
    );
  }, [issueId, issueIdentifier, navigation, paths]);

  const copyLink = useCallback(async () => {
    if (!issueId) return;
    // Share the identifier form (`/{ws}/issues/MUL-123`): a pasted link should
    // say which issue it points at. The UUID form stays valid, so links copied
    // before this still resolve.
    const url = navigation.getShareableUrl(paths.issueDetail(issueIdentifier || issueId));
    if (await copyText(url)) {
      toast.success(t(($) => $.detail.link_copied));
    } else {
      toast.error(t(($) => $.detail.link_copy_failed));
    }
  }, [paths, issueId, issueIdentifier, navigation, t]);

  const openCreateSubIssue = useCallback(() => {
    if (!issueId) return;
    openModal("create-issue", {
      parent_issue_id: issueId,
      parent_issue_identifier: issueIdentifier,
      ...(issueProjectId ? { project_id: issueProjectId } : {}),
      // Inherit the parent's assignee (member/agent) so a sub-issue
      // created from the "Add sub-issue" entry starts with the same owner
      // (discussion #1728). The modal keys off whether these fields are
      // present, not their value, so a seed overrides the sticky last-used
      // assignee it would otherwise fall back to, while omitting both for
      // an unassigned parent leaves that fallback intact. Seed the two
      // together — assignee_type is meaningless without assignee_id.
      ...(issueAssigneeType && issueAssigneeId
        ? { assignee_type: issueAssigneeType, assignee_id: issueAssigneeId }
        : {}),
    });
  }, [
    openModal,
    issueId,
    issueIdentifier,
    issueProjectId,
    issueAssigneeType,
    issueAssigneeId,
  ]);

  const openSetParent = useCallback(() => {
    if (!issueId) return;
    openModal("issue-set-parent", { issueId });
  }, [openModal, issueId]);

  // Detach from the parent and promote to a standalone issue. Reversible
  // (Set parent re-links it), non-destructive, and mirrors the clear-date
  // actions — so it applies directly instead of a confirm modal. `stage`
  // only orders sub-issues under a parent, so clear it in the same write to
  // avoid an orphaned value on a standalone issue. The success toast fires
  // from onSuccess, not eagerly after mutate() — otherwise a request that
  // fails on permission/network/validation would flash "removed" before the
  // error toast and the optimistic rollback (false confirmation).
  const removeParent = useCallback(() => {
    if (!issueId) return;
    if (surfaceActions) {
      surfaceActions.updateIssue(
        issueId,
        { parent_issue_id: null, stage: null },
        {
          onSuccess: () =>
            toast.success(t(($) => $.actions.remove_parent_issue_success)),
          errorMessage: t(($) => $.detail.update_failed),
        },
      );
    } else {
      updateIssue.mutate(
        { id: issueId, parent_issue_id: null, stage: null },
        {
          onSuccess: () =>
            toast.success(t(($) => $.actions.remove_parent_issue_success)),
          onError: (err) =>
            toast.error(
              err instanceof Error && err.message
                ? err.message
                : t(($) => $.detail.update_failed),
            ),
        },
      );
    }
  }, [issueId, surfaceActions, updateIssue, t]);

  const openAddChild = useCallback(() => {
    if (!issueId) return;
    openModal("issue-add-child", { issueId });
  }, [openModal, issueId]);

  const openDeleteConfirm = useCallback(
    (opts?: { onDeletedFallbackPath?: string }) => {
      if (!issueId) return;
      openModal("issue-delete-confirm", {
        issueId,
        identifier: issueIdentifier,
        onDeletedFallbackPath: opts?.onDeletedFallbackPath,
      });
    },
    [openModal, issueId, issueIdentifier],
  );

  return {
    updateField,
    openInNewTab,
    copyLink,
    openCreateSubIssue,
    openSetParent,
    removeParent,
    openAddChild,
    openDeleteConfirm,
  };
}

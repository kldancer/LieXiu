"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { Input } from "@liexiu/ui/components/ui/input";
import { Textarea } from "@liexiu/ui/components/ui/textarea";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from "@liexiu/ui/components/ui/alert-dialog";
import { toast } from "sonner";
import { useQueryClient } from "@tanstack/react-query";
import { workspaceKeys } from "@liexiu/core/workspace/queries";
import { issueKeys } from "@liexiu/core/issues/queries";
import { api } from "@liexiu/core/api";
import { useCurrentWorkspace } from "@liexiu/core/paths";
import type { Workspace } from "@liexiu/core/types";
import { AvatarUploadControl } from "../../common/avatar-upload-control";
import { useT } from "../../i18n";
import {
  SettingsCard,
  SettingsRow,
  SettingsSaveState,
  SettingsSection,
  SettingsTab,
  type SettingsSaveStatus,
} from "./settings-layout";
import { useAutoSave } from "./use-auto-save";

interface WorkspaceDetailsDraft {
  name: string;
  description: string;
  context: string;
}

function workspaceDetailsEqual(
  left: WorkspaceDetailsDraft,
  right: WorkspaceDetailsDraft,
) {
  return (
    left.name === right.name &&
    left.description === right.description &&
    left.context === right.context
  );
}

export function WorkspaceTab() {
  const { t } = useT("settings");
  const workspace = useCurrentWorkspace();
  // Derive the id from useCurrentWorkspace instead of the throwing
  // useWorkspaceId: this component can legitimately render while the
  // workspace is gone from the list cache but the URL slug hasn't changed
  // yet (post-delete invalidation before navigation completes, or an
  // external delete of the workspace we're on). The `!workspace` guard
  // below renders null for that window; a throwing hook would crash first.
  const qc = useQueryClient();

  const [name, setName] = useState(workspace?.name ?? "");
  const [description, setDescription] = useState(workspace?.description ?? "");
  const [context, setContext] = useState(workspace?.context ?? "");
  const [issuePrefix, setIssuePrefix] = useState(workspace?.issue_prefix ?? "");
  const [prefixSaveStatus, setPrefixSaveStatus] =
    useState<SettingsSaveStatus>("idle");
  const [confirmAction, setConfirmAction] = useState<{
    title: string;
    description: string;
    variant?: "destructive";
    onConfirm: () => Promise<void>;
  } | null>(null);
  const canManageWorkspace = true;

  // Reset form state only when the user switches to a different workspace.
  // Keying on workspace?.id (not the object ref) avoids wiping unsaved edits
  // when an unrelated mutation — e.g. avatar/logo upload — replaces the
  // cached Workspace object via setQueryData.
  useEffect(() => {
    setName(workspace?.name ?? "");
    setDescription(workspace?.description ?? "");
    setContext(workspace?.context ?? "");
    setIssuePrefix(workspace?.issue_prefix ?? "");
    // eslint-disable-next-line react-hooks/exhaustive-deps -- intentionally keyed on id only; see comment above
  }, [workspace?.id]);

  // Letters + digits only, uppercase, capped at 10 chars. The backend
  // uppercases and trims on its side too — this is purely a UX guardrail
  // so the value the user sees in the input matches what gets persisted.
  const normalizePrefix = (raw: string) =>
    raw.toUpperCase().replace(/[^A-Z0-9]/g, "").slice(0, 10);

  const normalizedPrefix = normalizePrefix(issuePrefix);
  const prefixChanged =
    !!workspace && normalizedPrefix !== workspace.issue_prefix;
  const prefixInvalid = normalizedPrefix.length === 0;

  const detailsDraft = useMemo(
    () => ({ name, description, context }),
    [context, description, name],
  );
  const savedDetails = useMemo(
    () => ({
      name: workspace?.name ?? "",
      description: workspace?.description ?? "",
      context: workspace?.context ?? "",
    }),
    [workspace?.context, workspace?.description, workspace?.name],
  );
  const saveDetails = useCallback(
    async (next: WorkspaceDetailsDraft) => {
      if (!workspace) return;
      const updated = await api.updateWorkspace(workspace.id, next);
      qc.setQueryData(workspaceKeys.list(), (old: Workspace[] | undefined) =>
        old?.map((ws) => (ws.id === updated.id ? updated : ws)),
      );
    },
    [qc, workspace],
  );
  const detailsAutoSave = useAutoSave({
    value: detailsDraft,
    savedValue: savedDetails,
    onSave: saveDetails,
    onSuccess: () =>
      toast.success(t(($) => $.workspace.toast_saved), {
        id: "settings-auto-save",
      }),
    onError: (error) =>
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.workspace.toast_save_failed),
      ),
    enabled: !!workspace && canManageWorkspace && !!name.trim(),
    isEqual: workspaceDetailsEqual,
  });

  const performPrefixSave = async (nextPrefix: string) => {
    if (!workspace) return;
    setPrefixSaveStatus("saving");
    try {
      const updated = await api.updateWorkspace(workspace.id, {
        issue_prefix: nextPrefix,
      });
      qc.setQueryData(workspaceKeys.list(), (old: Workspace[] | undefined) =>
        old?.map((ws) => (ws.id === updated.id ? updated : ws)),
      );
      // Issue identifiers are computed from the workspace prefix at read time,
      // so every cached issue key is stale after this confirmed change.
      await qc.invalidateQueries({ queryKey: issueKeys.all(updated.id) });
      setPrefixSaveStatus("saved");
      toast.success(t(($) => $.workspace.toast_saved), {
        id: "settings-auto-save",
      });
    } catch (error) {
      setPrefixSaveStatus("error");
      toast.error(
        error instanceof Error
          ? error.message
          : t(($) => $.workspace.toast_save_failed),
      );
    }
  };

  const handlePrefixBlur = () => {
    if (!workspace || prefixInvalid || !prefixChanged) return;
    const nextPrefix = normalizedPrefix;
    setConfirmAction({
      title: t(($) => $.workspace.prefix_confirm_title),
      description: t(($) => $.workspace.prefix_confirm_description, {
        oldPrefix: workspace.issue_prefix,
        newPrefix: nextPrefix,
      }),
      variant: "destructive",
      onConfirm: () => performPrefixSave(nextPrefix),
    });
  };

  if (!workspace) return null;

  return (
    <SettingsTab title={t(($) => $.page.tabs.general)}>
      <SettingsSection
        title={t(($) => $.workspace.section_general)}
        action={
          <SettingsSaveState
            status={
              prefixSaveStatus === "saving" || prefixSaveStatus === "error"
                ? prefixSaveStatus
                : detailsAutoSave.status === "idle"
                  ? prefixSaveStatus
                  : detailsAutoSave.status
            }
            savingLabel={t(($) => $.auto_save.saving)}
            savedLabel={t(($) => $.auto_save.saved)}
            errorLabel={t(($) => $.auto_save.failed)}
          />
        }
      >
        <SettingsCard>
          <SettingsRow
            label={t(($) => $.workspace.logo_label)}
            description={t(($) => $.workspace.click_logo_hint)}
            size="none"
          >
            <div className="flex justify-start sm:justify-end">
              <AvatarUploadControl
                variant="workspace"
                value={workspace.avatar_url ?? null}
                name={workspace.name}
                size={64}
                disabled={!canManageWorkspace}
                ariaLabel={t(($) => $.workspace.change_logo_aria)}
                onUploaded={async (url) => {
                  try {
                    const updated = await api.updateWorkspace(workspace.id, {
                      avatar_url: url,
                    });
                    qc.setQueryData(
                      workspaceKeys.list(),
                      (old: Workspace[] | undefined) =>
                        old?.map((ws) => (ws.id === updated.id ? updated : ws)),
                    );
                    toast.success(t(($) => $.workspace.toast_logo_updated), {
                      id: "settings-auto-save",
                    });
                  } catch (error) {
                    toast.error(
                      error instanceof Error
                        ? error.message
                        : t(($) => $.workspace.toast_logo_failed),
                    );
                  }
                }}
              />
            </div>
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.workspace.name_label)}
            size="text"
          >
            <Input
              type="text"
              name="workspace-name"
              autoComplete="organization"
              aria-label={t(($) => $.workspace.name_label)}
              value={name}
              onChange={(event) => setName(event.target.value)}
              onBlur={detailsAutoSave.flush}
              disabled={!canManageWorkspace}
            />
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.workspace.description_label)}
            size="text"
            align="start"
          >
            <Textarea
              name="workspace-description"
              autoComplete="off"
              aria-label={t(($) => $.workspace.description_label)}
              value={description}
              onChange={(event) => setDescription(event.target.value)}
              onBlur={detailsAutoSave.flush}
              rows={3}
              disabled={!canManageWorkspace}
              className="resize-none"
              placeholder={t(($) => $.workspace.description_placeholder)}
            />
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.workspace.context_label)}
            size="text"
            align="start"
          >
            <Textarea
              name="workspace-context"
              autoComplete="off"
              aria-label={t(($) => $.workspace.context_label)}
              value={context}
              onChange={(event) => setContext(event.target.value)}
              onBlur={detailsAutoSave.flush}
              rows={4}
              disabled={!canManageWorkspace}
              className="resize-none"
              placeholder={t(($) => $.workspace.context_placeholder)}
            />
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.workspace.slug_label)}
            size="text"
          >
            <Input
              type="text"
              name="workspace-slug"
              autoComplete="off"
              spellCheck={false}
              aria-label={t(($) => $.workspace.slug_label)}
              value={workspace.slug}
              readOnly
              className="bg-muted/50 font-mono text-muted-foreground dark:bg-muted/50"
            />
          </SettingsRow>

          <SettingsRow
            label={t(($) => $.workspace.issue_prefix_label)}
            description={t(($) => $.workspace.issue_prefix_hint, {
              example: `${normalizedPrefix || workspace.issue_prefix}-123`,
            })}
            size="code"
          >
              <Input
                type="text"
                name="workspace-issue-prefix"
                autoComplete="off"
                autoCapitalize="characters"
                spellCheck={false}
                aria-label={t(($) => $.workspace.issue_prefix_label)}
                value={issuePrefix}
                onChange={(event) => {
                  setPrefixSaveStatus("idle");
                  setIssuePrefix(normalizePrefix(event.target.value));
                }}
                onBlur={handlePrefixBlur}
                disabled={!canManageWorkspace}
                maxLength={10}
                aria-invalid={prefixInvalid}
                className="font-mono uppercase"
                placeholder={workspace.issue_prefix}
              />
          </SettingsRow>

            {!canManageWorkspace && (
              <div className="px-4 py-3 text-caption text-muted-foreground">
                {t(($) => $.workspace.manage_hint)}
              </div>
            )}
        </SettingsCard>
      </SettingsSection>

      <AlertDialog open={!!confirmAction} onOpenChange={(v) => { if (!v) setConfirmAction(null); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmAction?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmAction?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t(($) => $.workspace.confirm_cancel)}</AlertDialogCancel>
            <AlertDialogAction
              variant={confirmAction?.variant === "destructive" ? "destructive" : "default"}
              onClick={async () => {
                await confirmAction?.onConfirm();
                setConfirmAction(null);
              }}
            >
              {t(($) => $.workspace.confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

    </SettingsTab>
  );
}

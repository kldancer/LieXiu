"use client";

import { useEffect, useLayoutEffect, useRef, useState } from "react";
import {
  ArrowLeftRight,
  Check,
  ChevronRight,
  Maximize2,
  Minimize2,
  X as XIcon,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { DialogTitle } from "@liexiu/ui/components/ui/dialog";
import { Button } from "@liexiu/ui/components/ui/button";
import { Switch } from "@liexiu/ui/components/ui/switch";
import { api } from "@liexiu/core/api";
import { useWorkspaceId } from "@liexiu/core/hooks";
import { useCurrentWorkspace } from "@liexiu/core/paths";
import { projectListOptions } from "@liexiu/core/projects/queries";
import { useQuickCreateStore } from "@liexiu/core/issues/stores/quick-create-store";
import { useIssueDraftStore, type IssueCreateDraft } from "@liexiu/core/issues/stores/draft-store";
import { useCreateModeStore } from "@liexiu/core/issues/stores/create-mode-store";
import { useShortcut } from "@liexiu/core/shortcuts";
import { ShortcutKeycaps } from "../common/shortcut-keycaps";
import { ClearablePillButton } from "../common/pill-button";
import { ProjectPicker } from "../projects/components/project-picker";
import {
  ContentEditor,
  type ContentEditorRef,
  useFileDropZone,
  FileDropOverlay,
  useUploadGate,
  useComposerSubmit,
} from "../editor";
import { useIssueCreateUploads } from "./use-issue-create-uploads";
import { FileUploadButton } from "@liexiu/ui/components/common/file-upload-button";
import { useT } from "../i18n";

// AgentCreatePanel — agent-mode body of the create-issue dialog. Renders
// only the inner content; the surrounding `<Dialog>` AND `<DialogContent>`
// (Portal + Overlay + Popup) are owned by CreateIssueDialog so mode-switching
// swaps only this body. Lifting the Portal is what eliminates the close→open
// animation flash — Base UI replays Popup enter/exit when DialogContent is
// remounted, even inside a still-open Dialog Root.
//
// `onSwitchMode` is wired by the shell — the panel calls it with an optional
// carry payload (currently `project_id`). The shared draft store carries the
// description + agent across the agent→manual flip; project_id rides through
// the same carry channel manual→agent uses, so the manual panel reads it
// from `data?.project_id` without a parallel store.
export function AgentCreatePanel({
  onClose,
  onSwitchMode,
  data,
  isExpanded,
  setIsExpanded,
  onMissionCreated,
}: {
  onClose: () => void;
  onSwitchMode?: (carry?: Record<string, unknown> | null) => void;
  data?: Record<string, unknown> | null;
  /** Lifted to the shell so DialogContent's mode-aware className can react —
   *  same pattern as ManualCreatePanel. Shared across modes so the user's
   *  expand preference persists when switching between agent and manual. */
  isExpanded: boolean;
  setIsExpanded: (v: boolean) => void;
  onMissionCreated?: (missionId: string) => void;
}) {
  const { t } = useT("modals");
  const { t: tProjects } = useT("projects");
  const sendShortcut = useShortcut("send");
  const workspaceName = useCurrentWorkspace()?.name;
  const wsId = useWorkspaceId();
  // Pull `isSuccess` so the stale-id sweep below can distinguish "still
  // loading" from "loaded as empty". Reading length alone treats both as
  // empty and incorrectly clears a valid persisted preference on every open.
  const { data: projects = [], isSuccess: projectsLoaded } = useQuery(
    projectListOptions(wsId),
  );

  const keepOpen = useQuickCreateStore((s) => s.keepOpen);
  const setKeepOpen = useQuickCreateStore((s) => s.setKeepOpen);
  const setLastMode = useCreateModeStore((s) => s.setLastMode);
  // The prompt and project remain in the unified draft so mode switches preserve them.
  const draft = useIssueDraftStore((s) => s.draft);
  const setShared = useIssueDraftStore((s) => s.setShared);
  const setManual = useIssueDraftStore((s) => s.setManual);
  const setAgent = useIssueDraftStore((s) => s.setAgent);
  const setActiveMode = useIssueDraftStore((s) => s.setActiveMode);
  const clearDraft = useIssueDraftStore((s) => s.clearDraft);

  // Project has exactly two seeds, both carrying explicit user intent: the
  // project page (or manual panel) the modal was opened from, and the user's
  // own unfinished draft. It is deliberately NOT seeded from the last create
  // — see quick-create-store (MUL-5862).
  const [projectId, setProjectId] = useState<string | null>(() => {
    const seed = (data?.project_id as string | undefined) ?? draft.shared.projectId;
    return seed ?? null;
  });
  // Local state + shared draft always move together, so both the picker rows
  // and the pill's quick-clear go through here.
  const commitProject = (next: string | null) => {
    setProjectId(next);
    setShared({ projectId: next ?? undefined });
  };

  // Stale-id sweep. Once the project list query has actually resolved
  // (`isSuccess` — distinct from "data is the empty default during loading"),
  // a `projectId` that isn't in the list means the project was deleted in
  // another session. Clear local state AND the unfinished draft — the draft
  // is the only persisted copy left, and leaving it would make the next open
  // re-seed and submit the same dead value.
  useEffect(() => {
    if (!projectsLoaded || projectId === null) return;
    if (projects.some((p) => p.id === projectId)) return;
    setProjectId(null);
    if (draft.shared.projectId === projectId) {
      setShared({ projectId: undefined });
    }
  }, [projectsLoaded, projects, projectId, draft.shared.projectId, setShared]);

  // Mark the persisted draft's active mode so a later reopen and any reader of
  // the unified draft know which form is being edited.
  useEffect(() => {
    setActiveMode("agent");
  }, [setActiveMode]);

  const initialPrompt = draft.agent.prompt || (data?.prompt as string) || "";
  // The editor is uncontrolled — we read the latest markdown via the ref at
  // submit/switch time. `hasContent` mirrors emptiness so the Create button
  // can disable correctly without a controlled-input rerender on every keystroke.
  const editorRef = useRef<ContentEditorRef>(null);
  const [hasContent, setHasContent] = useState(initialPrompt.trim().length > 0);
  const [justSent, setJustSent] = useState(false);
  const [sentCount, setSentCount] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const uploadGate = useUploadGate(editorRef);
  // Coordinator-owned uploads in the shared draft pool (MUL-5181, L2): a file
  // pasted into the prompt survives dialog close and mode switches, aborts on
  // logout, and is dropped after a reload. `gate` widens the editor gate with
  // the pool's placeholders.
  const {
    attachments: pendingAttachments,
    handleUpload: handleUploadFile,
    gate,
  } = useIssueCreateUploads("agent", uploadGate, editorRef);
  const { isDragOver, dropZoneProps } = useFileDropZone({
    onDrop: (files) => files.forEach((f) => editorRef.current?.uploadFile(f)),
  });

  useEffect(() => {
    // Defer focus so it lands after the dialog's focus trap has settled —
    // otherwise the trap can bounce focus back to the first focusable header
    // button on the next tick.
    const id = requestAnimationFrame(() => editorRef.current?.focus());
    return () => cancelAnimationFrame(id);
  }, []);

  // Agent create runs through the shared await-then-render composer contract
  // (single-flight ref, submit-time upload re-check, lock+spin, await→boolean,
  // clear only on acceptance). The prompt IS the editor content, so this maps
  // onto the hook directly.
  // Stale-submit guard (MUL-5181 P0): the issue draft is a SINGLETON store.
  // A late success from a dialog the user closed mid-submit must not clear a
  // newer draft typed after reopening — see ManualCreatePanel for the rule.
  const mountedRef = useRef(true);
  useLayoutEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  const submittedDraftRef = useRef<IssueCreateDraft | null>(null);
  const createdMissionIdRef = useRef("");
  // Set by `onAccepted` only on the branch that keeps the panel open and wipes
  // the editor; read back by `afterAccepted`. The closing branch must not
  // refocus an editor that is about to unmount with the dialog.
  const refocusAfterAcceptRef = useRef(false);

  const composer = useComposerSubmit({
    editorRef,
    uploadGate: gate,
    onSubmit: async (md): Promise<boolean> => {
      // Flush the prompt editor's pending debounce before snapshotting — see
      // ManualCreatePanel.
      const pendingPrompt = editorRef.current?.flushPendingUpdate?.();
      if (pendingPrompt != null) setAgent({ prompt: pendingPrompt });
      submittedDraftRef.current = useIssueDraftStore.getState().draft;
      setError(null);
      try {
        const created = await api.quickCreateIssue({
          command_id: crypto.randomUUID(),
          prompt: md,
          project_id: projectId ?? undefined,
        });
        createdMissionIdRef.current = created.mission_id;
        setLastMode("agent");
        toast.success(t(($) => $.create_issue.agent.toast_sent), {
          duration: 4000,
        });
        return true;
      } catch (e) {
        // Server returns 422 with { code, ... } for the structured rejection
        // paths the modal cares about. Surface the reason in-modal so the
        // user can switch to a live agent / upgrade their daemon without
        // leaving the flow.
        setError(
          e instanceof Error && e.message
            ? e.message
            : t(($) => $.create_issue.agent.error_unknown),
        );
        return false;
      }
    },
    // Continuous-creation mode puts the caret back so the next prompt can be
    // typed immediately. Deliberately no `containerRef`: this is a dialog whose
    // own focus trap already bounds where focus can be, and reclaiming it is the
    // point of keep-open mode.
    afterAccepted: () => (refocusAfterAcceptRef.current ? "refocus" : "none"),
    onAccepted: () => {
      refocusAfterAcceptRef.current = false;
      // A successful create ends this whole draft (shared + manual + agent);
      // last-successful actor/project preferences were saved in onSubmit.
      // Success may only consume the draft it submitted: flush the editor's
      // pending debounce first, then clear only an untouched draft — edits
      // made mid-flight or by a reopened dialog survive.
      const latePrompt = editorRef.current?.flushPendingUpdate?.();
      if (latePrompt != null) setAgent({ prompt: latePrompt });
      const untouched =
        useIssueDraftStore.getState().draft === submittedDraftRef.current;
      if (untouched) clearDraft();
      // An edit made during the request survives; the panel stays open on it.
      if (!mountedRef.current || !untouched) return;
      if (keepOpen) {
        // Stay open for continuous creation — clear the editor so the user can
        // immediately type the next prompt.
        editorRef.current?.clearContent();
        setHasContent(false);
        setSentCount((c) => c + 1);
        setJustSent(true);
        setTimeout(() => setJustSent(false), 1500);
        refocusAfterAcceptRef.current = true;
      } else {
        onClose();
        if (createdMissionIdRef.current) {
          onMissionCreated?.(createdMissionIdRef.current);
          createdMissionIdRef.current = "";
        }
      }
    },
  });
  const submit = () => {
    void composer.submit();
  };
  const submitting = composer.submitting;

  // Switch to manual mode without destroying the prompt draft.
  const switchToManual = () => {
    // The prompt is copied into the manual description on assist-init; mid-upload
    // that body has already lost the pending image (see switchToAgent).
    if (gate.isBlocked()) return;
    // Commit the shared fields to the draft so the manual panel reads them from
    // there — local state can hold a value seeded from `data` that was never
    // written through a picker.
    setShared({ projectId: projectId ?? undefined });
    if (!draft.manual.description.trim()) {
      const md = editorRef.current?.getMarkdown() ?? "";
      if (md) setManual({ description: md });
    }
    setLastMode("manual");
    setActiveMode("manual");
    onSwitchMode?.(projectId ? { project_id: projectId } : null);
  };

  return (
    <>
        <DialogTitle className="sr-only">{t(($) => $.create_issue.sr_agent)}</DialogTitle>

        {/* Header */}
        <div className="flex items-center justify-between px-5 pt-3 pb-2 shrink-0">
          <div className="flex items-center gap-1.5 text-caption">
            <span className="text-muted-foreground">{workspaceName}</span>
            <ChevronRight className="size-3 text-faint-foreground" />
            <span className="font-medium">{t(($) => $.create_issue.agent_breadcrumb)}</span>
          </div>
          {/* Native `title` instead of Base UI Tooltip — Tooltip opens on
              keyboard focus, and the dialog's focus trap briefly lands focus
              on the first focusable element on mount, causing the tooltip to
              auto-pop every open. Same workaround applies to expand. */}
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => setIsExpanded(!isExpanded)}
              title={isExpanded ? t(($) => $.common.collapse_tooltip) : t(($) => $.common.expand_tooltip)}
              aria-label={isExpanded ? t(($) => $.common.collapse_tooltip) : t(($) => $.common.expand_tooltip)}
              className="rounded-sm p-1.5 opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
            >
              {isExpanded ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
            </button>
            <button
              type="button"
              onClick={onClose}
              title={t(($) => $.common.close)}
              aria-label={t(($) => $.common.close)}
              className="rounded-sm p-1.5 opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
            >
              <XIcon className="size-4" />
            </button>
          </div>
        </div>

        <div className="px-5 pt-1 pb-2 shrink-0 text-caption text-muted-foreground">
          {t(($) => $.create_issue.agent.mission_hint)}
        </div>

        {/* Prompt — same rich editor Advanced uses, so paste/drop images,
            mentions, and formatting all work. The dropZone wrapper enables
            drag-and-drop file uploads alongside paste. */}
        {/* `flex-1 min-h-0 overflow-y-auto` so the editor area absorbs the
            remaining vertical space inside the (now max-bounded) DialogContent
            and scrolls internally. Without it, pasting an image expanded the
            editor unbounded and pushed the modal past the viewport. */}
        <div
          {...dropZoneProps}
          className="relative px-5 pb-3 flex flex-1 min-h-[140px] overflow-y-auto"
        >
          <ContentEditor
            ref={editorRef}
            defaultValue={initialPrompt}
            placeholder={t(($) => $.create_issue.agent.prompt_placeholder)}
            onUpdate={(md) => {
              setHasContent(md.trim().length > 0);
              setAgent({ prompt: md });
            }}
            onUploadFile={handleUploadFile}
            onUploadingChange={uploadGate.onUploadingChange}
            attachments={pendingAttachments}
            onSubmit={submit}
            debounceMs={150}
          />
          {isDragOver && <FileDropOverlay />}
        </div>


        {error && (
          <div className="px-5 pb-2 text-caption text-destructive">{error}</div>
        )}

        {/* Project is the only optional field supported by the Mission draft contract. */}
        <div className="flex items-center gap-1.5 px-4 pb-2 shrink-0 flex-wrap">
          <ProjectPicker
              projectId={projectId}
              onUpdate={(u) => commitProject(u.project_id ?? null)}
              triggerRender={
                <ClearablePillButton
                  onClear={projectId !== null ? () => commitProject(null) : undefined}
                  clearLabel={tProjects(($) => $.picker.clear_aria)}
                />
              }
              align="start"
          />
        </div>

        {/* Footer */}
        <div className="flex flex-col gap-2 border-t px-4 py-3 shrink-0 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex min-h-7 items-center gap-2">
            {/* Deliberately NOT disabled while uploading: each file is its
                own queue entry, so queueing a second one is safe and waiting
                for the first to land just to attach the next is busywork. */}
            <FileUploadButton
              size="sm"
              multiple
              onSelect={(file) => editorRef.current?.uploadFile(file)}
            />
            {keepOpen && sentCount > 0 && (
              <span className="text-caption text-emerald-600 dark:text-emerald-400">
                {t(($) => $.create_issue.agent.sent_count, { count: sentCount })}
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              onClick={switchToManual}
              disabled={gate.uploading}
              aria-disabled={gate.uploading || undefined}
              aria-busy={gate.uploading || undefined}
              title={t(($) => $.create_issue.switch_to_manual_tooltip)}
              className="flex shrink-0 items-center gap-1.5 text-caption px-2 py-1 rounded-sm text-muted-foreground hover:text-foreground hover:bg-accent/60 transition-colors cursor-pointer disabled:cursor-not-allowed disabled:opacity-50"
            >
              <ArrowLeftRight className="size-3.5" />
              {t(($) => $.create_issue.switch_to_manual)}
            </button>
            <label className="flex shrink-0 items-center gap-1.5 text-caption text-muted-foreground cursor-pointer select-none">
              <Switch
                size="sm"
                checked={keepOpen}
                onCheckedChange={setKeepOpen}
              />
              {t(($) => $.create_issue.create_another)}
            </label>
            <Button
              size="sm"
              onClick={submit}
              disabled={!hasContent || submitting || gate.uploading}
              aria-disabled={gate.uploading || undefined}
              // Sending is a busy state too, not just uploading.
              aria-busy={gate.uploading || submitting || undefined}
              className={justSent ? "min-w-28 !bg-emerald-600 !text-white" : "min-w-28"}
            >
              {submitting ? t(($) => $.create_issue.agent.sending) : gate.uploading ? t(($) => $.create_issue.agent.uploading) : justSent ? (
                <span className="flex items-center gap-1"><Check className="size-3.5" />{t(($) => $.create_issue.agent.sent_label)}</span>
              ) : (
                <>
                  {t(($) => $.create_issue.agent.submit)}
                  {sendShortcut ? (
                    <ShortcutKeycaps
                      shortcut={sendShortcut}
                      decorative
                      className="ml-1"
                      keyClassName="border-background/30 bg-background/15 text-primary-foreground shadow-none"
                    />
                  ) : null}
                </>
              )}
            </Button>
          </div>
        </div>
    </>
  );
}

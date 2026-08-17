import {
  forwardRef,
  useImperativeHandle,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const mockQuickCreateIssue = vi.hoisted(() => vi.fn());
const mockSetKeepOpen = vi.hoisted(() => vi.fn());
const mockSetLastMode = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
// Uploads flow through the module-level coordinator, which calls
// `api.uploadFile(file, ctx, signal)` (MUL-5181 L2).
const mockApiUploadFile = vi.hoisted(() => vi.fn());
const mockSetShared = vi.hoisted(() => vi.fn());
const mockSetManual = vi.hoisted(() => vi.fn());
const mockSetAgent = vi.hoisted(() => vi.fn());
const mockSetActiveMode = vi.hoisted(() => vi.fn());
const mockClearDraft = vi.hoisted(() => vi.fn());
const quickCreateResult = {
  mission_id: "mission-1",
  status: "ready",
  replayed: false,
};

const emptyIssueDraft = () => ({
  shared: {
    projectId: undefined as string | undefined,
    priority: "none" as "none" | "low" | "medium" | "high" | "urgent",
    dueDate: null as string | null,
    attachments: [] as Array<{ id: string }>,
  },
  manual: {
    title: "",
    description: "",
    status: "todo" as const,
    startDate: null as string | null,
    assigneeType: undefined as "agent" | "member" | undefined,
    assigneeId: undefined as string | undefined,
    labelIds: [] as string[],
  },
  agent: {
    prompt: "",
  },
  activeMode: "agent" as "agent" | "manual",
});

const mockIssueDraftStore = {
  draft: emptyIssueDraft(),
  setShared: mockSetShared,
  setManual: mockSetManual,
  setAgent: mockSetAgent,
  setActiveMode: mockSetActiveMode,
  clearDraft: mockClearDraft,
};

const mockQuickCreateStore = {
  keepOpen: false,
  setKeepOpen: mockSetKeepOpen,
  // Not part of the store's interface any more (MUL-5862), but an older
  // build persisted it and localStorage still hands it back on rehydrate.
  // Kept here so the tests can prove the panel ignores it.
  lastProjectId: null as string | null,
};

// Per-test override for the projects query, so tests can swap between
// "loaded as empty" (the deleted-project case) and "still loading" without
// re-mocking the whole module.
const mockProjectsQuery = vi.hoisted(() => ({
  data: [] as Array<{ id: string; title: string; icon: string | null }>,
  isSuccess: true,
}));

// The real handle mints an id when it inserts the placeholder and hands it to
// the uploader, which adopts it as the draft `clientUploadId`. Mocks must do
// the same or the two records drift apart only in tests.
let mockUploadIdSeq = 0;

vi.mock("@tanstack/react-query", () => ({
  useQuery: ({ queryKey }: { queryKey: string[] }) => {
    switch (queryKey[0]) {
      case "projects":
        return mockProjectsQuery;
      default:
        return { data: [] };
    }
  },
}));

vi.mock("@liexiu/core/api", () => ({
  api: {
    quickCreateIssue: mockQuickCreateIssue,
    uploadFile: mockApiUploadFile,
  },
}));

vi.mock("@liexiu/core/hooks", () => ({
  useWorkspaceId: () => "ws-test",
}));

vi.mock("@liexiu/core/paths", () => ({
  useCurrentWorkspace: () => ({ name: "Test Workspace" }),
}));

// Mocked at the context module rather than the barrel so <AppLink> stays the
// real component and its click contract is what the test exercises.
vi.mock("@liexiu/core/workspace/queries", () => ({
}));

vi.mock("@liexiu/core/projects/queries", () => ({
  projectListOptions: () => ({ queryKey: ["projects"] }),
}));

vi.mock("@liexiu/core/issues/stores/quick-create-store", () => ({
  useQuickCreateStore: (selector?: (state: typeof mockQuickCreateStore) => unknown) =>
    (selector ? selector(mockQuickCreateStore) : mockQuickCreateStore),
}));

vi.mock("@liexiu/core/issues/stores/draft-store", () => ({
  useIssueDraftStore: Object.assign(
    (selector?: (state: typeof mockIssueDraftStore) => unknown) =>
      (selector ? selector(mockIssueDraftStore) : mockIssueDraftStore),
    { getState: () => mockIssueDraftStore },
  ),
}));

vi.mock("@liexiu/core/issues/stores/create-mode-store", () => ({
  useCreateModeStore: (selector?: (state: { setLastMode: typeof mockSetLastMode }) => unknown) =>
    (selector ? selector({ setLastMode: mockSetLastMode }) : { setLastMode: mockSetLastMode }),
}));


vi.mock("../projects/components/project-picker", () => ({
  ProjectPicker: ({ projectId, onUpdate, triggerRender }: any) => (
    <>
      <button type="button" data-testid="project-picker" onClick={() => onUpdate({ project_id: "proj-1" })}>
        Project {projectId ?? "none"}
      </button>
      {/* The caller's own trigger renders too — the pill carries the
          quick-clear ×, which belongs to this panel, not to the picker. */}
      {triggerRender}
    </>
  ),
}));

vi.mock("@liexiu/ui/lib/utils", () => ({
  cn: (...values: Array<string | false | null | undefined>) => values.filter(Boolean).join(" "),
}));

vi.mock("../editor", async () => {
  // Real submit gate (pure React) driven by the mock editor's
  // `hasActiveUploads` / `onUploadingChange`.
  const uploadGate = await vi.importActual<typeof import("../editor/use-upload-gate")>(
    "../editor/use-upload-gate",
  );
  // Real composer submit contract — pure React, no network. Drives the
  // single-flight + upload-gate semantics against the mocked editor/api.
  const composer = await vi.importActual<typeof import("../editor/use-composer-submit")>(
    "../editor/use-composer-submit",
  );
  const ContentEditor = forwardRef(({ defaultValue, onUpdate, onSubmit, onUploadFile, onUploadingChange, placeholder }: any, ref: any) => {
    const valueRef = useRef(defaultValue || "");
    const [value, setValue] = useState(defaultValue || "");
    // Mirrors the real editor's `uploading` node attrs: the placeholder sits
    // in the doc from before the await until the upload settles, which is what
    // `hasActiveUploads` reports and `onUploadingChange` publishes.
    const inFlightRef = useRef(0);
    const runUpload = async (file: File) => {
      inFlightRef.current += 1;
      if (inFlightRef.current === 1) onUploadingChange?.(true);
      try {
        return await onUploadFile?.(file, `mock-upload-${++mockUploadIdSeq}`);
      } finally {
        inFlightRef.current -= 1;
        if (inFlightRef.current === 0) onUploadingChange?.(false);
      }
    };

    useImperativeHandle(ref, () => ({
      getMarkdown: () => valueRef.current,
      clearContent: () => {
        valueRef.current = "";
        setValue("");
      },
      uploadFile: runUpload,
      focus: vi.fn(),
      hasActiveUploads: () => inFlightRef.current > 0,
      // Placeholder rebuild contract: the real handle draws a card for an
      // upload the document is not showing and reports whether it landed.
      // Mocks track ids only — no document to draw into.
      insertUploadPlaceholder: () => true,
      settleUploadPlaceholder: () => false,
    }));

    return (
      <>
        <textarea
          value={value}
          placeholder={placeholder}
          onChange={(e) => {
            valueRef.current = e.target.value;
            setValue(e.target.value);
            onUpdate?.(e.target.value);
          }}
          onKeyDown={(e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
              onSubmit?.();
            }
          }}
        />
        <button
          type="button"
          onClick={() => runUpload(new File(["image"], "shot.png", { type: "image/png" }))}
        >
          Mock editor upload
        </button>
      </>
    );
  });
  ContentEditor.displayName = "ContentEditor";

  return {
    ...uploadGate,
    ...composer,
    ContentEditor,
    useFileDropZone: () => ({ isDragOver: false, dropZoneProps: {} }),
    FileDropOverlay: () => null,
  };
});

vi.mock("@liexiu/ui/components/ui/dialog", () => ({
  DialogTitle: ({ children, className }: { children: ReactNode; className?: string }) => (
    <div className={className}>{children}</div>
  ),
}));

vi.mock("@liexiu/ui/components/ui/button", () => ({
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => (
    <button type="button" disabled={disabled} onClick={onClick}>
      {children}
    </button>
  ),
}));

vi.mock("@liexiu/ui/components/ui/switch", () => ({
  Switch: ({ checked, onCheckedChange }: { checked: boolean; onCheckedChange: (v: boolean) => void }) => (
    <input
      type="checkbox"
      checked={checked}
      onChange={(e) => onCheckedChange(e.target.checked)}
    />
  ),
}));

vi.mock("@liexiu/ui/components/common/file-upload-button", () => ({
  // `disabled` is forwarded so the "can still queue another file mid-upload"
  // guarantee is actually assertable here (MUL-4808).
  FileUploadButton: ({ disabled }: { disabled?: boolean }) => (
    <button type="button" disabled={disabled}>Upload file</button>
  ),
}));

vi.mock("sonner", () => ({
  toast: {
    success: mockToastSuccess,
  },
}));

import { I18nProvider } from "@liexiu/core/i18n/react";
import enCommon from "../locales/en/common.json";
import enModals from "../locales/en/modals.json";
import enEditor from "../locales/en/editor.json";
import enProjects from "../locales/en/projects.json";
import { AgentCreatePanel } from "./quick-create-issue";

const TEST_RESOURCES = {
  en: { common: enCommon, modals: enModals, editor: enEditor, projects: enProjects },
};

function renderPanel(props: React.ComponentProps<typeof AgentCreatePanel>) {
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <AgentCreatePanel {...props} />
    </I18nProvider>,
  );
}

describe("AgentCreatePanel", () => {
  afterEach(cleanup);

  beforeEach(() => {
    vi.clearAllMocks();
    mockQuickCreateStore.lastProjectId = null;
    mockQuickCreateStore.keepOpen = false;
    mockIssueDraftStore.draft = emptyIssueDraft();
    // The prompt now lives in the unified draft's agent slot.
    mockIssueDraftStore.draft.agent.prompt = "Persisted draft prompt";
    mockSetShared.mockImplementation((patch: Partial<typeof mockIssueDraftStore.draft.shared>) => {
      mockIssueDraftStore.draft.shared = { ...mockIssueDraftStore.draft.shared, ...patch };
    });
    mockSetManual.mockImplementation((patch: Partial<typeof mockIssueDraftStore.draft.manual>) => {
      mockIssueDraftStore.draft.manual = { ...mockIssueDraftStore.draft.manual, ...patch };
    });
    mockSetAgent.mockImplementation((patch: Partial<typeof mockIssueDraftStore.draft.agent>) => {
      mockIssueDraftStore.draft.agent = { ...mockIssueDraftStore.draft.agent, ...patch };
    });
    mockClearDraft.mockImplementation(() => {
      mockIssueDraftStore.draft = emptyIssueDraft();
    });
    mockProjectsQuery.data = [];
    mockProjectsQuery.isSuccess = true;
    mockQuickCreateIssue.mockResolvedValue(quickCreateResult);
    mockApiUploadFile.mockResolvedValue({
      id: "019ec09d-6222-722b-bdfa-427b105d80be",
      workspace_id: "ws-test",
      issue_id: null,
      comment_id: null,
      uploader_type: "member",
      uploader_id: "user-1",
      filename: "shot.png",
      url: "/uploads/shot.png",
      download_url: "/api/attachments/019ec09d-6222-722b-bdfa-427b105d80be/download",
      markdown_url: "/api/attachments/019ec09d-6222-722b-bdfa-427b105d80be/download",
      content_type: "image/png",
      size_bytes: 5,
      created_at: "2026-06-12T00:00:00Z",
    });
    mockSetKeepOpen.mockImplementation((value: boolean) => {
      mockQuickCreateStore.keepOpen = value;
    });
  });

  it("loads the persisted prompt draft when no transient prompt is provided", () => {
    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    expect(
      screen.getByPlaceholderText(
        'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
      ),
    ).toHaveProperty("value", "Persisted draft prompt");
  });

  it("writes prompt changes back to the draft store and clears them after submit", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    renderPanel({ onClose, isExpanded: false, setIsExpanded: vi.fn() });

    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
    );

    await user.clear(editor);
    await user.type(editor, "New agent prompt");
    expect(mockSetAgent).toHaveBeenLastCalledWith({ prompt: "New agent prompt" });

    await user.click(screen.getByRole("button", { name: /^Create$/i }));

    await waitFor(() => {
      expect(mockQuickCreateIssue).toHaveBeenCalledWith(expect.objectContaining({
        command_id: expect.stringMatching(/^[0-9a-f-]{36}$/),
        prompt: "New agent prompt",
        project_id: undefined,
      }));
    });
    const request = mockQuickCreateIssue.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(request).toEqual({
      command_id: expect.stringMatching(/^[0-9a-f-]{36}$/),
      prompt: "New agent prompt",
      project_id: undefined,
    });
    for (const oldField of [
      "agent_id",
      "priority",
      "due_date",
      "parent_issue_id",
      "attachment_ids",
    ]) {
      expect(request).not.toHaveProperty(oldField);
    }

    // A successful create ends the whole unified draft.
    expect(mockClearDraft).toHaveBeenCalled();
    expect(mockSetLastMode).toHaveBeenCalledWith("agent");
    expect(onClose).toHaveBeenCalled();
  });

  // MUL-5181 P0: success may only consume the draft it submitted — the editor
  // stays interactive during the request, so mid-flight edits must survive.
  it("typing draft B while draft A's quick-create is pending survives the success (mounted)", async () => {
    let resolveCreate!: (v: unknown) => void;
    mockQuickCreateIssue.mockImplementationOnce(
      () => new Promise((resolve) => { resolveCreate = resolve; }),
    );
    const onClose = vi.fn();
    renderPanel({ onClose, isExpanded: false, setIsExpanded: vi.fn() });
    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
    );
    fireEvent.change(editor, { target: { value: "Draft A prompt" } });
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));
    await waitFor(() => expect(mockQuickCreateIssue).toHaveBeenCalled());

    // Mid-flight edit replaces the singleton draft's object identity.
    mockIssueDraftStore.draft = {
      ...emptyIssueDraft(),
      agent: { ...emptyIssueDraft().agent, prompt: "Draft B prompt" },
    };

    await act(async () => {
      resolveCreate(quickCreateResult);
      await Promise.resolve();
    });

    expect(mockClearDraft).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    expect(mockIssueDraftStore.draft.agent.prompt).toBe("Draft B prompt");
  });

  // MUL-5181 P0: a submit that outlives its dialog may only consume the draft
  // it submitted — never one typed after closing and reopening.
  it("a late quick-create success does NOT clear a draft replaced after close", async () => {
    let resolveCreate!: (v: unknown) => void;
    mockQuickCreateIssue.mockImplementationOnce(
      () => new Promise((resolve) => { resolveCreate = resolve; }),
    );
    const view = renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });
    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
    );
    fireEvent.change(editor, { target: { value: "Draft A prompt" } });
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));
    await waitFor(() => expect(mockQuickCreateIssue).toHaveBeenCalled());

    view.unmount();
    mockIssueDraftStore.draft = {
      ...emptyIssueDraft(),
      agent: { ...emptyIssueDraft().agent, prompt: "Draft B prompt" },
    };

    await act(async () => {
      resolveCreate(quickCreateResult);
      await Promise.resolve();
    });

    expect(mockClearDraft).not.toHaveBeenCalled();
    expect(mockIssueDraftStore.draft.agent.prompt).toBe("Draft B prompt");
  });

  it("a late quick-create success still clears an untouched draft", async () => {
    let resolveCreate!: (v: unknown) => void;
    mockQuickCreateIssue.mockImplementationOnce(
      () => new Promise((resolve) => { resolveCreate = resolve; }),
    );
    const view = renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });
    const editor = screen.getByPlaceholderText(
      'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
    );
    fireEvent.change(editor, { target: { value: "Draft A prompt" } });
    fireEvent.click(screen.getByRole("button", { name: /^Create$/i }));
    await waitFor(() => expect(mockQuickCreateIssue).toHaveBeenCalled());

    view.unmount();
    await act(async () => {
      resolveCreate(quickCreateResult);
      await Promise.resolve();
    });

    expect(mockClearDraft).toHaveBeenCalled();
  });

  // A successful create must not persist its project, so the NEXT open
  // re-seeded the pill with it and quietly filed the following issue into the
  // same place. The target project belongs only to the mission being filed.
  describe("project is not remembered across creates", () => {
    it("ignores a lastProjectId left behind by an older build", () => {
      mockQuickCreateStore.lastProjectId = "proj-1";
      mockProjectsQuery.data = [{ id: "proj-1", title: "Web", icon: null }];
      mockProjectsQuery.isSuccess = true;

      renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

      // Seeding from it is exactly the removed behavior — the pill must be
      // empty, and with no value there is nothing to clear.
      expect(screen.getByTestId("project-picker")).toHaveProperty("textContent", "Project none");
      expect(screen.queryByRole("button", { name: "Clear project" })).toBeNull();
    });

    it("still submits the project picked in this session", async () => {
      // Guard against over-removal: dropping the memory must not drop the
      // field from the outgoing request.
      const user = userEvent.setup();
      mockProjectsQuery.data = [{ id: "proj-1", title: "Web", icon: null }];
      mockProjectsQuery.isSuccess = true;

      renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

      await user.click(screen.getByTestId("project-picker"));
      await user.type(
        screen.getByPlaceholderText(
          'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
        ),
        "Ship it",
      );
      await user.click(screen.getByRole("button", { name: /^Create$/i }));

      await waitFor(() => {
        expect(mockQuickCreateIssue).toHaveBeenCalledWith(
          expect.objectContaining({ project_id: "proj-1" }),
        );
      });
    });
  });

  // If the unfinished draft points at a project that has been deleted (or
  // moved to another workspace), the modal must not keep submitting that dead
  // UUID. Once the projects query resolves and the id is missing, we clear
  // BOTH local state and the draft; dropping only local state would leave the
  // next open re-seeding the same dead value and trigger the server's
  // `project not found` rejection. The draft is now the only persisted copy —
  // the last-create memory is gone (MUL-5862).
  it("clears a stale drafted project once the projects list resolves without it", async () => {
    mockIssueDraftStore.draft.shared.projectId = "deleted-proj";
    mockProjectsQuery.data = [];
    mockProjectsQuery.isSuccess = true;

    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    await waitFor(() => {
      expect(mockSetShared).toHaveBeenCalledWith({ projectId: undefined });
    });
    expect(screen.getByTestId("project-picker")).toHaveProperty("textContent", "Project none");
  });

  // Dropping a project used to cost two clicks — open the popover, hit
  // "No project" — because the pill had no clear affordance of its own
  // (MUL-5862). The × is part of the pill, so it only exists once the field
  // has a value to drop.
  describe("project pill quick-clear", () => {
    it("has no × while no project is selected", () => {
      renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

      expect(screen.getByTestId("project-picker")).toHaveProperty("textContent", "Project none");
      expect(screen.queryByRole("button", { name: "Clear project" })).toBeNull();
    });

    it("clears local state and the shared draft in one click", async () => {
      const user = userEvent.setup();
      mockIssueDraftStore.draft.shared.projectId = "proj-1";
      mockProjectsQuery.data = [{ id: "proj-1", title: "Web", icon: null }];
      mockProjectsQuery.isSuccess = true;

      renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });
      expect(screen.getByTestId("project-picker")).toHaveProperty("textContent", "Project proj-1");

      await user.click(screen.getByRole("button", { name: "Clear project" }));

      expect(screen.getByTestId("project-picker")).toHaveProperty("textContent", "Project none");
      expect(mockSetShared).toHaveBeenCalledWith({ projectId: undefined });
      // The × retires with the value it cleared.
      expect(screen.queryByRole("button", { name: "Clear project" })).toBeNull();
    });
  });

  // Mirror case: while the query is still loading, we must NOT preemptively
  // clear the drafted project — that would wipe a perfectly valid selection
  // on every open before the list ever renders.
  it("keeps the drafted project while the projects list is still loading", () => {
    mockIssueDraftStore.draft.shared.projectId = "proj-1";
    mockProjectsQuery.data = [];
    mockProjectsQuery.isSuccess = false;

    renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

    expect(mockSetShared).not.toHaveBeenCalled();
    expect(screen.getByTestId("project-picker")).toHaveProperty("textContent", "Project proj-1");
  });

  // MUL-4808 — Quick Create already gated Create; these pin the two gaps:
  // the mode switch (which re-serializes the prompt into the manual draft)
  // and the file button that used to lock during an upload for no reason.
  // Uploads remain editable prompt content and continue through the draft
  // coordinator; their attachment IDs are intentionally not independent
  // fields in the Mission draft request.
  describe("upload submit gate", () => {
    function startPendingUpload() {
      let release!: (result: unknown) => void;
      mockApiUploadFile.mockImplementationOnce(
        () => new Promise((resolve) => { release = resolve; }),
      );
      fireEvent.click(screen.getByRole("button", { name: "Mock editor upload" }));
      return { release: (result: unknown) => release(result) };
    }

    it("blocks Switch to Manual while an upload is in flight", async () => {
      const onSwitchMode = vi.fn();
      renderPanel({ onClose: vi.fn(), onSwitchMode, isExpanded: false, setIsExpanded: vi.fn() });

      startPendingUpload();

      // The switch hands the serialized prompt to the manual panel — mid-upload
      // that prompt has already lost the pending image.
      const switchButton = screen.getByRole("button", { name: /Switch to Manual/i });
      await waitFor(() => expect(switchButton).toHaveProperty("disabled", true));
      fireEvent.click(switchButton);
      expect(onSwitchMode).not.toHaveBeenCalled();
    });

    it("keeps the attach-file button usable during an upload so files can queue", async () => {
      renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

      startPendingUpload();

      // Each file is its own queue entry — making users wait for the first to
      // land before picking the second was a restriction with no race behind
      // it, and this issue explicitly removed it.
      const submit = await screen.findByRole("button", { name: "Uploading…" });
      expect(submit).toHaveProperty("disabled", true);
      expect(screen.getByRole("button", { name: "Upload file" })).toHaveProperty("disabled", false);
    });
  });

  // MUL-4931 — this path files a real issue, so a double-fire is a duplicate
  // issue, not a cosmetic glitch. `submitting` is state: two chords landing in
  // one tick both read the pre-update value, so only a synchronously-flipped
  // ref can gate it. Mirrors the manual-create regression.
  describe("send shortcut single-flight", () => {
    it("creates once when the send chord fires twice in the same tick", async () => {
      // Hold the request open so both presses land inside the in-flight window.
      let release!: (v: unknown) => void;
      mockQuickCreateIssue.mockImplementationOnce(
        () => new Promise((resolve) => { release = resolve; }),
      );

      renderPanel({ onClose: vi.fn(), isExpanded: false, setIsExpanded: vi.fn() });

      const editor = screen.getByPlaceholderText(
        'Tell the agent what to do, e.g. "let Bohan fix the issues loading slowness in the Web project"',
      );

      // Both presses inside ONE act: React cannot re-render between them, so
      // the second handler still closes over `submitting === false`. fireEvent
      // would flush in between and hide the race entirely.
      await act(async () => {
        const press = () =>
          editor.dispatchEvent(
            new KeyboardEvent("keydown", { key: "Enter", metaKey: true, bubbles: true }),
          );
        press();
        press();
      });

      await act(async () => { release(quickCreateResult); });

      expect(mockQuickCreateIssue).toHaveBeenCalledTimes(1);
    });
  });
});

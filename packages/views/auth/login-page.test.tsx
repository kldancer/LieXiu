import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { I18nProvider } from "@liexiu/core/i18n/react";
import enAuth from "../locales/en/auth.json";
import enCommon from "../locales/en/common.json";
import enSettings from "../locales/en/settings.json";

const { mockBootstrap, mockGetBootstrapStatus, mockGetMe, mockIssueCliToken } = vi.hoisted(() => ({
  mockBootstrap: vi.fn(),
  mockGetBootstrapStatus: vi.fn(),
  mockGetMe: vi.fn(),
  mockIssueCliToken: vi.fn(),
}));

vi.mock("@liexiu/core/auth", () => ({
  useAuthStore: Object.assign(() => ({}), {
    getState: () => ({ bootstrap: mockBootstrap }),
  }),
}));

vi.mock("@liexiu/core/api", () => ({
  api: {
    getBootstrapStatus: mockGetBootstrapStatus,
    getMe: mockGetMe,
    issueCliToken: mockIssueCliToken,
    setToken: vi.fn(),
  },
}));

import { LoginPage, validateCliCallback } from "./login-page";

function Wrapper({ children }: { children: ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <I18nProvider locale="en" resources={{ en: { auth: enAuth, common: enCommon, settings: enSettings } }}>
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    </I18nProvider>
  );
}

describe("single-owner LoginPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetMe.mockRejectedValue(new Error("unauthorized"));
    mockGetBootstrapStatus.mockResolvedValue({
      enabled: true,
      initialized: true,
      requires_selection: false,
    });
  });

  it("renders only the bootstrap credential for an initialized instance", async () => {
    render(<LoginPage onSuccess={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByLabelText("Bootstrap secret")).toBeInTheDocument();
    expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();
    expect(screen.queryByText(/google/i)).not.toBeInTheDocument();
  });

  it("provisions an empty instance and returns the canonical workspace", async () => {
    const onSuccess = vi.fn();
    mockGetBootstrapStatus.mockResolvedValue({
      enabled: true,
      initialized: false,
      requires_selection: false,
    });
    mockBootstrap.mockResolvedValue({
      token: "bootstrap-jwt",
      user: { id: "owner-1" },
      workspace: { id: "ws-1", name: "Local", slug: "local" },
      provisioned: true,
    });
    const user = userEvent.setup();
    render(<LoginPage onSuccess={onSuccess} />, { wrapper: Wrapper });

    await user.type(await screen.findByLabelText("Bootstrap secret"), "secret-value");
    await user.type(screen.getByLabelText("Owner name"), "Local Owner");
    await user.type(screen.getByLabelText("Owner email"), "owner@local.test");
    await user.type(screen.getByLabelText("Workspace name"), "Local");
    await user.type(screen.getByLabelText("Workspace slug"), "local");
    await user.click(screen.getByRole("button", { name: "Set up LieXiu" }));

    await waitFor(() => expect(mockBootstrap).toHaveBeenCalledWith(expect.objectContaining({
      secret: "secret-value",
      owner_email: "owner@local.test",
      workspace_slug: "local",
    })));
    expect(onSuccess).toHaveBeenCalledWith(expect.objectContaining({ id: "ws-1" }));
    expect(screen.getByLabelText("Bootstrap secret")).toHaveValue("");
  });

  it("does not fall back to public signup when bootstrap is disabled", async () => {
    mockGetBootstrapStatus.mockResolvedValue({
      enabled: false,
      initialized: false,
      requires_selection: false,
    });
    render(<LoginPage onSuccess={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByText(/Bootstrap failed/i)).toBeInTheDocument();
    expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();
  });

  it("uses the configured product name in bootstrap copy", async () => {
    render(<LoginPage productName="Acme Studio" onSuccess={vi.fn()} />, { wrapper: Wrapper });

    expect(await screen.findByText("Set up local Acme Studio")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set up Acme Studio" })).toBeInTheDocument();
  });
});

describe("validateCliCallback", () => {
  it("accepts localhost and private HTTP callbacks", () => {
    expect(validateCliCallback("http://127.0.0.1:44123/callback")).toBe(true);
    expect(validateCliCallback("http://192.168.1.10/callback")).toBe(true);
  });

  it("rejects public and non-HTTP callbacks", () => {
    expect(validateCliCallback("https://127.0.0.1/callback")).toBe(false);
    expect(validateCliCallback("http://example.com/callback")).toBe(false);
  });
});

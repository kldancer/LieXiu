// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { configStore } from "@liexiu/core/config";
import { I18nProvider } from "@liexiu/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

vi.mock("./vcs-tab", () => ({
  VCSTab: () => <div data-testid="vcs-tab" />,
}));

import { IntegrationsTab } from "./integrations-tab";

function renderTab() {
  return render(
    <I18nProvider locale="en" resources={{ en: { common: enCommon, settings: enSettings } }}>
      <IntegrationsTab />
    </I18nProvider>,
  );
}

describe("Settings IntegrationsTab", () => {
  beforeEach(() => {
    cleanup();
    configStore.getState().setAuthConfig({ vcsIntegrationAvailable: false });
  });

  it("hides VCS connections when the deployment reports them unavailable", () => {
    renderTab();
    expect(screen.queryByTestId("vcs-tab")).not.toBeInTheDocument();
  });

  it("shows VCS connections when the deployment enables them", () => {
    configStore.getState().setAuthConfig({ vcsIntegrationAvailable: true });
    renderTab();
    expect(screen.getByTestId("vcs-tab")).toBeInTheDocument();
  });

  it("does not render removed external integration sections", () => {
    renderTab();
    expect(screen.queryByText("Lark")).toBeNull();
    expect(screen.queryByText("Slack")).toBeNull();
    expect(screen.queryByText("Composio")).toBeNull();
  });
});

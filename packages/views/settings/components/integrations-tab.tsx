"use client";

import { VCSTab } from "./vcs-tab";
import { useConfigStore } from "@liexiu/core/config";
import { useT } from "../../i18n";
import { SettingsSection, SettingsTab } from "./settings-layout";

// Keep self-hosted VCS connections available while external chat channels and
// Composio are removed from the product surface.
export function IntegrationsTab() {
  const { t } = useT("settings");
  const vcsAvailable = useConfigStore((s) => s.vcsIntegrationAvailable);

  return (
    <SettingsTab title={t(($) => $.page.tabs.integrations)}>
      {vcsAvailable && (
        <SettingsSection title={t(($) => $.vcs.section_title)}>
          <VCSTab />
        </SettingsSection>
      )}
    </SettingsTab>
  );
}

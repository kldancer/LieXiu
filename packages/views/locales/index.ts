import type { LocaleResources, SupportedLocale } from "@liexiu/core/i18n";
import enCommon from "./en/common.json";
import enAuth from "./en/auth.json";
import enSettings from "./en/settings.json";
import enIssues from "./en/issues.json";
import enAgents from "./en/agents.json";
import enEditor from "./en/editor.json";
import enLabels from "./en/labels.json";
import enMembers from "./en/members.json";
import enMyIssues from "./en/my-issues.json";
import enSearch from "./en/search.json";
import enWorkspace from "./en/workspace.json";
import enProjects from "./en/projects.json";
import enSkills from "./en/skills.json";
import enModals from "./en/modals.json";
import enRuntimes from "./en/runtimes.json";
import enLayout from "./en/layout.json";
import enUsage from "./en/usage.json";
import enUi from "./en/ui.json";
import enOrchestration from "./en/orchestration.json";
import zhHansCommon from "./zh-Hans/common.json";
import zhHansAuth from "./zh-Hans/auth.json";
import zhHansSettings from "./zh-Hans/settings.json";
import zhHansIssues from "./zh-Hans/issues.json";
import zhHansAgents from "./zh-Hans/agents.json";
import zhHansEditor from "./zh-Hans/editor.json";
import zhHansLabels from "./zh-Hans/labels.json";
import zhHansMembers from "./zh-Hans/members.json";
import zhHansMyIssues from "./zh-Hans/my-issues.json";
import zhHansSearch from "./zh-Hans/search.json";
import zhHansWorkspace from "./zh-Hans/workspace.json";
import zhHansProjects from "./zh-Hans/projects.json";
import zhHansSkills from "./zh-Hans/skills.json";
import zhHansModals from "./zh-Hans/modals.json";
import zhHansRuntimes from "./zh-Hans/runtimes.json";
import zhHansLayout from "./zh-Hans/layout.json";
import zhHansUsage from "./zh-Hans/usage.json";
import zhHansUi from "./zh-Hans/ui.json";
import zhHansOrchestration from "./zh-Hans/orchestration.json";
import koCommon from "./ko/common.json";
import koAuth from "./ko/auth.json";
import koSettings from "./ko/settings.json";
import koIssues from "./ko/issues.json";
import koAgents from "./ko/agents.json";
import koEditor from "./ko/editor.json";
import koLabels from "./ko/labels.json";
import koMembers from "./ko/members.json";
import koMyIssues from "./ko/my-issues.json";
import koSearch from "./ko/search.json";
import koWorkspace from "./ko/workspace.json";
import koProjects from "./ko/projects.json";
import koSkills from "./ko/skills.json";
import koModals from "./ko/modals.json";
import koRuntimes from "./ko/runtimes.json";
import koLayout from "./ko/layout.json";
import koUsage from "./ko/usage.json";
import koUi from "./ko/ui.json";
import koOrchestration from "./ko/orchestration.json";
import jaCommon from "./ja/common.json";
import jaAuth from "./ja/auth.json";
import jaSettings from "./ja/settings.json";
import jaIssues from "./ja/issues.json";
import jaAgents from "./ja/agents.json";
import jaEditor from "./ja/editor.json";
import jaLabels from "./ja/labels.json";
import jaMembers from "./ja/members.json";
import jaMyIssues from "./ja/my-issues.json";
import jaSearch from "./ja/search.json";
import jaWorkspace from "./ja/workspace.json";
import jaProjects from "./ja/projects.json";
import jaSkills from "./ja/skills.json";
import jaModals from "./ja/modals.json";
import jaRuntimes from "./ja/runtimes.json";
import jaLayout from "./ja/layout.json";
import jaUsage from "./ja/usage.json";
import jaUi from "./ja/ui.json";
import jaOrchestration from "./ja/orchestration.json";

// Single source of truth for the resource bundle. Both apps (web layout +
// desktop App.tsx) import from here so adding a locale or namespace happens
// in exactly one place.
export const RESOURCES: Record<SupportedLocale, LocaleResources> = {
  en: {
    common: enCommon,
    auth: enAuth,
    settings: enSettings,
    issues: enIssues,
    agents: enAgents,
    editor: enEditor,
    labels: enLabels,
    members: enMembers,
    "my-issues": enMyIssues,
    search: enSearch,
    workspace: enWorkspace,
    projects: enProjects,
    skills: enSkills,
    modals: enModals,
    runtimes: enRuntimes,
    layout: enLayout,
    usage: enUsage,
    ui: enUi,
    orchestration: enOrchestration,
  },
  "zh-Hans": {
    common: zhHansCommon,
    auth: zhHansAuth,
    settings: zhHansSettings,
    issues: zhHansIssues,
    agents: zhHansAgents,
    editor: zhHansEditor,
    labels: zhHansLabels,
    members: zhHansMembers,
    "my-issues": zhHansMyIssues,
    search: zhHansSearch,
    workspace: zhHansWorkspace,
    projects: zhHansProjects,
    skills: zhHansSkills,
    modals: zhHansModals,
    runtimes: zhHansRuntimes,
    layout: zhHansLayout,
    usage: zhHansUsage,
    ui: zhHansUi,
    orchestration: zhHansOrchestration,
  },
  ko: {
    common: koCommon,
    auth: koAuth,
    settings: koSettings,
    issues: koIssues,
    agents: koAgents,
    editor: koEditor,
    labels: koLabels,
    members: koMembers,
    "my-issues": koMyIssues,
    search: koSearch,
    workspace: koWorkspace,
    projects: koProjects,
    skills: koSkills,
    modals: koModals,
    runtimes: koRuntimes,
    layout: koLayout,
    usage: koUsage,
    ui: koUi,
    orchestration: koOrchestration,
  },
  ja: {
    common: jaCommon,
    auth: jaAuth,
    settings: jaSettings,
    issues: jaIssues,
    agents: jaAgents,
    editor: jaEditor,
    labels: jaLabels,
    members: jaMembers,
    "my-issues": jaMyIssues,
    search: jaSearch,
    workspace: jaWorkspace,
    projects: jaProjects,
    skills: jaSkills,
    modals: jaModals,
    runtimes: jaRuntimes,
    layout: jaLayout,
    usage: jaUsage,
    ui: jaUi,
    orchestration: jaOrchestration,
  },
};

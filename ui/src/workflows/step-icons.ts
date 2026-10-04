import { Bot, FileSearch, GitBranch, MessageSquare, ShieldCheck, Terminal, UserCheck, Workflow } from "@lucide/vue";
import type { Component } from "vue";

const GROUP_ICONS: Record<string, Component> = {
  agent: Bot,
  command: Terminal,
  repo: GitBranch,
  gate: ShieldCheck,
  forge: MessageSquare,
  human: UserCheck,
  review: FileSearch,
  workflow: Workflow,
};

const GROUP_TITLES: Record<string, string> = {
  agent: "Agents",
  command: "Commands",
  repo: "Repository",
  gate: "Checks",
  forge: "Issues and PRs",
  human: "People",
  review: "Review",
  workflow: "Flow",
};

/** The icon for a step type, by the area its name starts with. */
export function stepIcon(type: string): Component {
  return GROUP_ICONS[type.split(".")[0]] ?? Workflow;
}

/** Step type names grouped by area, in a fixed reading order. */
export function stepGroups(types: string[]): { title: string; icon: Component; types: string[] }[] {
  return Object.keys(GROUP_TITLES)
    .map((group) => ({
      title: GROUP_TITLES[group],
      icon: GROUP_ICONS[group],
      types: types.filter((type) => type.split(".")[0] === group),
    }))
    .filter((group) => group.types.length);
}

import { defineConfig } from "vitepress";

export default defineConfig({
  title: "Archie",
  description: "Documentation for the Archie personal agent platform.",
  lastUpdated: true,
  cleanUrls: true,
  srcDir: ".",
  srcExclude: ["README.md"],
  outDir: ".vitepress/dist",

  head: [["link", { rel: "icon", href: "/favicon.svg" }]],

  themeConfig: {
    logo: "/favicon.svg",
    nav: [{ text: "Architecture", link: "/architecture/" }],
    sidebar: {
      "/architecture/": [
        {
          text: "Architecture",
          items: [
            { text: "Overview", link: "/architecture/" },
            { text: "Repository Organisation", link: "/architecture/organisation" },
            { text: "Dependencies and Contracts", link: "/architecture/dependencies-and-contracts" },
            { text: "Agent System", link: "/architecture/agent-system" },
            { text: "Configuration", link: "/architecture/configuration" },
            { text: "Identity", link: "/architecture/identity" },
            { text: "Messaging and Work Intake", link: "/architecture/messaging-and-work-intake" },
            { text: "Plugins and Extensions", link: "/architecture/plugins-and-extensions" },
            { text: "Policy System", link: "/architecture/policy" },
            { text: "Safe Change and Recovery", link: "/architecture/safe-change-and-recovery" },
            { text: "Generated Documentation", link: "/architecture/generated-documentation" },
            { text: "Domain and Feature Review Procedure", link: "/architecture/package-review" },
            { text: "Migration Decisions", link: "/architecture/migration-decisions" },
          ],
        },
      ],
    },
    search: {
      provider: "local",
    },
    footer: {
      message: "Built with VitePress.",
      copyright: "Archie",
    },
  },
});

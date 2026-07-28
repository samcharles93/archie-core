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
    search: {
      provider: "local",
    },
    footer: {
      message: "Built with VitePress.",
      copyright: "Archie",
    },
  },
});

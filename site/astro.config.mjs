// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLlmsTxt from 'starlight-llms-txt';
import { execSync } from 'node:child_process';

// The release shown in the header: JEVX_VERSION if set, else the newest tag reachable from this checkout. Never
// hand-edited, so a rebuild after a release always shows that release.
const version =
	process.env.JEVX_VERSION ||
	(() => {
		try {
			return execSync('git describe --tags --abbrev=0 --match "v*"', { stdio: ['ignore', 'pipe', 'ignore'] }).toString().trim();
		} catch {
			return 'dev';
		}
	})();

// Project GitHub Pages: https://muthuishere.github.io/jevx
// Same stack and house style as the cljgo and toolnexus docs sites.
export default defineConfig({
	vite: { define: { __JEVX_VERSION__: JSON.stringify(version) } },
	site: 'https://muthuishere.github.io',
	base: '/jevx',
	integrations: [
		starlight({
			title: 'jevx',
			description:
				'Typed judgements for coding agents: yes/no, pick-one and rating verdicts with a probability, from any System One endpoint.',
			// No right-hand "On this page" ToC, as on cljgo: frees width for tables and JSON.
			tableOfContents: false,
			favicon: '/favicon.svg',
			plugins: [
				starlightLlmsTxt({
					projectName: 'jevx',
					description:
						'jevx is a CLI and agent skill that lets a coding agent hand small decisions (yes/no, pick-one, rating) to a Jev-style System One decision model and get back a typed verdict with a probability and an exit code (0 yes, 1 no, 3 unsure, 4 error).',
				}),
			],
			components: {
				Footer: './src/components/Footer.astro',
				// Title plus the current release, as on cljgo.
				SiteTitle: './src/components/SiteTitle.astro',
			},
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/muthuishere/jevx' },
				{ icon: 'npm', label: 'npm', href: 'https://www.npmjs.com/package/@muthuishere/jevx' },
			],
			editLink: { baseUrl: 'https://github.com/muthuishere/jevx/edit/main/site/' },
			customCss: [
				'@fontsource-variable/inter',
				'@fontsource-variable/jetbrains-mono',
				'./src/styles/theme.css',
			],
			expressiveCode: {
				themes: ['github-dark', 'github-light'],
				styleOverrides: {
					borderRadius: '0.5rem',
					codeFontFamily: "'JetBrains Mono Variable', 'JetBrains Mono', ui-monospace, monospace",
				},
			},
			sidebar: [
				{
					label: 'Start',
					items: [
						{ label: 'Introduction', slug: 'start/introduction' },
						{ label: 'Getting started', slug: 'start/getting-started' },
						{ label: 'Install', slug: 'start/install' },
					],
				},
				{
					label: 'Guides',
					items: [
						{ label: 'Agent scenarios', slug: 'guides/scenarios' },
						{ label: 'Memory', slug: 'guides/memory' },
						{ label: 'Ask', slug: 'guides/ask' },
						{ label: 'Shortcuts: is, pick, filter, rank', slug: 'guides/shortcuts' },
						{ label: 'Saved questions', slug: 'guides/questions' },
						{ label: 'Context', slug: 'guides/context' },
						{ label: 'Plugins', slug: 'guides/plugins' },
						{ label: 'Using it from an agent', slug: 'guides/agents' },
					],
				},
				{
					label: 'Reference',
					items: [
						{ label: 'Settings', slug: 'reference/settings' },
						{ label: 'Exit codes & reliability', slug: 'reference/exit-codes' },
						{ label: 'Privacy: what leaves your machine', slug: 'reference/privacy' },
						{ label: 'CLI reference', slug: 'reference/cli' },
						{ label: 'Config file', slug: 'reference/config' },
						{ label: 'Compared with other Jev tools', slug: 'reference/comparison' },
						{ label: 'Changelog', slug: 'reference/changelog' },
					],
				},
				{ label: 'FAQ', slug: 'faq' },
			],
		}),
	],
});

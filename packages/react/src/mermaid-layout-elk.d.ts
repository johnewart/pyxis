/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 *
 * @mermaid-js/layout-elk is an OPTIONAL peer of mermaid (better auto-layout for
 * complex flowcharts). mermaidRuntime.ts dynamically imports it and swallows
 * the failure if it isn't installed, so it's deliberately not a hard
 * dependency here — this ambient declaration just satisfies the type checker
 * for that dynamic import without requiring every @pyxis-llm/react consumer to
 * install a package they may never need.
 */
declare module '@mermaid-js/layout-elk' {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const layoutLoader: any;
  export default layoutLoader;
}

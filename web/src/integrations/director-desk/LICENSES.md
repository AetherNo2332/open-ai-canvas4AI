# DirectorDesk browser integration

This integration is based on the MIT-licensed browser-safe concepts from
[mangfufu/director-desk](https://github.com/mangfufu/director-desk), version
0.4.11. Electron, MCP, local project storage and built-in AI adapters are not
bundled into the web product. The browser bundle in `vendor/director-desk-engine.js`
is produced from the upstream `src/engine.ts` dependency graph and keeps its
MIT provenance; it is loaded only through the canvas integration boundary.

The maintained browser bundle imports `@desk/uuid` for UUID creation so HTTP LAN
deployments use secure `getRandomValues` when `randomUUID` is unavailable. Preserve
this adaptation when regenerating the engine bundle.

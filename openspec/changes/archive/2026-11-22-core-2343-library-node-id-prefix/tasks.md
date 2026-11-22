## 1. Server

- [x] 1.1 Library node IDs carry the `library-` prefix once
- [x] 1.2 Stored navigation overrides that name legacy doubled IDs still apply; current IDs win and a live node's own ID is never aliased

## 2. Web

- [x] 2.1 An old doubled node address resolves to the node
- [x] 2.2 A Library pivot remembered under the old node ID is still read

## 3. Tests and goldens

- [x] 3.1 Go: node IDs equal the Library ID; a legacy override hides, orders and relabels
- [x] 3.2 Vitest: legacy address resolution and remembered pivot fallback
- [x] 3.3 Regenerate the manifest golden, the golden corpus and the golden hashes through their documented commands

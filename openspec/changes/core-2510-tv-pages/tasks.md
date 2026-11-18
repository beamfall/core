## 1. Container page (CORE-2510)

- [x] 1.1 Ancestor link above the title; the ancestor's one-card rail is dropped
- [x] 1.2 Video Media rows over a list-only detail Layout: still, unshared ordinal and runtime, two-line summary, title once
- [x] 1.3 Row opens detail; separate play button plays with the container queue
- [x] 1.4 On Deck names the next child once
- [x] 1.5 Vitest for each point

## 2. Links and addresses (CORE-2511)

- [x] 2.1 `MediaItemCard` renders an anchor; the More menu stays a sibling
- [x] 2.2 `/entity/:entityId` resolves bare IDs and readable addresses; canonical rewrite skips Revealed sessions
- [x] 2.3 Vitest for anchors and address resolution

## 3. Lower rails and shelf ranking (CORE-2512)

- [x] 3.1 Similar containers rail ranked by shared category and person Entities, hidden when empty
- [x] 3.2 Browse Layout catalog shelves ranked by engagement tier and year
- [x] 3.3 Vitest for selection and ordering

## 4. Sibling switcher and overview clamp (CORE-2522)

- [x] 4.1 Sibling containers from the ancestor as a link row under the title, own marked current
- [x] 4.2 First long `text` field reads as a clamped overview when there is no summary, and leaves the fields list
- [x] 4.3 Vitest for the switcher and the clamp

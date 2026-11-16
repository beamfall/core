# Oscillux licensing

Oscillux is free software under the GNU Affero General Public License, version 3 only
([LICENSE](LICENSE), SPDX `AGPL-3.0-only`), the same licence as Beamfall core.

Copyright Russell Lewis.

## What the licence allows

- Using, studying, changing and sharing Oscillux, for any purpose, commercial or not.
- Passing on copies or changed versions, in source or compiled form, under the same licence and
  with the corresponding source, as the licence's conveying terms require.
- Running a changed version for users over a network, as long as those users are offered its
  corresponding source (section 13).

## Separate licences

As the copyright holder, Russell Lewis can also license Oscillux under other terms. Beamfall's
official products use Oscillux under a separate permission from the owner instead of the AGPL.
That permission covers only those official products. It does not extend to forks, to derived or
unrelated products, or to any third party: everyone else, including anyone who forks this
repository, uses Oscillux under the AGPL. For other terms, ask the owner.

## Test material

The conformance vectors in this repository are generated from Oscillux's own Python oracles and
synthetic signals and are covered by the AGPL like the rest of the source. Some constants were
tuned on annotated recordings; those recordings and their annotations are not part of this
repository.

## Rules

- Every source file starts with `SPDX-License-Identifier: AGPL-3.0-only`.
- Outside contributions need the [contributor agreement](CONTRIBUTOR-AGREEMENT.md) before merge
  ([CONTRIBUTING.md](CONTRIBUTING.md)), so the owner can keep offering Oscillux under separate
  licences as well as the AGPL.
- Third-party dependencies keep their own licences; this file governs only Oscillux's own source.
  Notices for shader snippets: [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).
  Dependencies stay on the permissive allow-list in `tools/check-licenses.py` (`make licenses`).

## Before a separate licence or release

The owner may license only material for which the owner has sufficient rights. Before a separate
licence or a public release, record the exact included material and verify ownership, or a
sufficient contributor grant, for every contribution and dependency. Unknown rights block
inclusion; a clean build, signed artifact or provenance assertion is not legal clearance. Keep
third-party notices wherever they apply, and check that the source, binaries and notices agree.

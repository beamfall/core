# Plugin provenance and how your plugin is presented

Author-facing summary of the rules that govern how the Console presents your plugin. The full
decision, rationale, and rejected alternatives live in
`../plans/roadmap/evidence/gcr-008-plugin-ui.md`.
Binding sources: ADR-0196 §3/§4, ADR-0130 §1, ADR-0191 §2.1/§2.5.

## You cannot declare your own standing

Your bundle has no author, publisher, vendor, or provenance field, and it will not get one. The
Console resolves your plugin's **provenance** — First-Party, Third-Party, or Sideloaded — from
which signing key and root authenticated the artifact, on the server, at load time. Nothing in
your manifest influences it. If the loader cannot attribute your artifact to a Beamfall root, it
resolves to Sideloaded; it never falls back to First-Party.

Provenance is separate from **trust state** (Trusted / Tampered / Revoked / Unrecognized /
Unsigned), which is about artifact integrity, not authorship. Both are shown together on your row.

## What you control, and what it buys you

You control `DisplayName` (and the other locale strings). It is rendered as plain text in a slot
the Core owns. Markup, glyphs, badge-like characters, and authority-suggesting wording are not
interpreted and grant nothing: a plugin named `Beamfall Official ✓` renders that literal string
next to a chip that says what it actually is. There is no first-party trust mark to earn or
imitate — every row carries a provenance chip, including first-party rows, so an unlabelled row is
a bug rather than a claim.

You do not deliver rendering code, templates with logic, or rendering instructions to any client.
That ceiling is permanent (ADR-0130 §1); layouts and labels are data for a renderer fixed at
review time.

## Provenance does not gate capability

Being first-party grants no permission, no outbound access, no scraper endpoint, and no update
channel. What your plugin may do derives from its trust state, registry membership, and the
permissions the **operator** explicitly granted. In particular, declaring an outbound origin in
your manifest is your *claim*; the operator's per-source approval is what actually opens it.

The practical difference you will see is on *installability*, not trust: bundles compiled into the
server binary cannot be uninstalled or toggled off the way an installed plugin can, so those rows
show disabled controls with an explanation rather than hidden ones.

## Today's reality

Runtime-installable third-party bundles are deferred (ADR-0191 §2.5). Today, in-server plugins
compile into the binary, so building against the current bundle shape means fork-and-rebuild — it
is pre-1.0 and is not an external contract. If you are extending Beamfall from outside, the
supported surface is out-of-process: the `/v1/` API with scoped tokens, the SSE event stream, and
Console-native webhooks (ADR-0191 §2.1).

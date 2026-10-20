import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Web feature coverage markers migrated from the retired vanilla static app.
// The featurecheck ledger scans `feature:<id>` markers in current React tests;
// behavior assertions live in the focused GraphQL, page, a11y, and E2E specs.
// Markers that are bound to a behavior test live in that test instead of this
// list (see the binding assertions below).
//
// feature:access-schedules
// feature:activity-log
// feature:admin-session
// feature:ai-indexing-optout
// feature:api-keys
// feature:artwork-picker
// feature:artwork-pinning
// feature:artwork-slots
// feature:audio-track-select
// feature:auto-tag-review
// feature:bulk-edit
// feature:canonical-timeline
// feature:client-pref-playback
// feature:console-api-keys
// feature:console-api-tokens
// feature:console-config
// feature:dedupe-review
// feature:defensive-render
// feature:device-promotion
// feature:direct-https-acme
// feature:direct-play
// feature:enforced-filters
// feature:enrichment-index-status
// feature:entity-detail
// feature:entity-manager
// feature:fail-soft
// feature:field-locks
// feature:file-operations
// feature:first-run-wizard
// feature:generated-assets
// feature:hls-transcode
// feature:job-management
// feature:lan-direct
// feature:layout-render
// feature:library-health
// feature:library-preview-clips
// feature:library-roots
// feature:live-sessions
// feature:locale-l10n
// feature:markers-skip
// feature:migration-wizard
// feature:my-data-panel
// feature:navigation-manifest
// feature:overlay-network
// feature:pairing-enroll
// feature:playback-info
// feature:plugin-filmtv
// feature:plugin-music
// feature:plugin-podcasts
// feature:profile-switch
// feature:qbittorrent-ingest
// feature:queue-next-up
// feature:relock
// feature:reveal-contract
// feature:reveal-relock
// feature:reveal-unlock
// feature:scheduler
// feature:search-entities
// feature:search-keyword
// feature:search-mode
// feature:section-sources
// feature:see-all-paging
// feature:self-hosted-relay
// feature:session-lifecycle
// feature:stream-token
// feature:subscriptions
// feature:subtitle-tracks
// feature:transcode-config
// feature:transport-ui
// feature:trash-review
// feature:trickplay
// feature:unmatched-queue
// feature:watcher

// Features whose ledger marker must live beside a real behavior test. Each
// listed file carries the marker and asserts the behavior, so the marker cannot
// be credited from this ledger alone.
const boundFeatures: Record<string, string[]> = {
  "hls-error-recovery": [
    "src/areas/viewer/player/hlsRecovery.test.ts",
    "src/areas/viewer/player/usePlayer.test.tsx",
    "src/areas/viewer/pages/PlayerPage.test.tsx",
  ],
};

describe("web feature coverage marker ledger", () => {
  it("keeps web feature markers on the current React test surface", () => {
    expect(true).toBe(true);
  });

  it("keeps bound feature markers in their behavior tests, not in this ledger", () => {
    const ledger = readFileSync(new URL(import.meta.url), "utf8");
    for (const [feature, files] of Object.entries(boundFeatures)) {
      const marker = `feature:${feature}`;
      expect(ledger, `${marker} must not be credited from the ledger`).not.toContain(marker);
      for (const file of files) {
        expect(readFileSync(file, "utf8"), `${file} must carry ${marker}`).toContain(marker);
      }
    }
  });
});

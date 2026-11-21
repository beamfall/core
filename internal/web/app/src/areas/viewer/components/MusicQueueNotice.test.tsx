import { readFileSync } from "node:fs";
import { join } from "node:path";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { I18nProvider } from "@/lib/i18n";
import { MiniPlayer } from "../player/MiniPlayer";
import { MusicQueueNotice, showMusicQueueNotice } from "./MusicQueueNotice";

const remoteCSS = readFileSync(join(__dirname, "../remote/remote.css"), "utf8");
const miniPlayerCSS = readFileSync(join(__dirname, "../player/mini-player.css"), "utf8");
const noticeCSS = readFileSync(join(__dirname, "music-queue-notice.css"), "utf8");

afterEach(cleanup);

function Viewer({ docked }: { docked: boolean }) {
  return (
    <I18nProvider>
      <div data-register="viewer">
        <style>{remoteCSS + miniPlayerCSS + noticeCSS}</style>
        <MusicQueueNotice />
        {docked && (
          <MiniPlayer
            videoRef={{ current: null }}
            title="Track One"
            artist=""
            artworkURL=""
            mediaID="track-1"
            songScale={1}
            onOpen={() => {}}
            onStop={() => {}}
          />
        )}
      </div>
    </I18nProvider>
  );
}

it("raises the notice above the mini-player and lets pointer events pass through", () => {
  showMusicQueueNotice("confirmed", "tail");
  const { rerender } = render(<Viewer docked={false} />);
  const notice = screen.getByRole("status");
  expect(getComputedStyle(notice).bottom).toBe("16px");
  expect(getComputedStyle(notice).pointerEvents).toBe("none");

  rerender(<Viewer docked />);
  expect(getComputedStyle(notice).bottom).toBe("calc(16px + var(--bf-mini-height))");
  expect(getComputedStyle(notice).pointerEvents).toBe("none");

  rerender(<Viewer docked={false} />);
  expect(getComputedStyle(notice).bottom).toBe("16px");
});

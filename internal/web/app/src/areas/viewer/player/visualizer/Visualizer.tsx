import { type RefObject, useEffect, useRef, useState } from "react";
import type { PlaybackStream } from "../../lib/playback";
import { decodable } from "../useGaplessAudio";
import { decodeTrack, silentFrame, TrackAnalysis } from "./analysis";
import { loadOscillux } from "./oscillux";
import { Renderer } from "./renderer";
import { visualFor } from "./visuals";

// The longest step one frame advances the visual, so a tab coming back from the background
// does not jump its clock.
const MAX_DT = 0.1;
// Render no more pixels than this: the visual sits behind the artwork card.
const MAX_DPR = 2;

/** True where this browser can draw an Oscillux visual. */
export function canVisualize(): boolean {
  return (
    typeof WebGL2RenderingContext !== "undefined" && typeof OfflineAudioContext !== "undefined"
  );
}

// The pinned Oscillux visualizer for an audio item (CORE-1309): one of the release's visuals,
// reacting to the playing track and passed through Oscillux's frame flash limiter. It draws
// only for a direct-play stream from this origin, which it decodes for analysis; otherwise,
// or without hardware WebGL2, the slot stays empty. A failure at any point leaves the slot empty too:
// the visual is decoration and never interrupts playback.
export function Visualizer({
  videoRef,
  stream,
  trackKey,
}: {
  videoRef: RefObject<HTMLVideoElement | null>;
  stream: PlaybackStream | null;
  /** Stable per track: picks the visual. */
  trackKey: string;
}) {
  const url = (decodable(stream) && stream?.url) || "";
  const [failed, setFailed] = useState(false);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const rendererRef = useRef<Renderer | null>(null);
  const analysisRef = useRef<TrackAnalysis | null>(null);
  const keyRef = useRef(trackKey);
  const enabled = !!url && !failed && canVisualize();

  // The renderer and its draw loop, for as long as the slot is shown.
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!enabled || !canvas) return;
    let alive = true;
    let frame = 0;
    let last = 0;
    const silent = silentFrame();
    const reduced = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    const fail = (err: unknown) => {
      if (!alive) return;
      console.warn("visualizer: off", err);
      setFailed(true);
    };
    const tick = (now: number) => {
      frame = requestAnimationFrame(tick);
      const renderer = rendererRef.current;
      const video = videoRef.current;
      const dt = last ? Math.min(MAX_DT, (now - last) / 1000) : 1 / 60;
      last = now;
      if (!renderer || !video || video.paused || document.hidden) return;
      const rect = canvas.getBoundingClientRect();
      const dpr = Math.min(MAX_DPR, window.devicePixelRatio || 1);
      try {
        const audio = analysisRef.current?.sample(video.currentTime * 1000) ?? silent;
        renderer.draw(
          Math.round(rect.width * dpr),
          Math.round(rect.height * dpr),
          dt,
          audio,
          !!reduced?.matches,
        );
      } catch (err) {
        cancelAnimationFrame(frame);
        fail(err);
      }
    };
    loadOscillux()
      .then((osc) => Renderer.create(canvas, osc))
      .then((renderer) => {
        // No hardware WebGL2: the slot stays empty, as without WebGL2 at all.
        if (!renderer) {
          if (alive) setFailed(true);
          return;
        }
        if (!alive) return renderer.dispose();
        rendererRef.current = renderer;
        return renderer.use(visualFor(keyRef.current)).then(() => {
          if (alive) frame = requestAnimationFrame(tick);
        });
      })
      .catch(fail);
    const lost = (event: Event) => {
      event.preventDefault();
      fail(new Error("WebGL context lost"));
    };
    canvas.addEventListener("webglcontextlost", lost);
    return () => {
      alive = false;
      cancelAnimationFrame(frame);
      canvas.removeEventListener("webglcontextlost", lost);
      rendererRef.current?.dispose();
      rendererRef.current = null;
    };
  }, [enabled, videoRef]);

  // The visual for this track, applied now or once the renderer is ready.
  useEffect(() => {
    keyRef.current = trackKey;
    rendererRef.current?.use(visualFor(trackKey)).catch((err) => {
      console.warn("visualizer: off", err);
      setFailed(true);
    });
  }, [trackKey]);

  // The track's analysis. Until it is decoded the visual draws with silence.
  useEffect(() => {
    if (!enabled) return;
    const abort = new AbortController();
    let analysis: TrackAnalysis | null = null;
    Promise.all([loadOscillux(), decodeTrack(url, abort.signal)])
      .then(([osc, pcm]) => {
        if (abort.signal.aborted) return;
        analysis = new TrackAnalysis(osc, pcm);
        analysisRef.current = analysis;
      })
      .catch((err) => {
        // Without analysis the visual keeps drawing, idle.
        if (!abort.signal.aborted) console.warn("visualizer: no analysis", err);
      });
    return () => {
      abort.abort();
      if (analysisRef.current === analysis) analysisRef.current = null;
      analysis?.free();
    };
  }, [enabled, url]);

  if (!enabled) return null;
  return <canvas ref={canvasRef} className="bf-audio-visualizer-canvas" />;
}

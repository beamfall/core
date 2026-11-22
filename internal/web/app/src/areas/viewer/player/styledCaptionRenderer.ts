import JASSUB from "jassub";
import { publishedSubtitleOffset, SUBTITLE_OFFSET_EVENT } from "./playbackSync";

async function fetchCaption(url: URL, signal: AbortSignal): Promise<Response> {
  const response = await fetch(url, { signal, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) throw new Error("Could not load styled subtitle resource");
  return response;
}

export async function startStyledCaption(
  video: HTMLVideoElement,
  source: string,
  signal: AbortSignal,
  onRendererError: () => void,
): Promise<void> {
  const url = new URL(source, document.baseURI);
  const bytes = await (await fetchCaption(url, signal)).arrayBuffer();
  let subContent: string;
  try {
    subContent = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    // Raw sidecars preserve their original encoding on the wire.
    subContent = new TextDecoder("windows-1252").decode(bytes);
  }
  const fonts: string[] = [];
  if (url.pathname.includes("/api/media/subtitle/")) {
    url.pathname += "/fonts";
    const indexes: number[] = await (await fetchCaption(url, signal)).json();
    for (const index of indexes) {
      fonts.push(new URL(`${url.pathname}/${index}`, url).href);
    }
  }
  signal.throwIfAborted();

  const canvas = document.createElement("canvas");
  canvas.style.position = "absolute";
  canvas.style.pointerEvents = "none";
  video.insertAdjacentElement("afterend", canvas);
  try {
    // Attach the video only after WASM is ready so a failed initialization
    // never leaves JASSUB's frame loop waiting on the worker.
    const renderer = new JASSUB({ canvas, subContent, fonts, queryFonts: false });
    const worker = renderer._worker;
    const dispose = () => {
      worker.removeEventListener("error", onRendererError);
      worker.removeEventListener("messageerror", onRendererError);
      // destroy() waits on an RPC, which may never finish after a worker failure.
      void renderer.destroy().catch(() => {});
      worker.terminate();
    };
    signal.addEventListener("abort", dispose, { once: true });
    worker.addEventListener("error", onRendererError);
    worker.addEventListener("messageerror", onRendererError);
    await renderer.ready;
    signal.throwIfAborted();
    // JASSUB 2.5.36 logs rejected draws without surfacing them. Wrap the
    // local RPC proxy so assignments never modify methods in the worker.
    const remote = renderer.renderer;
    const draw = async (...args: Parameters<typeof remote._draw>) => {
      try {
        await remote._draw(...args);
      } catch (error) {
        if (!signal.aborted) onRendererError();
        throw error;
      }
    };
    renderer.renderer = new Proxy(remote, {
      get: (target, key) => (key === "_draw" ? draw : Reflect.get(target, key)),
    });
    await renderer.setVideo(video);
    signal.throwIfAborted();
    const repaintIfPaused = async () => {
      if (!video.paused || video.videoWidth <= 0 || video.videoHeight <= 0) return;
      await renderer.manualRender(
        {
          expectedDisplayTime: performance.now(),
          width: video.videoWidth,
          height: video.videoHeight,
          mediaTime: video.currentTime,
        },
        true,
      );
    };
    // JASSUB draws at mediaTime + timeOffset; a positive subtitle offset shows
    // cues later (CORE-1671), so it looks the cue up that far back.
    const followOffset = () => {
      renderer.timeOffset = -publishedSubtitleOffset(video);
    };
    const onOffset = () => {
      followOffset();
      void repaintIfPaused().catch(() => {});
    };
    followOffset();
    video.addEventListener(SUBTITLE_OFFSET_EVENT, onOffset);
    signal.addEventListener(
      "abort",
      () => video.removeEventListener(SUBTITLE_OFFSET_EVENT, onOffset),
      {
        once: true,
      },
    );
    await repaintIfPaused();
  } catch (error) {
    canvas.remove();
    throw error;
  }
}

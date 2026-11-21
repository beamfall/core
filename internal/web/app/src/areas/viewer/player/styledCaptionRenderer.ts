import JASSUB from "jassub";

async function fetchCaption(url: URL, signal: AbortSignal): Promise<Response> {
  const response = await fetch(url, { signal, credentials: "same-origin", cache: "no-store" });
  if (!response.ok) throw new Error("Could not load styled subtitle resource");
  return response;
}

export async function startStyledCaption(
  video: HTMLVideoElement,
  source: string,
  signal: AbortSignal,
  onWorkerError: () => void,
): Promise<void> {
  const url = new URL(source, document.baseURI);
  const subContent = await (await fetchCaption(url, signal)).text();
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
      worker.removeEventListener("error", onWorkerError);
      worker.removeEventListener("messageerror", onWorkerError);
      // destroy() waits on an RPC, which may never finish after a worker failure.
      void renderer.destroy().catch(() => {});
      worker.terminate();
    };
    signal.addEventListener("abort", dispose, { once: true });
    worker.addEventListener("error", onWorkerError);
    worker.addEventListener("messageerror", onWorkerError);
    await renderer.ready;
    signal.throwIfAborted();
    await renderer.setVideo(video);
    if (video.paused && video.videoWidth > 0 && video.videoHeight > 0) {
      await renderer.manualRender(
        {
          expectedDisplayTime: performance.now(),
          width: video.videoWidth,
          height: video.videoHeight,
          mediaTime: video.currentTime,
        },
        true,
      );
    }
  } catch (error) {
    canvas.remove();
    throw error;
  }
}

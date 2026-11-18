import { ImagePlus, Trash2 } from "lucide-react";
import { type KeyboardEvent, type PointerEvent, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { removeProfilePicture, uploadProfilePicture } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { ProfileAvatar } from "./ProfileAvatar";

// The crop stage, in CSS pixels, and the picture the browser sends. The
// server re-encodes whatever arrives (CORE-1240) and fills any transparency
// with its own backdrop, so the crop goes up as a lossless PNG; cropping here
// lets the person choose the framing instead of taking the server's center
// square.
const STAGE = 240;
const OUTPUT = 512;
const MAX_ZOOM = 4;
const PAN_STEP = 12;

type Crop = { zoom: number; x: number; y: number };
type Source = { url: string; image: HTMLImageElement };

/** The image's on-stage scale, covering the square at zoom 1. */
function scaleFor(image: HTMLImageElement, zoom: number) {
  return (STAGE / Math.min(image.naturalWidth, image.naturalHeight)) * zoom;
}

/** Keep the image covering the stage: no empty edge may show. */
function clamp(image: HTMLImageElement, crop: Crop): Crop {
  const scale = scaleFor(image, crop.zoom);
  const minX = STAGE - image.naturalWidth * scale;
  const minY = STAGE - image.naturalHeight * scale;
  return {
    zoom: crop.zoom,
    x: Math.min(0, Math.max(minX, crop.x)),
    y: Math.min(0, Math.max(minY, crop.y)),
  };
}

function centered(image: HTMLImageElement): Crop {
  const scale = scaleFor(image, 1);
  return {
    zoom: 1,
    x: (STAGE - image.naturalWidth * scale) / 2,
    y: (STAGE - image.naturalHeight * scale) / 2,
  };
}

/** Zoom about the stage center, so the subject stays put. */
function zoomTo(image: HTMLImageElement, crop: Crop, zoom: number): Crop {
  const before = scaleFor(image, crop.zoom);
  const after = scaleFor(image, zoom);
  const cx = (STAGE / 2 - crop.x) / before;
  const cy = (STAGE / 2 - crop.y) / before;
  return clamp(image, { zoom, x: STAGE / 2 - cx * after, y: STAGE / 2 - cy * after });
}

async function renderCrop(image: HTMLImageElement, crop: Crop): Promise<Blob> {
  const scale = scaleFor(image, crop.zoom);
  const canvas = document.createElement("canvas");
  canvas.width = OUTPUT;
  canvas.height = OUTPUT;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("canvas unavailable");
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(
    image,
    -crop.x / scale,
    -crop.y / scale,
    STAGE / scale,
    STAGE / scale,
    0,
    0,
    OUTPUT,
    OUTPUT,
  );
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error("encode failed"))),
      "image/png",
    ),
  );
}

// The page's CSP allows data: images but not blob: ones, so the picked file is
// read as a data URL rather than an object URL.
function loadImage(file: File): Promise<Source> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onerror = () => reject(new Error("unreadable file"));
    reader.onload = () => {
      const url = String(reader.result);
      const image = new Image();
      image.onload = () => resolve({ url, image });
      image.onerror = () => reject(new Error("unreadable image"));
      image.src = url;
    };
    reader.readAsDataURL(file);
  });
}

/**
 * Upload, frame and remove a Profile's picture (CORE-1240). Picking a file
 * opens a square crop stage: drag or use the arrow keys to move the picture,
 * and the slider to zoom. The framed square is sent to the server, which
 * stores its own normalized copy and serves it to every device.
 */
export function ProfilePhotoEditor({
  profile,
  onChanged,
}: {
  profile: { id: string; name: string; avatar?: string; pictureURL?: string };
  onChanged: (pictureURL: string) => Promise<void> | void;
}) {
  const t = useT();
  const inputID = useId();
  const zoomID = useId();
  const fileInput = useRef<HTMLInputElement>(null);
  const [source, setSource] = useState<Source | null>(null);
  const [crop, setCrop] = useState<Crop>({ zoom: 1, x: 0, y: 0 });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<"read" | "save" | "remove" | null>(null);
  const drag = useRef<{ pointer: number; x: number; y: number; crop: Crop } | null>(null);

  async function pick(file: File | undefined) {
    if (!file) return;
    setError(null);
    try {
      const next = await loadImage(file);
      setSource(next);
      setCrop(centered(next.image));
    } catch {
      setError("read");
    }
  }

  async function save() {
    if (!source) return;
    setBusy(true);
    setError(null);
    try {
      const url = await uploadProfilePicture(profile.id, await renderCrop(source.image, crop));
      setSource(null);
      await onChanged(url);
    } catch {
      setError("save");
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await removeProfilePicture(profile.id);
      await onChanged("");
    } catch {
      setError("remove");
    } finally {
      setBusy(false);
    }
  }

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    e.currentTarget.setPointerCapture?.(e.pointerId);
    drag.current = { pointer: e.pointerId, x: e.clientX, y: e.clientY, crop };
  }

  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    const start = drag.current;
    if (!source || !start || start.pointer !== e.pointerId) return;
    setCrop(
      clamp(source.image, {
        zoom: start.crop.zoom,
        x: start.crop.x + e.clientX - start.x,
        y: start.crop.y + e.clientY - start.y,
      }),
    );
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (!source) return;
    const delta = {
      ArrowLeft: [PAN_STEP, 0],
      ArrowRight: [-PAN_STEP, 0],
      ArrowUp: [0, PAN_STEP],
      ArrowDown: [0, -PAN_STEP],
    }[e.key];
    if (!delta) return;
    e.preventDefault();
    setCrop((c) => clamp(source.image, { ...c, x: c.x + delta[0], y: c.y + delta[1] }));
  }

  const errorKey =
    error === "read"
      ? "profilePhotoReadFailed"
      : error === "save"
        ? "profilePhotoSaveFailed"
        : error === "remove"
          ? "profilePhotoRemoveFailed"
          : null;

  if (source) {
    const scale = scaleFor(source.image, crop.zoom);
    return (
      <div className="flex flex-col items-center gap-4" data-slot="profile-photo-editor">
        <div
          role="img"
          aria-label={t("profilePhotoStageLabel")}
          tabIndex={0}
          className="bf-photo-stage"
          style={{ width: STAGE, height: STAGE }}
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={() => {
            drag.current = null;
          }}
          onPointerCancel={() => {
            drag.current = null;
          }}
          onKeyDown={onKeyDown}
        >
          <img
            src={source.url}
            alt=""
            draggable={false}
            style={{
              width: source.image.naturalWidth * scale,
              height: source.image.naturalHeight * scale,
              transform: `translate(${crop.x}px, ${crop.y}px)`,
            }}
          />
        </div>
        <p className="m-0 text-xs text-muted-foreground">{t("profilePhotoStageHint")}</p>
        <div className="flex w-full max-w-60 items-center gap-3">
          <label htmlFor={zoomID} className="text-sm">
            {t("profilePhotoZoom")}
          </label>
          <input
            id={zoomID}
            type="range"
            min={1}
            max={MAX_ZOOM}
            step={0.01}
            value={crop.zoom}
            onChange={(e) => setCrop((c) => zoomTo(source.image, c, Number(e.target.value)))}
            className="bf-photo-zoom flex-1"
          />
        </div>
        {errorKey && (
          <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
            {t(errorKey)}
          </p>
        )}
        <div className="flex gap-2">
          <Button type="button" variant="outline" disabled={busy} onClick={() => setSource(null)}>
            {t("profilePhotoCancel")}
          </Button>
          <Button type="button" disabled={busy} onClick={() => void save()}>
            {t("profilePhotoSave")}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3" data-slot="profile-photo-editor">
      <div className="flex flex-wrap items-center gap-4">
        <ProfileAvatar profile={profile} size="lg" />
        <div className="flex flex-wrap gap-2">
          <input
            ref={fileInput}
            id={inputID}
            type="file"
            accept="image/*"
            className="sr-only"
            tabIndex={-1}
            aria-label={t(profile.pictureURL ? "profilePhotoReplace" : "profilePhotoUpload")}
            onChange={(e) => {
              void pick(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
          <Button
            type="button"
            variant="outline"
            disabled={busy}
            onClick={() => fileInput.current?.click()}
          >
            <ImagePlus aria-hidden />
            {t(profile.pictureURL ? "profilePhotoReplace" : "profilePhotoUpload")}
          </Button>
          {profile.pictureURL && (
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void remove()}>
              <Trash2 aria-hidden />
              {t("profilePhotoRemove")}
            </Button>
          )}
        </div>
      </div>
      <p className="m-0 text-sm text-muted-foreground">{t("profilePhotoLead")}</p>
      {errorKey && (
        <p role="alert" className="m-0 text-sm text-[var(--state-danger)]">
          {t(errorKey)}
        </p>
      )}
    </div>
  );
}

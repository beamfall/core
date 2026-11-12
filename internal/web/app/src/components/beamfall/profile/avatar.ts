import {
  Bird,
  Cat,
  Clapperboard,
  Crown,
  Dog,
  Flame,
  Gamepad2,
  Ghost,
  Heart,
  Leaf,
  type LucideIcon,
  Moon,
  Music,
  Rocket,
  Star,
  Sun,
} from "lucide-react";
import type { StringKey } from "@/strings";

// A Profile's avatar is an opaque token the server stores and returns
// unchanged (CORE-1196). This client writes "<picture>.<tone>"; a token it does
// not recognise (another client's, or none) falls back to a generated avatar:
// the Profile's initial on a tone derived from its id, so every Profile always
// has a stable, distinct avatar without anyone choosing one.

/** The reserved per-Profile client pref key that holds the avatar token. */
export const PROFILE_AVATAR_PREF_KEY = "profile.avatar";

export const AVATAR_PICTURES = [
  { id: "rocket", icon: Rocket, label: "avatarPictureRocket" },
  { id: "star", icon: Star, label: "avatarPictureStar" },
  { id: "heart", icon: Heart, label: "avatarPictureHeart" },
  { id: "moon", icon: Moon, label: "avatarPictureMoon" },
  { id: "sun", icon: Sun, label: "avatarPictureSun" },
  { id: "cat", icon: Cat, label: "avatarPictureCat" },
  { id: "dog", icon: Dog, label: "avatarPictureDog" },
  { id: "bird", icon: Bird, label: "avatarPictureBird" },
  { id: "ghost", icon: Ghost, label: "avatarPictureGhost" },
  { id: "gamepad", icon: Gamepad2, label: "avatarPictureGamepad" },
  { id: "music", icon: Music, label: "avatarPictureMusic" },
  { id: "film", icon: Clapperboard, label: "avatarPictureFilm" },
  { id: "leaf", icon: Leaf, label: "avatarPictureLeaf" },
  { id: "flame", icon: Flame, label: "avatarPictureFlame" },
  { id: "crown", icon: Crown, label: "avatarPictureCrown" },
] as const satisfies readonly { id: string; icon: LucideIcon; label: StringKey }[];

export const AVATAR_TONES = [
  { label: "avatarToneAmber" },
  { label: "avatarToneAqua" },
  { label: "avatarToneGreen" },
  { label: "avatarToneBlue" },
  { label: "avatarToneViolet" },
  { label: "avatarToneRed" },
  { label: "avatarToneTeal" },
  { label: "avatarToneSilver" },
] as const satisfies readonly { label: StringKey }[];

export type AvatarPictureID = (typeof AVATAR_PICTURES)[number]["id"];

export interface AvatarChoice {
  /** The chosen picture, or null for the Profile's initial. */
  picture: AvatarPictureID | null;
  /** Index into AVATAR_TONES. */
  tone: number;
}

/** A stable tone for a Profile that has not chosen one. */
export function generatedTone(profileID: string): number {
  let hash = 0;
  for (let i = 0; i < profileID.length; i++) {
    hash = (hash * 31 + profileID.charCodeAt(i)) >>> 0;
  }
  return hash % AVATAR_TONES.length;
}

/** Reads an avatar token; anything unrecognised resolves to the generated avatar. */
export function resolveAvatar(token: string | undefined, profileID: string): AvatarChoice {
  const [pictureID, toneText, ...rest] = (token ?? "").split(".");
  const tone = Number(toneText);
  const toneKnown = rest.length === 0 && /^\d$/.test(toneText ?? "") && tone < AVATAR_TONES.length;
  if (!toneKnown) return { picture: null, tone: generatedTone(profileID) };
  if (pictureID === "initial") return { picture: null, tone };
  const picture = AVATAR_PICTURES.find((p) => p.id === pictureID);
  if (!picture) return { picture: null, tone: generatedTone(profileID) };
  return { picture: picture.id, tone };
}

/** The token this client stores for a choice. */
export function avatarToken(choice: AvatarChoice): string {
  return `${choice.picture ?? "initial"}.${choice.tone}`;
}

/** The first letter of a Profile name, for the generated avatar. */
export function profileInitial(name: string): string {
  const first = Array.from(name.trim())[0];
  return first ? first.toLocaleUpperCase() : "";
}

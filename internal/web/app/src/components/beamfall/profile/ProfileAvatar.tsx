import { useState } from "react";
import { cn } from "@/lib/utils";
import { AVATAR_PICTURES, type AvatarChoice, profileInitial, resolveAvatar } from "./avatar";
import "./profile.css";

export type AvatarSize = "sm" | "md" | "lg" | "xl";

/**
 * A Profile's avatar: its uploaded picture framed in its colour, else its
 * chosen icon on its colour, else its initial on a generated colour.
 * Decorative; the Profile name beside it is the label. Pass `choice` to
 * preview an icon and colour that have not been saved yet.
 */
export function ProfileAvatar({
  profile,
  choice,
  size = "md",
  className,
}: {
  profile: { id: string; name: string; avatar?: string; pictureURL?: string };
  choice?: AvatarChoice;
  size?: AvatarSize;
  className?: string;
}) {
  const { picture, tone } = choice ?? resolveAvatar(profile.avatar, profile.id);
  const Icon = AVATAR_PICTURES.find((p) => p.id === picture)?.icon;
  // A picture that fails to load (removed elsewhere, offline) falls back to
  // the icon or initial rather than a broken image.
  const [failedURL, setFailedURL] = useState<string | null>(null);
  const pictureURL =
    profile.pictureURL && profile.pictureURL !== failedURL ? profile.pictureURL : "";
  return (
    <span
      aria-hidden
      data-slot="profile-avatar"
      data-size={size}
      data-tone={tone}
      data-picture={pictureURL ? "" : undefined}
      className={cn("bf-avatar", className)}
    >
      {pictureURL ? (
        <img
          src={pictureURL}
          alt=""
          draggable={false}
          decoding="async"
          className="bf-avatar-picture"
          onError={() => setFailedURL(pictureURL)}
        />
      ) : Icon ? (
        <Icon className="bf-avatar-icon" />
      ) : (
        profileInitial(profile.name)
      )}
    </span>
  );
}

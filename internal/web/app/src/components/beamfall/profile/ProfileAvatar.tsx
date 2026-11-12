import { cn } from "@/lib/utils";
import { AVATAR_PICTURES, type AvatarChoice, profileInitial, resolveAvatar } from "./avatar";
import "./profile.css";

export type AvatarSize = "sm" | "md" | "lg" | "xl";

/**
 * A Profile's avatar: its chosen picture on its chosen colour, or its initial
 * on a generated colour. Decorative; the Profile name beside it is the label.
 * Pass `choice` to preview an avatar that has not been saved yet.
 */
export function ProfileAvatar({
  profile,
  choice,
  size = "md",
  className,
}: {
  profile: { id: string; name: string; avatar?: string };
  choice?: AvatarChoice;
  size?: AvatarSize;
  className?: string;
}) {
  const { picture, tone } = choice ?? resolveAvatar(profile.avatar, profile.id);
  const Icon = AVATAR_PICTURES.find((p) => p.id === picture)?.icon;
  return (
    <span
      aria-hidden
      data-slot="profile-avatar"
      data-size={size}
      data-tone={tone}
      className={cn("bf-avatar", className)}
    >
      {Icon ? <Icon className="bf-avatar-icon" /> : profileInitial(profile.name)}
    </span>
  );
}

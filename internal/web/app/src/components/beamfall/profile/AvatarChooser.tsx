import { useEffect, useId, useState } from "react";
import { useT } from "@/lib/i18n";
import {
  AVATAR_PICTURES,
  AVATAR_TONES,
  type AvatarChoice,
  avatarToken,
  resolveAvatar,
} from "./avatar";
import { ProfileAvatar } from "./ProfileAvatar";

/**
 * Picture and colour pickers for one Profile's avatar, with a live preview.
 * Each pick is saved straight away through `onSave`; a rejected save puts the
 * previous avatar back and the caller reports the failure.
 */
export function AvatarChooser({
  profile,
  onSave,
}: {
  profile: { id: string; name: string; avatar?: string };
  onSave: (token: string) => Promise<void>;
}) {
  const t = useT();
  const groupName = useId();
  const [choice, setChoice] = useState(() => resolveAvatar(profile.avatar, profile.id));

  // Follow the saved avatar when it changes underneath (refresh, other tab).
  useEffect(() => {
    setChoice(resolveAvatar(profile.avatar, profile.id));
  }, [profile.avatar, profile.id]);

  function pick(next: AvatarChoice) {
    const previous = choice;
    setChoice(next);
    onSave(avatarToken(next)).catch(() => setChoice(previous));
  }

  return (
    <div className="flex flex-col gap-5 sm:flex-row sm:items-start">
      <ProfileAvatar profile={profile} choice={choice} size="lg" className="self-center" />
      <div className="flex min-w-0 flex-1 flex-col gap-4">
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="mb-2 p-0 text-sm font-medium">{t("avatarPictureGroup")}</legend>
          <div className="flex flex-wrap gap-1">
            {[{ id: null, label: "avatarPictureInitial" as const }, ...AVATAR_PICTURES].map(
              (picture) => (
                <label
                  key={picture.id ?? "initial"}
                  className="bf-avatar-option"
                  title={t(picture.label)}
                >
                  <input
                    type="radio"
                    className="sr-only"
                    name={`${groupName}-picture`}
                    checked={choice.picture === picture.id}
                    onChange={() => pick({ ...choice, picture: picture.id })}
                    aria-label={t(picture.label)}
                  />
                  <ProfileAvatar profile={profile} choice={{ ...choice, picture: picture.id }} />
                </label>
              ),
            )}
          </div>
        </fieldset>
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="mb-2 p-0 text-sm font-medium">{t("avatarToneGroup")}</legend>
          <div className="flex flex-wrap gap-1">
            {AVATAR_TONES.map((tone, index) => (
              <label key={tone.label} className="bf-avatar-option" title={t(tone.label)}>
                <input
                  type="radio"
                  className="sr-only"
                  name={`${groupName}-tone`}
                  checked={choice.tone === index}
                  onChange={() => pick({ ...choice, tone: index })}
                  aria-label={t(tone.label)}
                />
                <ProfileAvatar
                  profile={{ ...profile, name: "" }}
                  choice={{ picture: null, tone: index }}
                  size="sm"
                  className="bf-avatar-swatch"
                />
              </label>
            ))}
          </div>
        </fieldset>
      </div>
    </div>
  );
}

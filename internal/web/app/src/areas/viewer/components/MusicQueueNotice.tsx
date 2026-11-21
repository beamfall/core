import { useEffect, useState } from "react";
import { useT } from "@/lib/i18n";
import { useMusicQueueSnapshot } from "../lib/musicQueue";

export function MusicQueueNotice({ profileID }: { profileID: string }) {
  const t = useT();
  const { notice } = useMusicQueueSnapshot(profileID);
  const [expired, setExpired] = useState<typeof notice>();
  useEffect(() => {
    if (!notice) return;
    const timer = window.setTimeout(() => setExpired(notice), 4000);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const message = notice && notice !== expired ? t(notice.key) : null;
  return (
    <div role="status" className={message ? "bf-remote-message" : "sr-only"}>
      {message && <p className="bf-remote-message-text">{message}</p>}
    </div>
  );
}

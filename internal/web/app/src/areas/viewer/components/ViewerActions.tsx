import { Search } from "lucide-react";
import { type FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router";
import { useT } from "@/lib/i18n";

// The viewer's header actions (CORE-1131): a search field that opens Search with
// the typed text, and on phones a search icon in its place. Profile, My Data and
// Profile settings live in the AppShell's account menu.
export function ViewerActions() {
  const t = useT();
  const navigate = useNavigate();
  const [text, setText] = useState("");

  function onSubmit(event: FormEvent) {
    event.preventDefault();
    const query = text.trim();
    navigate(query ? `/search?q=${encodeURIComponent(query)}` : "/search");
    setText("");
  }

  return (
    <>
      <form role="search" className="bf-search" onSubmit={onSubmit}>
        <Search aria-hidden className="bf-search-icon" />
        <input
          type="search"
          className="bf-search-input"
          aria-label={t("search")}
          placeholder={t("searchPlaceholder")}
          value={text}
          onChange={(event) => setText(event.target.value)}
        />
      </form>
      <Link to="/search" className="bf-icon-button bf-search-icon-link" aria-label={t("search")}>
        <Search aria-hidden className="size-[18px]" />
      </Link>
    </>
  );
}

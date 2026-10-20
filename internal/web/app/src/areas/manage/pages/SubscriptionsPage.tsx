import { useCallback, useEffect, useRef, useState } from "react";
import {
  EmptyLine,
  ErrorLine,
  ManageField,
  ManageInlineForm,
  ManageSection,
  ManageTable,
  PathPickerField,
} from "@/components/beamfall/workbench";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  addSubscription,
  fetchSubscriptions,
  removeSubscription,
  setSubscriptionRetention,
} from "@/lib/graphql";
import type { SubscriptionInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";

const RETENTION_KINDS = [
  { value: "keep-all", key: "retentionKeepAll" as const },
  { value: "keep-last-n", key: "retentionKeepLastN" as const },
  { value: "delete-after-played", key: "retentionDeleteAfterPlayed" as const },
];

// Subscriptions (ADR-0030): Library-level feed Subscriptions with a retention
// policy. Feed URLs can carry private-feed credentials, so error text is always
// a fixed client string — the URL is never echoed back outside the list. Ported
// from app.js refreshSubscriptions / add-subscription-form. Remove is
// destructive (it drops a feed binding) so it routes through a confirm dialog.
export function SubscriptionsPage() {
  const t = useT();
  const { manifest } = useSession();
  const libraries = manifest?.libraries ?? [];

  const [libraryID, setLibraryID] = useState("");
  const [subscriptions, setSubscriptions] = useState<SubscriptionInfo[]>([]);
  const [error, setError] = useState("");
  const [feedURL, setFeedURL] = useState("");
  const [root, setRoot] = useState("");
  const [authHeader, setAuthHeader] = useState("");
  const loadGeneration = useRef(0);
  const libraryIDRef = useRef(libraryID);
  libraryIDRef.current = libraryID;

  // Default the picker to the first Library once the manifest arrives.
  useEffect(() => {
    if (!libraryID && libraries.length > 0) setLibraryID(libraries[0].id);
  }, [libraries, libraryID]);

  const load = useCallback(
    async (id: string) => {
      const generation = ++loadGeneration.current;
      setError("");
      setSubscriptions([]);
      if (!id) return;
      try {
        const next = await fetchSubscriptions(id);
        if (loadGeneration.current === generation) setSubscriptions(next);
      } catch {
        if (loadGeneration.current === generation) setError(t("subscriptionsUnavailable"));
      }
    },
    [t],
  );

  useEffect(() => {
    void load(libraryID);
    return () => {
      loadGeneration.current += 1;
    };
  }, [libraryID, load]);

  const refreshMutationLibrary = useCallback(
    async (mutationLibraryID: string) => {
      if (libraryIDRef.current !== mutationLibraryID) return false;
      await load(mutationLibraryID);
      return true;
    },
    [load],
  );

  const onAdd = async () => {
    setError("");
    const trimmedFeed = feedURL.trim();
    const mutationLibraryID = libraryID;
    if (!mutationLibraryID || !trimmedFeed) {
      setError(t("pickLibraryFeedRoot"));
      return;
    }
    try {
      await addSubscription({
        libraryID: mutationLibraryID,
        feedURL: trimmedFeed,
        root: root.trim() || null,
        authHeader: authHeader.trim() || null,
      });
      if (libraryIDRef.current !== mutationLibraryID) return;
      setFeedURL("");
      setRoot("");
      setAuthHeader("");
      await refreshMutationLibrary(mutationLibraryID);
    } catch {
      if (libraryIDRef.current === mutationLibraryID) {
        // Fixed message: the feed URL may carry credentials.
        setError(t("couldNotAddSubscription"));
      }
    }
  };

  const onSaveRetention = async (sub: SubscriptionInfo, kind: string, n: number | null) => {
    const mutationLibraryID = libraryID;
    setError("");
    try {
      await setSubscriptionRetention(sub.id, kind, kind === "keep-last-n" ? n : null);
      await refreshMutationLibrary(mutationLibraryID);
    } catch {
      if (libraryIDRef.current === mutationLibraryID) {
        setError(t("couldNotUpdateRetention"));
      }
    }
  };

  const onRemove = async (sub: SubscriptionInfo) => {
    const mutationLibraryID = libraryID;
    setError("");
    try {
      await removeSubscription(sub.id);
      await refreshMutationLibrary(mutationLibraryID);
    } catch {
      if (libraryIDRef.current === mutationLibraryID) setError(t("couldNotRemoveSubscription"));
    }
  };

  return (
    <div className="space-y-4">
      <ManageSection
        title={t("subscriptions")}
        actions={
          <div className="space-y-1">
            <Label htmlFor="subscription-library" className="sr-only">
              {t("library")}
            </Label>
            <Select value={libraryID} onValueChange={setLibraryID}>
              <SelectTrigger id="subscription-library" size="sm" className="w-56">
                <SelectValue placeholder={t("library")} />
              </SelectTrigger>
              <SelectContent>
                {libraries.map((lib) => (
                  <SelectItem key={lib.id} value={lib.id}>
                    {lib.title}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        }
      >
        {subscriptions.length === 0 ? (
          <EmptyLine message={t("noSubscriptions")} />
        ) : (
          <ManageTable>
            <TableHeader>
              <TableRow>
                <TableHead>{t("title")}</TableHead>
                <TableHead>{t("retentionPolicy")}</TableHead>
                <TableHead className="text-right">{t("remove")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {subscriptions.map((sub) => (
                <SubscriptionRow
                  key={sub.id}
                  subscription={sub}
                  onSaveRetention={onSaveRetention}
                  onRemove={onRemove}
                />
              ))}
            </TableBody>
          </ManageTable>
        )}
        <ErrorLine message={error} />
      </ManageSection>

      <ManageSection title={t("addSubscription")}>
        <ManageInlineForm>
          <ManageField id="subscription-feed" label={t("feedURL")}>
            <Input
              id="subscription-feed"
              value={feedURL}
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setFeedURL(e.target.value)}
              className="w-72"
            />
          </ManageField>
          <PathPickerField
            id="subscription-root"
            label={t("downloadFolder")}
            role="downloadDestination"
            value={root}
            onChange={setRoot}
            suppressAppStoreDownload
          />
          <ManageField id="subscription-auth" label={t("authorizationHeader")}>
            <Input
              id="subscription-auth"
              type="password"
              value={authHeader}
              autoComplete="off"
              onChange={(e) => setAuthHeader(e.target.value)}
            />
          </ManageField>
          <Button type="button" onClick={onAdd}>
            {t("addSubscription")}
          </Button>
        </ManageInlineForm>
      </ManageSection>
    </div>
  );
}

function SubscriptionRow({
  subscription,
  onSaveRetention,
  onRemove,
}: {
  subscription: SubscriptionInfo;
  onSaveRetention: (sub: SubscriptionInfo, kind: string, n: number | null) => Promise<void>;
  onRemove: (sub: SubscriptionInfo) => Promise<void>;
}) {
  const t = useT();
  const [kind, setKind] = useState(subscription.retentionKind || "keep-all");
  const [n, setN] = useState(
    subscription.retentionN == null ? "" : String(subscription.retentionN),
  );

  return (
    <TableRow>
      <TableCell className="manage-cell-primary">
        {subscription.title || subscription.feedURL}
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-2">
          <Select value={kind} onValueChange={setKind}>
            <SelectTrigger size="sm" className="w-44" title={t("retentionPolicy")}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {RETENTION_KINDS.map((r) => (
                <SelectItem key={r.value} value={r.value}>
                  {t(r.key)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {kind === "keep-last-n" && (
            <Input
              type="number"
              min={0}
              step={1}
              inputMode="numeric"
              aria-label={t("retentionCount")}
              title={t("retentionCount")}
              value={n}
              onChange={(e) => setN(e.target.value)}
              className="w-20"
            />
          )}
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onSaveRetention(subscription, kind, Number(n || "0"))}
          >
            {t("save")}
          </Button>
        </div>
      </TableCell>
      <TableCell className="text-right">
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button type="button" variant="destructive" size="sm">
              {t("remove")}
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("remove")}</AlertDialogTitle>
              <AlertDialogDescription>
                {subscription.title || subscription.feedURL}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
              <AlertDialogAction onClick={() => onRemove(subscription)}>
                {t("remove")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </TableCell>
    </TableRow>
  );
}

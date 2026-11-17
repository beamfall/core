// CORE-1136 — Refresh all metadata for a Library: the Console card action and its Operations job.
export const libraryMetadataRefreshStrings = {
  en: {
    refreshAllMetadata: "Refresh all metadata",
    confirmRefreshAllMetadata: "Refresh metadata for every item in this library?",
    refreshAllMetadataBody:
      "Each identified item is looked up again with its provider. Locked fields keep their values. This may take a while; you can follow or cancel it in Operations.",
    refreshAllMetadataQueued: "Metadata refresh queued.",
    refreshAllMetadataFollow: "Follow it in Operations",
    jobKindLibraryMetadataRefresh: "Metadata refresh",
    jobKindDescLibraryMetadataRefresh:
      "Looks up every identified item in a library again and applies its provider's current metadata. Locked fields keep their values.",
    jobProgressMessageRefreshingMetadata: "Refreshing metadata",
  },
  fr: {
    refreshAllMetadata: "Actualiser toutes les métadonnées",
    confirmRefreshAllMetadata:
      "Actualiser les métadonnées de tous les éléments de cette bibliothèque ?",
    refreshAllMetadataBody:
      "Chaque élément identifié est recherché à nouveau auprès de son fournisseur. Les champs verrouillés gardent leur valeur. Cela peut prendre un moment ; vous pouvez la suivre ou l’annuler dans Opérations.",
    refreshAllMetadataQueued: "Actualisation des métadonnées en file d’attente.",
    refreshAllMetadataFollow: "Suivre dans Opérations",
    jobKindLibraryMetadataRefresh: "Actualisation des métadonnées",
    jobKindDescLibraryMetadataRefresh:
      "Recherche à nouveau chaque élément identifié d’une bibliothèque et applique les métadonnées actuelles de son fournisseur. Les champs verrouillés gardent leur valeur.",
    jobProgressMessageRefreshingMetadata: "Actualisation des métadonnées",
  },
  de: {
    refreshAllMetadata: "Alle Metadaten aktualisieren",
    confirmRefreshAllMetadata: "Metadaten für alle Elemente dieser Mediathek aktualisieren?",
    refreshAllMetadataBody:
      "Jedes erkannte Element wird erneut beim Anbieter abgefragt. Gesperrte Felder behalten ihren Wert. Das kann eine Weile dauern; unter Betrieb kannst du den Fortschritt verfolgen oder abbrechen.",
    refreshAllMetadataQueued: "Metadaten-Aktualisierung eingereiht.",
    refreshAllMetadataFollow: "Unter Betrieb verfolgen",
    jobKindLibraryMetadataRefresh: "Metadaten-Aktualisierung",
    jobKindDescLibraryMetadataRefresh:
      "Sucht jedes erkannte Element einer Mediathek erneut beim Anbieter und übernimmt dessen aktuelle Metadaten. Gesperrte Felder behalten ihren Wert.",
    jobProgressMessageRefreshingMetadata: "Metadaten werden aktualisiert",
  },
  es: {
    refreshAllMetadata: "Actualizar todos los metadatos",
    confirmRefreshAllMetadata:
      "¿Actualizar los metadatos de todos los elementos de esta biblioteca?",
    refreshAllMetadataBody:
      "Cada elemento identificado se vuelve a buscar en su proveedor. Los campos bloqueados conservan su valor. Puede tardar un rato; puedes seguirla o cancelarla en Operaciones.",
    refreshAllMetadataQueued: "Actualización de metadatos en cola.",
    refreshAllMetadataFollow: "Seguir en Operaciones",
    jobKindLibraryMetadataRefresh: "Actualización de metadatos",
    jobKindDescLibraryMetadataRefresh:
      "Vuelve a buscar cada elemento identificado de una biblioteca y aplica los metadatos actuales de su proveedor. Los campos bloqueados conservan su valor.",
    jobProgressMessageRefreshingMetadata: "Actualizando metadatos",
  },
  pt: {
    refreshAllMetadata: "Atualizar todos os metadados",
    confirmRefreshAllMetadata: "Atualizar os metadados de todos os itens desta biblioteca?",
    refreshAllMetadataBody:
      "Cada item identificado é procurado de novo no seu provedor. Os campos bloqueados mantêm seu valor. Isso pode levar algum tempo; você pode acompanhar ou cancelar em Operações.",
    refreshAllMetadataQueued: "Atualização de metadados na fila.",
    refreshAllMetadataFollow: "Acompanhar em Operações",
    jobKindLibraryMetadataRefresh: "Atualização de metadados",
    jobKindDescLibraryMetadataRefresh:
      "Procura de novo cada item identificado de uma biblioteca e aplica os metadados atuais do seu provedor. Os campos bloqueados mantêm seu valor.",
    jobProgressMessageRefreshingMetadata: "Atualizando metadados",
  },
} as const;

// Deleted libraries: the "Recently deleted" group in Console > Libraries, its
// restore and permanent-delete controls, and the Undo offered after a delete
// (CORE-1201). Counts use the label form ("Media items: 3") so every locale
// stays grammatical without plural rules. A string with a typed argument such
// as {count, number} is parsed as ICU, where a plain apostrophe starts a quoted
// literal: keep those strings free of one.
export const libraryTrashStrings = {
  en: {
    recentlyDeleted: "Recently deleted",
    recentlyDeletedLead:
      "Deleted libraries wait here for 30 days. Restore one to bring back its media, watch history and settings exactly as they were.",
    deletedLibraryWhen: "Deleted {date}",
    deletedLibraryItems: "Media items: {count, number}",
    deletedLibraryPurgeAfter: "Removed for good on {date}",
    deletedLibraryKept: "Kept until you restore it",
    restoreLibraryNamed: "Restore {library}",
    restoreLibraryBusy: "Restoring…",
    restoreLibraryFailed: "Couldn't restore this library. Try again.",
    libraryRestored: "Restored “{library}”. Beamfall is scanning its folders for changes.",
    undoDeleteLibrary: "Undo",
    purgeLibrary: "Delete permanently",
    purgeLibraryNamed: "Delete {library} permanently",
    purgeLibraryTitle: "Delete “{library}” permanently?",
    purgeLibraryRemoves:
      "Its catalog records, watch history and generated artwork are removed and cannot be recovered. Media items: {count, number}.",
    purgeLibraryRebuild:
      "To see this media in Beamfall again, add its folders to a new library. It starts with no watch history.",
    purgeLibraryFailed: "Couldn't delete this library permanently. Try again.",
    libraryPurged:
      "Permanently deleted “{library}”. Media items removed: {count, number}. Its media files are still on disk.",
  },
  fr: {
    recentlyDeleted: "Supprimées récemment",
    recentlyDeletedLead:
      "Les bibliothèques supprimées restent ici pendant 30 jours. Restaurez-en une pour retrouver ses médias, son historique de lecture et ses réglages tels qu’ils étaient.",
    deletedLibraryWhen: "Supprimée le {date}",
    deletedLibraryItems: "Éléments multimédias : {count, number}",
    deletedLibraryPurgeAfter: "Suppression définitive le {date}",
    deletedLibraryKept: "Conservée jusqu’à sa restauration",
    restoreLibraryNamed: "Restaurer {library}",
    restoreLibraryBusy: "Restauration…",
    restoreLibraryFailed: "Impossible de restaurer cette bibliothèque. Réessayez.",
    libraryRestored:
      "« {library} » a été restaurée. Beamfall analyse ses dossiers pour détecter les changements.",
    undoDeleteLibrary: "Annuler",
    purgeLibrary: "Supprimer définitivement",
    purgeLibraryNamed: "Supprimer définitivement {library}",
    purgeLibraryTitle: "Supprimer définitivement « {library} » ?",
    purgeLibraryRemoves:
      "Ses fiches du catalogue, son historique de lecture et ses illustrations générées sont supprimés et ne peuvent pas être récupérés. Éléments multimédias : {count, number}.",
    purgeLibraryRebuild:
      "Pour revoir ces médias dans Beamfall, ajoutez ses dossiers à une nouvelle bibliothèque. Elle démarre sans historique de lecture.",
    purgeLibraryFailed: "Impossible de supprimer définitivement cette bibliothèque. Réessayez.",
    libraryPurged:
      "« {library} » a été supprimée définitivement. Éléments multimédias supprimés : {count, number}. Ses fichiers multimédias sont toujours sur le disque.",
  },
  de: {
    recentlyDeleted: "Zuletzt gelöscht",
    recentlyDeletedLead:
      "Gelöschte Bibliotheken bleiben 30 Tage hier. Beim Wiederherstellen kehren Medien, Wiedergabeverlauf und Einstellungen genau so zurück, wie sie waren.",
    deletedLibraryWhen: "Gelöscht am {date}",
    deletedLibraryItems: "Medienelemente: {count, number}",
    deletedLibraryPurgeAfter: "Wird am {date} endgültig entfernt",
    deletedLibraryKept: "Bleibt erhalten, bis sie wiederhergestellt wird",
    restoreLibraryNamed: "{library} wiederherstellen",
    restoreLibraryBusy: "Wird wiederhergestellt…",
    restoreLibraryFailed:
      "Die Bibliothek konnte nicht wiederhergestellt werden. Bitte erneut versuchen.",
    libraryRestored:
      "„{library}“ wurde wiederhergestellt. Beamfall durchsucht die Ordner nach Änderungen.",
    undoDeleteLibrary: "Rückgängig",
    purgeLibrary: "Endgültig löschen",
    purgeLibraryNamed: "{library} endgültig löschen",
    purgeLibraryTitle: "„{library}“ endgültig löschen?",
    purgeLibraryRemoves:
      "Katalogeinträge, Wiedergabeverlauf und erzeugte Bilder werden entfernt und lassen sich nicht wiederherstellen. Medienelemente: {count, number}.",
    purgeLibraryRebuild:
      "Um diese Medien wieder in Beamfall zu sehen, die Ordner einer neuen Bibliothek hinzufügen. Sie beginnt ohne Wiedergabeverlauf.",
    purgeLibraryFailed:
      "Die Bibliothek konnte nicht endgültig gelöscht werden. Bitte erneut versuchen.",
    libraryPurged:
      "„{library}“ wurde endgültig gelöscht. Entfernte Medienelemente: {count, number}. Die Mediendateien liegen weiterhin auf dem Datenträger.",
  },
  es: {
    recentlyDeleted: "Eliminadas recientemente",
    recentlyDeletedLead:
      "Las bibliotecas eliminadas permanecen aquí durante 30 días. Restaura una para recuperar su contenido, su historial de reproducción y sus ajustes tal como estaban.",
    deletedLibraryWhen: "Eliminada el {date}",
    deletedLibraryItems: "Elementos multimedia: {count, number}",
    deletedLibraryPurgeAfter: "Se eliminará definitivamente el {date}",
    deletedLibraryKept: "Se conserva hasta que la restaures",
    restoreLibraryNamed: "Restaurar {library}",
    restoreLibraryBusy: "Restaurando…",
    restoreLibraryFailed: "No se pudo restaurar esta biblioteca. Inténtalo de nuevo.",
    libraryRestored:
      "Se restauró «{library}». Beamfall está analizando sus carpetas en busca de cambios.",
    undoDeleteLibrary: "Deshacer",
    purgeLibrary: "Eliminar definitivamente",
    purgeLibraryNamed: "Eliminar {library} definitivamente",
    purgeLibraryTitle: "¿Eliminar «{library}» definitivamente?",
    purgeLibraryRemoves:
      "Sus registros del catálogo, su historial de reproducción y sus imágenes generadas se eliminan y no se pueden recuperar. Elementos multimedia: {count, number}.",
    purgeLibraryRebuild:
      "Para volver a ver este contenido en Beamfall, añade sus carpetas a una biblioteca nueva. Empieza sin historial de reproducción.",
    purgeLibraryFailed: "No se pudo eliminar definitivamente esta biblioteca. Inténtalo de nuevo.",
    libraryPurged:
      "Se eliminó «{library}» definitivamente. Elementos multimedia eliminados: {count, number}. Sus archivos multimedia siguen en el disco.",
  },
  pt: {
    recentlyDeleted: "Excluídas recentemente",
    recentlyDeletedLead:
      "As bibliotecas excluídas ficam aqui por 30 dias. Restaure uma para recuperar a mídia, o histórico de reprodução e os ajustes exatamente como estavam.",
    deletedLibraryWhen: "Excluída em {date}",
    deletedLibraryItems: "Itens de mídia: {count, number}",
    deletedLibraryPurgeAfter: "Será removida definitivamente em {date}",
    deletedLibraryKept: "Mantida até você restaurá-la",
    restoreLibraryNamed: "Restaurar {library}",
    restoreLibraryBusy: "Restaurando…",
    restoreLibraryFailed: "Não foi possível restaurar esta biblioteca. Tente novamente.",
    libraryRestored:
      "“{library}” foi restaurada. O Beamfall está verificando as pastas em busca de alterações.",
    undoDeleteLibrary: "Desfazer",
    purgeLibrary: "Excluir permanentemente",
    purgeLibraryNamed: "Excluir {library} permanentemente",
    purgeLibraryTitle: "Excluir “{library}” permanentemente?",
    purgeLibraryRemoves:
      "Os registros do catálogo, o histórico de reprodução e as imagens geradas são removidos e não podem ser recuperados. Itens de mídia: {count, number}.",
    purgeLibraryRebuild:
      "Para ver esta mídia no Beamfall de novo, adicione as pastas a uma nova biblioteca. Ela começa sem histórico de reprodução.",
    purgeLibraryFailed:
      "Não foi possível excluir esta biblioteca permanentemente. Tente novamente.",
    libraryPurged:
      "“{library}” foi excluída permanentemente. Itens de mídia removidos: {count, number}. Os arquivos de mídia continuam no disco.",
  },
} as const;

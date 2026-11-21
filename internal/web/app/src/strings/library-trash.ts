// Deleted libraries: the "Recently deleted" group in Console > Libraries, its
// restore and permanent-delete controls, and the Undo offered after a delete
// (CORE-1201); and in Manage > Trash, Media the library scan folded into another
// item as an extra Version, with Undo merge per batch, and the read-only list of
// likely identical copies on disk (CORE-2916). Counts use the label form
// ("Media items: 3") so every locale stays grammatical without plural rules. A string with a typed argument such
// as {count, number} is parsed as ICU, where a plain apostrophe starts a quoted
// literal: keep those strings free of one.
export const libraryTrashStrings = {
  en: {
    recentlyDeleted: "Recently deleted",
    recentlyDeletedLead:
      "Deleted libraries wait here for 30 days, then are removed for good. A library that holds the only copy of its media stays until you restore it. Restoring brings back its media, watch history and settings exactly as they were.",
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
    trashMergedHeading: "Merged copies",
    trashMergedLead:
      "The library scan merged these items into another item as extra Versions. No files were deleted: each file now belongs to the item it was merged into. Purging these entries removes only the empty records, never the files.",
    trashMergedCount: "Merges: {count, number}",
    trashMergedItems: "Merged items",
    trashMergedInto: "Merged into",
    trashMergedAsVersion: "As a Version (automatic, scan)",
    trashMergedHiddenSurvivor: "Another item",
    trashMergedWhen: "Merged",
    trashUndoMerge: "Undo merge",
    trashUndoMergeNamed: "Undo merge into {title}",
    trashUndoMergeBusy: "Undoing…",
    trashUndoMergeFailed: "Couldn't undo this merge. Try again.",
    duplicateCopiesHeading: "Duplicate copies on disk",
    duplicateCopiesLead:
      "Items that hold more than one likely identical file: same size and matching sampled fingerprint. Check the files before you remove one. Beamfall never deletes files.",
    trashNoDeleted: "No deleted items.",
    duplicateCopiesEmpty: "No duplicate copies found.",
    duplicateCopiesUnavailable: "Couldn't load duplicate copies.",
    duplicateCopiesItem: "Item",
    duplicateCopiesFiles: "Files",
  },
  fr: {
    recentlyDeleted: "Supprimées récemment",
    recentlyDeletedLead:
      "Les bibliothèques supprimées restent ici pendant 30 jours, puis sont retirées définitivement. Une bibliothèque qui détient la seule copie de ses médias reste jusqu’à sa restauration. La restauration rétablit ses médias, son historique de lecture et ses réglages tels qu’ils étaient.",
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
    trashMergedHeading: "Copies fusionnées",
    trashMergedLead:
      "L’analyse de la bibliothèque a fusionné ces éléments dans un autre élément comme Versions supplémentaires. Aucun fichier n’a été supprimé : chaque fichier appartient désormais à l’élément dans lequel il a été fusionné. Purger ces entrées ne supprime que les fiches vides, jamais les fichiers.",
    trashMergedCount: "Fusions : {count, number}",
    trashMergedItems: "Éléments fusionnés",
    trashMergedInto: "Fusionné dans",
    trashMergedAsVersion: "Comme Version (automatique, analyse)",
    trashMergedHiddenSurvivor: "Un autre élément",
    trashMergedWhen: "Fusionné",
    trashUndoMerge: "Annuler la fusion",
    trashUndoMergeNamed: "Annuler la fusion dans {title}",
    trashUndoMergeBusy: "Annulation…",
    trashUndoMergeFailed: "Impossible d’annuler cette fusion. Réessayez.",
    duplicateCopiesHeading: "Copies en double sur le disque",
    duplicateCopiesLead:
      "Éléments qui contiennent plusieurs fichiers probablement identiques : même taille et empreinte échantillonnée identique. Vérifiez les fichiers avant d’en supprimer un. Beamfall ne supprime jamais de fichiers.",
    trashNoDeleted: "Aucun élément supprimé.",
    duplicateCopiesEmpty: "Aucune copie en double.",
    duplicateCopiesUnavailable: "Impossible de charger les copies en double.",
    duplicateCopiesItem: "Élément",
    duplicateCopiesFiles: "Fichiers",
  },
  de: {
    recentlyDeleted: "Zuletzt gelöscht",
    recentlyDeletedLead:
      "Gelöschte Bibliotheken bleiben 30 Tage hier und werden dann endgültig entfernt. Eine Bibliothek, die die einzige Kopie ihrer Medien enthält, bleibt, bis du sie wiederherstellst. Beim Wiederherstellen kehren Medien, Wiedergabeverlauf und Einstellungen genau so zurück, wie sie waren.",
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
    trashMergedHeading: "Zusammengeführte Kopien",
    trashMergedLead:
      "Der Bibliotheksscan hat diese Elemente als zusätzliche Versionen in ein anderes Element zusammengeführt. Es wurden keine Dateien gelöscht: Jede Datei gehört jetzt zu dem Element, in das sie zusammengeführt wurde. Das Bereinigen dieser Einträge entfernt nur die leeren Datensätze, nie die Dateien.",
    trashMergedCount: "Zusammenführungen: {count, number}",
    trashMergedItems: "Zusammengeführte Elemente",
    trashMergedInto: "Zusammengeführt in",
    trashMergedAsVersion: "Als Version (automatisch, Scan)",
    trashMergedHiddenSurvivor: "Ein anderes Element",
    trashMergedWhen: "Zusammengeführt",
    trashUndoMerge: "Zusammenführung rückgängig machen",
    trashUndoMergeNamed: "Zusammenführung in {title} rückgängig machen",
    trashUndoMergeBusy: "Wird rückgängig gemacht…",
    trashUndoMergeFailed:
      "Diese Zusammenführung konnte nicht rückgängig gemacht werden. Versuche es erneut.",
    duplicateCopiesHeading: "Doppelte Kopien auf der Festplatte",
    duplicateCopiesLead:
      "Elemente mit mehr als einer wahrscheinlich gleichen Datei: gleiche Größe und übereinstimmender Stichproben-Fingerabdruck. Prüfe die Dateien, bevor du eine entfernst. Beamfall löscht nie Dateien.",
    trashNoDeleted: "Keine gelöschten Elemente.",
    duplicateCopiesEmpty: "Keine doppelten Kopien gefunden.",
    duplicateCopiesUnavailable: "Doppelte Kopien konnten nicht geladen werden.",
    duplicateCopiesItem: "Element",
    duplicateCopiesFiles: "Dateien",
  },
  es: {
    recentlyDeleted: "Eliminadas recientemente",
    recentlyDeletedLead:
      "Las bibliotecas eliminadas permanecen aquí durante 30 días y después se quitan definitivamente. Una biblioteca que guarda la única copia de su contenido se queda hasta que la restaures. Al restaurarla vuelven su contenido, su historial de reproducción y sus ajustes tal como estaban.",
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
    trashMergedHeading: "Copias fusionadas",
    trashMergedLead:
      "El análisis de la biblioteca fusionó estos elementos en otro elemento como Versiones adicionales. No se eliminó ningún archivo: cada archivo pertenece ahora al elemento en el que se fusionó. Purgar estas entradas solo elimina los registros vacíos, nunca los archivos.",
    trashMergedCount: "Fusiones: {count, number}",
    trashMergedItems: "Elementos fusionados",
    trashMergedInto: "Fusionado en",
    trashMergedAsVersion: "Como Versión (automático, análisis)",
    trashMergedHiddenSurvivor: "Otro elemento",
    trashMergedWhen: "Fusionado",
    trashUndoMerge: "Deshacer fusión",
    trashUndoMergeNamed: "Deshacer fusión en {title}",
    trashUndoMergeBusy: "Deshaciendo…",
    trashUndoMergeFailed: "No se pudo deshacer esta fusión. Inténtalo de nuevo.",
    duplicateCopiesHeading: "Copias duplicadas en el disco",
    duplicateCopiesLead:
      "Elementos que tienen más de un archivo probablemente idéntico: mismo tamaño y huella de muestra coincidente. Comprueba los archivos antes de eliminar uno. Beamfall nunca elimina archivos.",
    trashNoDeleted: "No hay elementos eliminados.",
    duplicateCopiesEmpty: "No se encontraron copias duplicadas.",
    duplicateCopiesUnavailable: "No se pudieron cargar las copias duplicadas.",
    duplicateCopiesItem: "Elemento",
    duplicateCopiesFiles: "Archivos",
  },
  pt: {
    recentlyDeleted: "Excluídas recentemente",
    recentlyDeletedLead:
      "As bibliotecas excluídas ficam aqui por 30 dias e depois são removidas de vez. Uma biblioteca que guarda a única cópia da sua mídia fica até você restaurá-la. Restaurar traz de volta a mídia, o histórico de reprodução e os ajustes exatamente como estavam.",
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
    trashMergedHeading: "Cópias mescladas",
    trashMergedLead:
      "A verificação da biblioteca mesclou estes itens em outro item como Versões adicionais. Nenhum arquivo foi excluído: cada arquivo agora pertence ao item em que foi mesclado. Limpar estas entradas remove apenas os registros vazios, nunca os arquivos.",
    trashMergedCount: "Mesclagens: {count, number}",
    trashMergedItems: "Itens mesclados",
    trashMergedInto: "Mesclado em",
    trashMergedAsVersion: "Como Versão (automático, verificação)",
    trashMergedHiddenSurvivor: "Outro item",
    trashMergedWhen: "Mesclado",
    trashUndoMerge: "Desfazer mesclagem",
    trashUndoMergeNamed: "Desfazer mesclagem em {title}",
    trashUndoMergeBusy: "Desfazendo…",
    trashUndoMergeFailed: "Não foi possível desfazer esta mesclagem. Tente novamente.",
    duplicateCopiesHeading: "Cópias duplicadas no disco",
    duplicateCopiesLead:
      "Itens que têm mais de um arquivo provavelmente idêntico: mesmo tamanho e impressão digital amostrada igual. Verifique os arquivos antes de remover um. O Beamfall nunca exclui arquivos.",
    trashNoDeleted: "Nenhum item excluído.",
    duplicateCopiesEmpty: "Nenhuma cópia duplicada encontrada.",
    duplicateCopiesUnavailable: "Não foi possível carregar as cópias duplicadas.",
    duplicateCopiesItem: "Item",
    duplicateCopiesFiles: "Arquivos",
  },
} as const;

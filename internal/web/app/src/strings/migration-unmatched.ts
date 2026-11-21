export const migrationUnmatchedStrings = {
  en: {
    unmatchedHeading: "Unmatched records",
    unmatchedLead: "Files Beamfall could not match to a library item. Fix the match or rescan.",
    unmatchedEmpty: "No unmatched records.",
    unmatchedUnavailable: "Could not load unmatched records.",
    unmatchedRescan: "Rescan all unmatched",
    unmatchedFixMatch: "Fix match",
    unmatchedSearchLabel: "Search title",
    unmatchedSearch: "Search",
    unmatchedApply: "Apply",
    unmatchedApplyPin: "Apply + pin",
    unmatchedNoCandidates: "No candidates yet.",
    unmatchedBestGuess: "Best guess: {title}",
    unmatchedQueue: "Queue",
    unmatchedSearchByTitleYear: "Search by title and year, then pick the candidate to pin.",
    unmatchedCandidateWhy: "Returned by {source} for this search.",
    unmatchedSelectedMatch: "Selected match",
    unmatchedCreateMedia: "Create Media",
    unmatchedCreateUnavailable: "Create is reserved for no-Media import records.",
    unmatchedClearQueue: "Clear queue",
    // CORE-2910 — grouped, plain-language Unmatched fix flows.
    unmatchedGroupItems: "{count, plural, one {# item} other {# items}}",
    unmatchedWhyAmbiguous:
      "Several catalog entries fit this name equally well, so Beamfall did not guess.",
    unmatchedWhyBelow:
      "The closest catalog entry, “{title}”, matched only {confidence}; {threshold} is needed.",
    unmatchedWhyBelowUntitled:
      "The closest catalog entry matched only {confidence}; {threshold} is needed.",
    unmatchedWhyNone: "The catalog returned nothing for this name.",
    unmatchedWhyManual: "This source does not score matches, so pick the entry yourself.",
    unmatchedWhyUnknown: "Beamfall could not match this automatically.",
    unmatchedHintYear: "The folder says {year}, so entries from {year} are suggested first.",
    unmatchedHintAddYear:
      "Adding the year to the folder name, like “{example}”, lets the next scan tell these entries apart.",
    unmatchedHintQualifier:
      "The folder name includes “({qualifier})”, which the catalog title may not. Search without it, or rename the folder.",
    unmatchedYearLabel: "Year",
    unmatchedSuggested: "Suggested",
    unmatchedPickHint: "Choose the right entry, then match.",
    unmatchedMatchAll: "Match all {count}",
    unmatchedMatching: "Matching {done} of {total}…",
    unmatchedMatchedSome: "Matched {done} of {total}. Could not match: {titles}.",
    unmatchedKnowID: "Know the catalog ID?",
    unmatchedNamingHeading: "File and folder",
    unmatchedNamingLead:
      "Match these above. To help future scans, rename the folder in Libraries or the file in Rename.",
    unmatchedNoPath: "No file path recorded.",
    unmatchedOpenRename: "Open Rename",
    unmatchedOpenLibraries: "Open Libraries",
    unmatchedManualNoCatalog:
      "This source has no catalog to search, so these items cannot be matched here. Rename them so a catalog source finds them, or hide them.",
    unmatchedTruncated: "Showing the first {count} items. Fix or hide some to see the rest.",
    unmatchedHide: "Hide",
    unmatchedHideAll: "Hide all {count}",
    unmatchedHideTitle: "Hide these items?",
    unmatchedHideBody:
      "They leave this list but nothing is deleted. The next scan may queue them again.",
  },
  fr: {
    unmatchedHeading: "Enregistrements non appariés",
    unmatchedLead:
      "Fichiers que Beamfall n’a pas pu associer à un élément de bibliothèque. Corrigez l’association ou relancez l’analyse.",
    unmatchedEmpty: "Aucun enregistrement non apparié.",
    unmatchedUnavailable: "Impossible de charger les enregistrements non appariés.",
    unmatchedRescan: "Réanalyser tous les non appariés",
    unmatchedFixMatch: "Corriger l’appariement",
    unmatchedSearchLabel: "Rechercher un titre",
    unmatchedSearch: "Rechercher",
    unmatchedApply: "Appliquer",
    unmatchedApplyPin: "Appliquer et épingler",
    unmatchedNoCandidates: "Aucun candidat pour l’instant.",
    unmatchedBestGuess: "Meilleure hypothèse : {title}",
    unmatchedQueue: "File d’attente",
    unmatchedSearchByTitleYear:
      "Recherchez par titre et année, puis choisissez le candidat à épingler.",
    unmatchedCandidateWhy: "Renvoyé par {source} pour cette recherche.",
    unmatchedSelectedMatch: "Correspondance sélectionnée",
    unmatchedCreateMedia: "Créer un média",
    unmatchedCreateUnavailable: "La création est réservée aux enregistrements d’import sans média.",
    unmatchedClearQueue: "Vider la file d’attente",
    unmatchedGroupItems: "{count, plural, one {# élément} other {# éléments}}",
    unmatchedWhyAmbiguous:
      "Plusieurs entrées du catalogue correspondent aussi bien à ce nom, Beamfall n’a donc pas choisi.",
    unmatchedWhyBelow:
      "L’entrée du catalogue la plus proche, « {title} », n’atteint que {confidence} ; il faut {threshold}.",
    unmatchedWhyBelowUntitled:
      "L’entrée du catalogue la plus proche n’atteint que {confidence} ; il faut {threshold}.",
    unmatchedWhyNone: "Le catalogue n’a rien renvoyé pour ce nom.",
    unmatchedWhyManual:
      "Cette source ne note pas les correspondances : choisissez l’entrée vous-même.",
    unmatchedWhyUnknown: "Beamfall n’a pas pu faire la correspondance automatiquement.",
    unmatchedHintYear:
      "Le dossier indique {year} : les entrées de {year} sont proposées en premier.",
    unmatchedHintAddYear:
      "Ajouter l’année au nom du dossier, par exemple « {example} », permet à la prochaine analyse de distinguer ces entrées.",
    unmatchedHintQualifier:
      "Le nom du dossier contient « ({qualifier}) », ce que le titre du catalogue n’a peut-être pas. Recherchez sans, ou renommez le dossier.",
    unmatchedYearLabel: "Année",
    unmatchedSuggested: "Suggéré",
    unmatchedPickHint: "Choisissez la bonne entrée, puis associez.",
    unmatchedMatchAll: "Tout associer ({count})",
    unmatchedMatching: "Association {done} sur {total}…",
    unmatchedMatchedSome: "{done} sur {total} associés. Échec : {titles}.",
    unmatchedKnowID: "Vous connaissez l’ID du catalogue ?",
    unmatchedNamingHeading: "Fichier et dossier",
    unmatchedNamingLead:
      "Associez-les ci-dessus. Pour aider les prochaines analyses, renommez le dossier dans Bibliothèques ou le fichier dans Renommer.",
    unmatchedNoPath: "Aucun chemin de fichier enregistré.",
    unmatchedOpenRename: "Ouvrir Renommer",
    unmatchedOpenLibraries: "Ouvrir Bibliothèques",
    unmatchedManualNoCatalog:
      "Cette source n'a pas de catalogue à rechercher : ces éléments ne peuvent pas être associés ici. Renommez-les pour qu'une source avec catalogue les trouve, ou masquez-les.",
    unmatchedTruncated:
      "Affichage des {count} premiers éléments. Corrigez-en ou masquez-en pour voir la suite.",
    unmatchedHide: "Masquer",
    unmatchedHideAll: "Tout masquer ({count})",
    unmatchedHideTitle: "Masquer ces éléments ?",
    unmatchedHideBody:
      "Ils quittent cette liste, mais rien n’est supprimé. La prochaine analyse peut les remettre en file.",
  },
  de: {
    unmatchedHeading: "Nicht zugeordnete Datensätze",
    unmatchedLead:
      "Dateien, die Beamfall keinem Bibliothekselement zuordnen konnte. Korrigiere die Zuordnung oder scanne erneut.",
    unmatchedEmpty: "Keine nicht zugeordneten Datensätze.",
    unmatchedUnavailable: "Nicht zugeordnete Datensätze konnten nicht geladen werden.",
    unmatchedRescan: "Alle nicht zugeordneten neu scannen",
    unmatchedFixMatch: "Zuordnung korrigieren",
    unmatchedSearchLabel: "Titel suchen",
    unmatchedSearch: "Suchen",
    unmatchedApply: "Anwenden",
    unmatchedApplyPin: "Anwenden und fixieren",
    unmatchedNoCandidates: "Noch keine Kandidaten.",
    unmatchedBestGuess: "Beste Vermutung: {title}",
    unmatchedQueue: "Warteschlange",
    unmatchedSearchByTitleYear:
      "Suche nach Titel und Jahr und wähle dann den Kandidaten zum Fixieren.",
    unmatchedCandidateWhy: "Von {source} für diese Suche geliefert.",
    unmatchedSelectedMatch: "Ausgewählte Zuordnung",
    unmatchedCreateMedia: "Medium erstellen",
    unmatchedCreateUnavailable: "Das Erstellen ist Import-Datensätzen ohne Medium vorbehalten.",
    unmatchedClearQueue: "Warteschlange leeren",
    unmatchedGroupItems: "{count, plural, one {# Element} other {# Elemente}}",
    unmatchedWhyAmbiguous:
      "Mehrere Katalogeinträge passen gleich gut zu diesem Namen, daher hat Beamfall nicht geraten.",
    unmatchedWhyBelow:
      "Der nächste Katalogeintrag, „{title}“, erreicht nur {confidence}; nötig sind {threshold}.",
    unmatchedWhyBelowUntitled:
      "Der nächste Katalogeintrag erreicht nur {confidence}; nötig sind {threshold}.",
    unmatchedWhyNone: "Der Katalog hat für diesen Namen nichts geliefert.",
    unmatchedWhyManual: "Diese Quelle bewertet keine Treffer, wähle den Eintrag also selbst.",
    unmatchedWhyUnknown: "Beamfall konnte das nicht automatisch zuordnen.",
    unmatchedHintYear:
      "Der Ordner nennt {year}, daher werden Einträge aus {year} zuerst vorgeschlagen.",
    unmatchedHintAddYear:
      "Mit dem Jahr im Ordnernamen, etwa „{example}“, kann der nächste Scan diese Einträge unterscheiden.",
    unmatchedHintQualifier:
      "Der Ordnername enthält „({qualifier})“, der Katalogtitel womöglich nicht. Suche ohne diesen Zusatz oder benenne den Ordner um.",
    unmatchedYearLabel: "Jahr",
    unmatchedSuggested: "Vorgeschlagen",
    unmatchedPickHint: "Wähle den richtigen Eintrag und ordne dann zu.",
    unmatchedMatchAll: "Alle {count} zuordnen",
    unmatchedMatching: "Ordne {done} von {total} zu …",
    unmatchedMatchedSome: "{done} von {total} zugeordnet. Fehlgeschlagen: {titles}.",
    unmatchedKnowID: "Katalog-ID bekannt?",
    unmatchedNamingHeading: "Datei und Ordner",
    unmatchedNamingLead:
      "Ordne sie oben zu. Damit spätere Scans sie erkennen, benenne den Ordner unter Bibliotheken oder die Datei unter Umbenennen um.",
    unmatchedNoPath: "Kein Dateipfad gespeichert.",
    unmatchedOpenRename: "Umbenennen öffnen",
    unmatchedOpenLibraries: "Bibliotheken öffnen",
    unmatchedManualNoCatalog:
      "Diese Quelle hat keinen durchsuchbaren Katalog, daher lassen sich diese Elemente hier nicht zuordnen. Benenne sie um, damit eine Quelle mit Katalog sie findet, oder blende sie aus.",
    unmatchedTruncated:
      "Die ersten {count} Elemente werden angezeigt. Korrigiere oder blende einige aus, um den Rest zu sehen.",
    unmatchedHide: "Ausblenden",
    unmatchedHideAll: "Alle {count} ausblenden",
    unmatchedHideTitle: "Diese Elemente ausblenden?",
    unmatchedHideBody:
      "Sie verschwinden aus dieser Liste, gelöscht wird nichts. Der nächste Scan kann sie wieder einreihen.",
  },
  es: {
    unmatchedHeading: "Registros no coincidentes",
    unmatchedLead:
      "Archivos que Beamfall no pudo asociar a un elemento de la biblioteca. Corrige la coincidencia o vuelve a analizar.",
    unmatchedEmpty: "No hay registros no coincidentes.",
    unmatchedUnavailable: "No se pudieron cargar los registros no coincidentes.",
    unmatchedRescan: "Reescanear todos los no coincidentes",
    unmatchedFixMatch: "Corregir coincidencia",
    unmatchedSearchLabel: "Buscar título",
    unmatchedSearch: "Buscar",
    unmatchedApply: "Aplicar",
    unmatchedApplyPin: "Aplicar y fijar",
    unmatchedNoCandidates: "Aún no hay candidatos.",
    unmatchedBestGuess: "Mejor suposición: {title}",
    unmatchedQueue: "Cola",
    unmatchedSearchByTitleYear: "Busca por título y año, y elige el candidato que quieres fijar.",
    unmatchedCandidateWhy: "Devuelto por {source} para esta búsqueda.",
    unmatchedSelectedMatch: "Coincidencia seleccionada",
    unmatchedCreateMedia: "Crear medio",
    unmatchedCreateUnavailable: "Crear está reservado para registros de importación sin medio.",
    unmatchedClearQueue: "Vaciar la cola",
    unmatchedGroupItems: "{count, plural, one {# elemento} other {# elementos}}",
    unmatchedWhyAmbiguous:
      "Varias entradas del catálogo encajan igual de bien con este nombre, así que Beamfall no eligió.",
    unmatchedWhyBelow:
      "La entrada del catálogo más cercana, «{title}», solo alcanzó {confidence}; se necesita {threshold}.",
    unmatchedWhyBelowUntitled:
      "La entrada del catálogo más cercana solo alcanzó {confidence}; se necesita {threshold}.",
    unmatchedWhyNone: "El catálogo no devolvió nada para este nombre.",
    unmatchedWhyManual: "Esta fuente no puntúa coincidencias, así que elige la entrada tú.",
    unmatchedWhyUnknown: "Beamfall no pudo emparejarlo automáticamente.",
    unmatchedHintYear:
      "La carpeta indica {year}, así que se sugieren primero las entradas de {year}.",
    unmatchedHintAddYear:
      "Añadir el año al nombre de la carpeta, como «{example}», permite que el próximo análisis distinga estas entradas.",
    unmatchedHintQualifier:
      "El nombre de la carpeta incluye «({qualifier})», que quizá el título del catálogo no tenga. Busca sin él o cambia el nombre de la carpeta.",
    unmatchedYearLabel: "Año",
    unmatchedSuggested: "Sugerida",
    unmatchedPickHint: "Elige la entrada correcta y luego empareja.",
    unmatchedMatchAll: "Emparejar los {count}",
    unmatchedMatching: "Emparejando {done} de {total}…",
    unmatchedMatchedSome: "Emparejados {done} de {total}. No se pudo: {titles}.",
    unmatchedKnowID: "¿Conoces el ID del catálogo?",
    unmatchedNamingHeading: "Archivo y carpeta",
    unmatchedNamingLead:
      "Asócialos arriba. Para ayudar a los próximos análisis, renombra la carpeta en Bibliotecas o el archivo en Renombrar.",
    unmatchedNoPath: "No hay ruta de archivo registrada.",
    unmatchedOpenRename: "Abrir Renombrar",
    unmatchedOpenLibraries: "Abrir Bibliotecas",
    unmatchedManualNoCatalog:
      "Esta fuente no tiene un catálogo para buscar, así que estos elementos no se pueden asociar aquí. Renómbralos para que una fuente con catálogo los encuentre, u ocúltalos.",
    unmatchedTruncated:
      "Se muestran los primeros {count} elementos. Corrige u oculta algunos para ver el resto.",
    unmatchedHide: "Ocultar",
    unmatchedHideAll: "Ocultar los {count}",
    unmatchedHideTitle: "¿Ocultar estos elementos?",
    unmatchedHideBody:
      "Salen de esta lista, pero no se borra nada. El próximo análisis puede volver a ponerlos en cola.",
  },
  pt: {
    unmatchedHeading: "Registros não correspondidos",
    unmatchedLead:
      "Arquivos que o Beamfall não conseguiu associar a um item da biblioteca. Corrija a correspondência ou verifique novamente.",
    unmatchedEmpty: "Nenhum registro não correspondido.",
    unmatchedUnavailable: "Não foi possível carregar os registros não correspondidos.",
    unmatchedRescan: "Reanalisar todos os não correspondidos",
    unmatchedFixMatch: "Corrigir correspondência",
    unmatchedSearchLabel: "Pesquisar título",
    unmatchedSearch: "Pesquisar",
    unmatchedApply: "Aplicar",
    unmatchedApplyPin: "Aplicar e fixar",
    unmatchedNoCandidates: "Nenhum candidato ainda.",
    unmatchedBestGuess: "Melhor palpite: {title}",
    unmatchedQueue: "Fila",
    unmatchedSearchByTitleYear: "Pesquise por título e ano e escolha o candidato a fixar.",
    unmatchedCandidateWhy: "Retornado por {source} para esta pesquisa.",
    unmatchedSelectedMatch: "Correspondência selecionada",
    unmatchedCreateMedia: "Criar mídia",
    unmatchedCreateUnavailable: "Criar é reservado a registros de importação sem mídia.",
    unmatchedClearQueue: "Limpar a fila",
    unmatchedGroupItems: "{count, plural, one {# item} other {# itens}}",
    unmatchedWhyAmbiguous:
      "Várias entradas do catálogo combinam igualmente com este nome, então o Beamfall não escolheu.",
    unmatchedWhyBelow:
      "A entrada do catálogo mais próxima, “{title}”, chegou só a {confidence}; é preciso {threshold}.",
    unmatchedWhyBelowUntitled:
      "A entrada do catálogo mais próxima chegou só a {confidence}; é preciso {threshold}.",
    unmatchedWhyNone: "O catálogo não retornou nada para este nome.",
    unmatchedWhyManual:
      "Esta fonte não pontua correspondências, então escolha a entrada você mesmo.",
    unmatchedWhyUnknown: "O Beamfall não conseguiu associar isto automaticamente.",
    unmatchedHintYear: "A pasta indica {year}, então as entradas de {year} são sugeridas primeiro.",
    unmatchedHintAddYear:
      "Adicionar o ano ao nome da pasta, como “{example}”, permite que a próxima varredura diferencie estas entradas.",
    unmatchedHintQualifier:
      "O nome da pasta inclui “({qualifier})”, que o título do catálogo talvez não tenha. Pesquise sem isso ou renomeie a pasta.",
    unmatchedYearLabel: "Ano",
    unmatchedSuggested: "Sugerida",
    unmatchedPickHint: "Escolha a entrada certa e depois associe.",
    unmatchedMatchAll: "Associar todos os {count}",
    unmatchedMatching: "Associando {done} de {total}…",
    unmatchedMatchedSome: "{done} de {total} associados. Falharam: {titles}.",
    unmatchedKnowID: "Sabe o ID do catálogo?",
    unmatchedNamingHeading: "Arquivo e pasta",
    unmatchedNamingLead:
      "Associe-os acima. Para ajudar as próximas verificações, renomeie a pasta em Bibliotecas ou o arquivo em Renomear.",
    unmatchedNoPath: "Nenhum caminho de arquivo registrado.",
    unmatchedOpenRename: "Abrir Renomear",
    unmatchedOpenLibraries: "Abrir Bibliotecas",
    unmatchedManualNoCatalog:
      "Esta fonte não tem catálogo para pesquisar, então estes itens não podem ser associados aqui. Renomeie-os para que uma fonte com catálogo os encontre, ou oculte-os.",
    unmatchedTruncated:
      "Mostrando os primeiros {count} itens. Corrija ou oculte alguns para ver o restante.",
    unmatchedHide: "Ocultar",
    unmatchedHideAll: "Ocultar todos os {count}",
    unmatchedHideTitle: "Ocultar estes itens?",
    unmatchedHideBody:
      "Eles saem desta lista, mas nada é excluído. A próxima varredura pode colocá-los na fila de novo.",
  },
} as const;

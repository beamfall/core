export const migrationUnmatchedGroupStrings = {
  en: {
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
      "If the folder or file name is wrong, fix it in Rename, then rescan these items.",
    unmatchedNoPath: "No file path recorded.",
    unmatchedOpenRename: "Open Rename",
    unmatchedRescanGroup: "Rescan these",
    unmatchedRescanNeedsLibrary: "Choose a library above to rescan these items.",
    unmatchedHide: "Hide",
    unmatchedHideAll: "Hide all {count}",
    unmatchedHideTitle: "Hide these items?",
    unmatchedHideBody:
      "They leave this list but nothing is deleted. The next scan may queue them again.",
  },
  fr: {
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
      "Si le nom du dossier ou du fichier est faux, corrigez-le dans Renommer, puis réanalysez ces éléments.",
    unmatchedNoPath: "Aucun chemin de fichier enregistré.",
    unmatchedOpenRename: "Ouvrir Renommer",
    unmatchedRescanGroup: "Réanalyser ces éléments",
    unmatchedRescanNeedsLibrary:
      "Choisissez une bibliothèque ci-dessus pour réanalyser ces éléments.",
    unmatchedHide: "Masquer",
    unmatchedHideAll: "Tout masquer ({count})",
    unmatchedHideTitle: "Masquer ces éléments ?",
    unmatchedHideBody:
      "Ils quittent cette liste, mais rien n’est supprimé. La prochaine analyse peut les remettre en file.",
  },
  de: {
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
      "Ist der Ordner- oder Dateiname falsch, korrigiere ihn unter Umbenennen und scanne diese Elemente dann neu.",
    unmatchedNoPath: "Kein Dateipfad gespeichert.",
    unmatchedOpenRename: "Umbenennen öffnen",
    unmatchedRescanGroup: "Diese neu scannen",
    unmatchedRescanNeedsLibrary: "Wähle oben eine Bibliothek, um diese Elemente neu zu scannen.",
    unmatchedHide: "Ausblenden",
    unmatchedHideAll: "Alle {count} ausblenden",
    unmatchedHideTitle: "Diese Elemente ausblenden?",
    unmatchedHideBody:
      "Sie verschwinden aus dieser Liste, gelöscht wird nichts. Der nächste Scan kann sie wieder einreihen.",
  },
  es: {
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
      "Si el nombre de la carpeta o del archivo es incorrecto, corrígelo en Renombrar y vuelve a analizar estos elementos.",
    unmatchedNoPath: "No hay ruta de archivo registrada.",
    unmatchedOpenRename: "Abrir Renombrar",
    unmatchedRescanGroup: "Volver a analizar estos",
    unmatchedRescanNeedsLibrary:
      "Elige una biblioteca arriba para volver a analizar estos elementos.",
    unmatchedHide: "Ocultar",
    unmatchedHideAll: "Ocultar los {count}",
    unmatchedHideTitle: "¿Ocultar estos elementos?",
    unmatchedHideBody:
      "Salen de esta lista, pero no se borra nada. El próximo análisis puede volver a ponerlos en cola.",
  },
  pt: {
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
      "Se o nome da pasta ou do arquivo estiver errado, corrija em Renomear e depois verifique estes itens de novo.",
    unmatchedNoPath: "Nenhum caminho de arquivo registrado.",
    unmatchedOpenRename: "Abrir Renomear",
    unmatchedRescanGroup: "Verificar estes de novo",
    unmatchedRescanNeedsLibrary: "Escolha uma biblioteca acima para verificar estes itens de novo.",
    unmatchedHide: "Ocultar",
    unmatchedHideAll: "Ocultar todos os {count}",
    unmatchedHideTitle: "Ocultar estes itens?",
    unmatchedHideBody:
      "Eles saem desta lista, mas nada é excluído. A próxima varredura pode colocá-los na fila de novo.",
  },
} as const;

export const serverLogStrings = {
  en: {
    // Console server logs (CORE-1716).
    serverLogsHeading: "Server logs",
    serverLogsLead:
      "The most recent lines the server wrote to its log. Tokens, secrets, IP addresses and the user names in home folder paths are removed before the lines leave the server.",
    serverLogsDownload: "Download log bundle",
    serverLogsEmpty: "The server has not written any log lines yet.",
    serverLogsUnavailable: "Server logs could not be loaded.",
    serverLogsNotKept:
      "This server does not keep recent log lines. Read its log on the host instead.",
    serverLogsShowing: "Showing the last {count} of up to {capacity} lines.",
    serverLogsLinesLabel: "Recent server log lines",
  },
  fr: {
    serverLogsHeading: "Journaux du serveur",
    serverLogsLead:
      "Les lignes les plus récentes écrites par le serveur dans son journal. Les jetons, secrets, adresses IP et noms d’utilisateur des chemins de dossiers personnels sont supprimés avant que les lignes ne quittent le serveur.",
    serverLogsDownload: "Télécharger l’archive des journaux",
    serverLogsEmpty: "Le serveur n’a encore écrit aucune ligne de journal.",
    serverLogsUnavailable: "Impossible de charger les journaux du serveur.",
    serverLogsNotKept:
      "Ce serveur ne conserve pas les lignes de journal récentes. Consultez son journal directement sur l’hôte.",
    serverLogsShowing: "Affichage des {count} dernières lignes sur {capacity} au maximum.",
    serverLogsLinesLabel: "Lignes récentes du journal du serveur",
  },
  de: {
    serverLogsHeading: "Serverlogs",
    serverLogsLead:
      "Die neuesten Zeilen, die der Server in sein Log geschrieben hat. Tokens, Geheimnisse, IP-Adressen und die Benutzernamen in Pfaden von Home-Ordnern werden entfernt, bevor die Zeilen den Server verlassen.",
    serverLogsDownload: "Log-Paket herunterladen",
    serverLogsEmpty: "Der Server hat noch keine Logzeilen geschrieben.",
    serverLogsUnavailable: "Serverlogs konnten nicht geladen werden.",
    serverLogsNotKept:
      "Dieser Server speichert keine aktuellen Logzeilen. Lies sein Log stattdessen auf dem Host.",
    serverLogsShowing: "Die letzten {count} von bis zu {capacity} Zeilen werden angezeigt.",
    serverLogsLinesLabel: "Aktuelle Zeilen des Serverlogs",
  },
  es: {
    serverLogsHeading: "Registros del servidor",
    serverLogsLead:
      "Las líneas más recientes que el servidor escribió en su registro. Los tokens, secretos, direcciones IP y los nombres de usuario en las rutas de carpetas personales se eliminan antes de que las líneas salgan del servidor.",
    serverLogsDownload: "Descargar paquete de registros",
    serverLogsEmpty: "El servidor aún no ha escrito ninguna línea de registro.",
    serverLogsUnavailable: "No se pudieron cargar los registros del servidor.",
    serverLogsNotKept:
      "Este servidor no conserva las líneas de registro recientes. Consulta su registro en el host.",
    serverLogsShowing: "Mostrando las últimas {count} de hasta {capacity} líneas.",
    serverLogsLinesLabel: "Líneas recientes del registro del servidor",
  },
  pt: {
    serverLogsHeading: "Logs do servidor",
    serverLogsLead:
      "As linhas mais recentes que o servidor gravou em seu log. Tokens, segredos, endereços IP e os nomes de usuário em caminhos de pastas pessoais são removidos antes que as linhas saiam do servidor.",
    serverLogsDownload: "Baixar pacote de logs",
    serverLogsEmpty: "O servidor ainda não gravou nenhuma linha de log.",
    serverLogsUnavailable: "Não foi possível carregar os logs do servidor.",
    serverLogsNotKept:
      "Este servidor não guarda as linhas de log recentes. Leia o log dele no host.",
    serverLogsShowing: "Mostrando as últimas {count} de até {capacity} linhas.",
    serverLogsLinesLabel: "Linhas recentes do log do servidor",
  },
} as const;

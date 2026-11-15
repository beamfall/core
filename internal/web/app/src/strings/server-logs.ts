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
  fr: {},
  de: {},
  es: {},
  pt: {},
} as const;

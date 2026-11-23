package graph

// Agent API reads (CORE-2096, CORE-2097). agentCatalog lets an agent discover
// every top-level field and which credentials may run it, derived from the
// dispatcher registries so it can never drift from what execute() enforces.
// diagnosticsBundle gives an operator's agent the build, runtime statistics,
// filtered log lines and bounded text profiles in one read, without shell
// access to the host. Neither adds a route: both live on /api/graphql.

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/beamfall/core/internal/graphshape"
	"github.com/beamfall/core/internal/store"
)

// processStart approximates server start for diagnosticsBundle uptime: the
// graph package is initialised during process start-up.
var processStart = time.Now()

// agentCatalogGuide is the short orientation every catalog read carries.
const agentCatalogGuide = `Beamfall GraphQL for agents.
POST /api/graphql with {"query": "...", "variables": {...}} and "Authorization: Bearer <token>".
Mint a token offline with the server stopped: beamfall token create --name <n> --access cli|diagnostics|agent.
Tiers: cli runs the operator CLI fields; diagnostics also runs every admin read plus jobCancel and jobRetry; agent also runs admin mutations except minting credentials and transferring ownership.
A token has no Profile: a Library it creates with createLibrary is granted to the server owner's Profile.
Each field below lists its kind, whether it needs an Admin Session, the token tiers that may run it, its arguments and its top-level result fields. Each argument reads "name: kind", marked optional when the field runs without it; an empty list means the field takes none. Select result fields in braces, for example: query { jobs { id status } }.
A refused field answers "admin session required". Every response carries an X-Beamfall-Request-Id header; pass it to serverLogs(requestId: ...) or diagnosticsBundle to find the matching log lines.
Debug: serverLogs(limit, contains, level, requestId) and diagnosticsBundle(profiles: ["goroutine","heap","allocs","block","mutex","threadcreate"]).`

// AgentCatalogInfo is the agentCatalog read on the wire.
type AgentCatalogInfo struct {
	Guide  string              `json:"guide"`
	Fields []AgentCatalogField `json:"fields"`
}

// AgentCatalogField describes one top-level GraphQL field.
type AgentCatalogField struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	AdminSession bool     `json:"adminSession"`
	ManagerRole  bool     `json:"managerRole"`
	TokenTiers   []string `json:"tokenTiers"`
	// ArgumentsKnown is false when the server has no argument metadata for the
	// field: Arguments is then empty because it is unknown, not because the
	// field takes none.
	ArgumentsKnown bool     `json:"argumentsKnown"`
	Arguments      []string `json:"arguments"`
	ResultFields   []string `json:"resultFields"`
}

var tokenTiers = []store.APITokenAccess{store.APITokenAccessCLI, store.APITokenAccessDiagnostics, store.APITokenAccessAgent}

func agentCatalog() AgentCatalogInfo {
	fields := make([]AgentCatalogField, 0, len(fieldHandlerRegistry))
	for name, handler := range fieldHandlerRegistry {
		entry := AgentCatalogField{
			Name:           handler.wireName,
			Kind:           "query",
			AdminSession:   handler.roleGate&roleAdmin != 0,
			ManagerRole:    handler.roleGate&roleManager != 0,
			TokenTiers:     []string{},
			Arguments:      catalogArguments(name),
			ArgumentsKnown: catalogArgumentsKnown(name),
			ResultFields:   catalogResultFields(name),
		}
		if handler.kind == fieldMutation {
			entry.Kind = "mutation"
		}
		for _, tier := range tokenTiers {
			if !entry.AdminSession || tokenTierAllows(tier, name) {
				entry.TokenTiers = append(entry.TokenTiers, string(tier))
			}
		}
		fields = append(fields, entry)
	}
	slices.SortFunc(fields, func(a, b AgentCatalogField) int { return strings.Compare(a.Name, b.Name) })
	return AgentCatalogInfo{Guide: agentCatalogGuide, Fields: fields}
}

// catalogArgumentsKnown reports whether fieldArgumentRegistry describes field.
// The conformance test keeps the registry in step with fieldHandlerRegistry,
// so every dispatched field reports true.
func catalogArgumentsKnown(field string) bool {
	_, ok := fieldArgumentRegistry[field]
	return ok
}

func catalogArguments(field string) []string {
	args := fieldArgumentRegistry[field]
	out := make([]string, 0, len(args))
	for _, arg := range args {
		text := arg.Name + ": " + arg.Kind
		forms := slices.Clone(arg.Enum)
		for _, prefix := range arg.Prefixes {
			forms = append(forms, prefix+"<name>")
		}
		if len(forms) > 0 {
			text += " (" + strings.Join(forms, "|") + ")"
		}
		if arg.Optional {
			text += " optional"
		}
		out = append(out, text)
	}
	return out
}

func catalogResultFields(field string) []string {
	result, ok := resultTypes[field]
	if !ok {
		return []string{}
	}
	shape := graphshape.Shape(result)
	out := make([]string, 0, len(shape.Children))
	for _, child := range shape.Children {
		out = append(out, child.Name)
	}
	return out
}

// serverLogFilter is the optional contains/level/requestId filter shared by
// serverLogs and diagnosticsBundle. Lines are kept in the slog text form
// (`level=INFO`, `requestID=...`).
type serverLogFilter struct {
	contains  string
	level     string
	requestID string
}

func serverLogFilterFrom(req request) serverLogFilter {
	return serverLogFilter{
		contains:  stringVar(req, "contains"),
		level:     strings.ToUpper(trimmedVariable(req, "level")),
		requestID: trimmedVariable(req, "requestId"),
	}
}

func (f serverLogFilter) active() bool {
	return f.contains != "" || f.level != "" || f.requestID != ""
}

func (f serverLogFilter) match(line string) bool {
	if f.contains != "" && !strings.Contains(line, f.contains) {
		return false
	}
	if f.level != "" && !logLineHasAttr(line, "level", f.level) {
		return false
	}
	if f.requestID != "" && !logLineHasAttr(line, "requestID", f.requestID) {
		return false
	}
	return true
}

// logLineHasAttr reports whether a slog text line carries key=value as one of
// its attributes. Keys and values may be Go-quoted; text inside a quoted key
// or value never counts as an attribute.
func logLineHasAttr(line, key, value string) bool {
	for rest := strings.TrimLeft(line, " "); rest != ""; rest = strings.TrimLeft(rest, " ") {
		name, after := slogToken(rest, true)
		if !strings.HasPrefix(after, "=") {
			rest = after // a bare word with no value
			continue
		}
		got, next := slogToken(after[1:], false)
		if name == key && got == value {
			return true
		}
		rest = next
	}
	return false
}

// slogToken reads one key or value from the start of s and returns it
// unquoted with the remaining text. A bare key stops at '=' or a space; a bare
// value stops at a space.
func slogToken(s string, isKey bool) (string, string) {
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, `"`) {
		end := len(s)
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' {
				i++
			} else if s[i] == '"' {
				end = i + 1
				break
			}
		}
		raw := s[:end]
		if unquoted, err := strconv.Unquote(raw); err == nil {
			return unquoted, s[end:]
		}
		return strings.Trim(raw, `"`), s[end:]
	}
	stop := " "
	if isKey {
		stop = "= "
	}
	end := strings.IndexAny(s, stop)
	if end < 0 {
		end = len(s)
	}
	if end == 0 {
		// a stray '=' with no key: consume it so the scan advances
		return "", s[1:]
	}
	return s[:end], s[end:]
}

// filteredServerLogs returns up to limit of the most recent kept lines that
// match the filter, oldest first. A filtered read scans the whole tail.
func (h *Handler) filteredServerLogs(limit int, filter serverLogFilter) []string {
	if !filter.active() {
		lines := h.serverLogSource.Lines(limit)
		if lines == nil {
			return []string{}
		}
		return lines
	}
	matched := []string{}
	for _, line := range h.serverLogSource.Lines(h.serverLogSource.Capacity()) {
		if filter.match(line) {
			matched = append(matched, line)
		}
	}
	if len(matched) > limit {
		matched = matched[len(matched)-limit:]
	}
	return matched
}

// diagnosticsProfiles is the closed set of runtime profiles a bundle may carry.
// CPU and trace profiles are excluded: they sample over time and would hold
// the request open.
var diagnosticsProfiles = []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"}

// diagnosticsProfileMaxBytes bounds each profile's text.
const diagnosticsProfileMaxBytes = 256 << 10

// diagnosticsLogDefault is how many log lines a bundle carries by default.
const diagnosticsLogDefault = 200

// DiagnosticsBundleInfo is the diagnosticsBundle read on the wire.
type DiagnosticsBundleInfo struct {
	Version       string                `json:"version"`
	Revision      string                `json:"revision"`
	GoVersion     string                `json:"goVersion"`
	OS            string                `json:"os"`
	Arch          string                `json:"arch"`
	StartedAt     string                `json:"startedAt"`
	UptimeSeconds int64                 `json:"uptimeSeconds"`
	Goroutines    int                   `json:"goroutines"`
	CPUs          int                   `json:"cpus"`
	Memory        DiagnosticsMemoryInfo `json:"memory"`
	Logs          ServerLogsInfo        `json:"logs"`
	Profiles      []DiagnosticsProfile  `json:"profiles"`
}

// DiagnosticsMemoryInfo is a subset of runtime.MemStats, in bytes.
type DiagnosticsMemoryInfo struct {
	HeapAlloc    uint64 `json:"heapAlloc"`
	HeapInuse    uint64 `json:"heapInuse"`
	HeapObjects  uint64 `json:"heapObjects"`
	StackInuse   uint64 `json:"stackInuse"`
	Sys          uint64 `json:"sys"`
	TotalAlloc   uint64 `json:"totalAlloc"`
	NumGC        uint32 `json:"numGC"`
	PauseTotalNs uint64 `json:"pauseTotalNs"`
}

// DiagnosticsProfile is one runtime profile in its text form.
type DiagnosticsProfile struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

func (h *Handler) diagnosticsBundle(req request) (DiagnosticsBundleInfo, error) {
	requested, err := diagnosticsProfileNames(req)
	if err != nil {
		return DiagnosticsBundleInfo{}, err
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	bundle := DiagnosticsBundleInfo{
		Version:       serverVersion(),
		Revision:      buildRevision(),
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		StartedAt:     processStart.UTC().Format(time.RFC3339),
		UptimeSeconds: int64(time.Since(processStart) / time.Second),
		Goroutines:    runtime.NumGoroutine(),
		CPUs:          runtime.NumCPU(),
		Memory: DiagnosticsMemoryInfo{
			HeapAlloc:    mem.HeapAlloc,
			HeapInuse:    mem.HeapInuse,
			HeapObjects:  mem.HeapObjects,
			StackInuse:   mem.StackInuse,
			Sys:          mem.Sys,
			TotalAlloc:   mem.TotalAlloc,
			NumGC:        mem.NumGC,
			PauseTotalNs: mem.PauseTotalNs,
		},
		Logs:     ServerLogsInfo{Lines: []string{}},
		Profiles: []DiagnosticsProfile{},
	}
	if h.serverLogSource != nil {
		limit := boundedIntVar(req, "limit", diagnosticsLogDefault, serverLogsMaxLimit)
		bundle.Logs = ServerLogsInfo{
			Available: true,
			Capacity:  h.serverLogSource.Capacity(),
			Lines:     h.filteredServerLogs(limit, serverLogFilterFrom(req)),
		}
	}
	for _, name := range requested {
		bundle.Profiles = append(bundle.Profiles, profileText(name))
	}
	return bundle, nil
}

func diagnosticsProfileNames(req request) ([]string, error) {
	names, _ := newVars(req).StringList("profiles", "profile names")
	out := []string{}
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if !slices.Contains(diagnosticsProfiles, name) {
			return nil, clientError(fmt.Sprintf("unknown profile %q; allowed: %s", name, strings.Join(diagnosticsProfiles, ", ")))
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

func profileText(name string) DiagnosticsProfile {
	if name == "goroutine" {
		// Full stacks, the form an agent reads. runtime.Stack fills a fixed
		// buffer, so a large goroutine count never grows memory past the cap
		// (pprof's debug=2 path buffers the whole dump first).
		buf := make([]byte, diagnosticsProfileMaxBytes)
		n := runtime.Stack(buf, true)
		return DiagnosticsProfile{Name: name, Text: strings.ToValidUTF8(string(buf[:n]), ""), Truncated: n == len(buf)}
	}
	w := &cappedWriter{limit: diagnosticsProfileMaxBytes}
	if profile := pprof.Lookup(name); profile != nil {
		_ = profile.WriteTo(w, 1)
	}
	return DiagnosticsProfile{Name: name, Text: strings.ToValidUTF8(w.buf.String(), ""), Truncated: w.truncated}
}

// errProfileCapReached stops a profile write once cappedWriter is full, so the
// rest of the profile is never formatted or buffered.
var errProfileCapReached = errors.New("profile size cap reached")

// cappedWriter keeps at most limit bytes and fails the write that would exceed
// it, bounding the memory a profile read can hold.
type cappedWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	room := w.limit - w.buf.Len()
	if len(p) <= room {
		return w.buf.Write(p)
	}
	w.buf.Write(p[:max(room, 0)])
	w.truncated = true
	return max(room, 0), errProfileCapReached
}

func buildRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			return setting.Value
		}
	}
	return ""
}

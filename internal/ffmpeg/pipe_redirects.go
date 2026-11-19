package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// HTTP redirect guard compatibility (CORE-2589). Pipe runs pass the http
// protocol's -max_redirects 0, so FFmpeg never follows a redirect off the
// loopback source. FFmpeg 7.1 (Jellyfin) and 9.x have that option; stock
// 8.0.x does not, and rejects the whole argv ("Unrecognized option
// 'max_redirects'. Error splitting the argument list: Option not found"), so
// every pipe job would fail on such a host.
//
// Each tool binary is asked once whether its http protocol lists the option.
// A build that does keeps the guard. A build that does not runs without it:
// the input is always the encderiv loopback endpoint (internal/encderiv), which
// answers only 200, 206 or an error and never a redirect, and the protocol
// allowlist stays http,tcp, so there is no redirect for FFmpeg to follow.

const maxRedirectsOption = "-max_redirects"

// httpProtocolHelpRunner prints toolPath's http protocol options.
type httpProtocolHelpRunner func(ctx context.Context, toolPath string) ([]byte, error)

const httpProtocolHelpTimeout = 10 * time.Second

// httpProtocolHelpLimit bounds the help listing, a few dozen lines.
const httpProtocolHelpLimit = 256 << 10

// defaultHTTPProtocolHelp is the production httpProtocolHelpRunner: one
// bounded, supervised `-h protocol=http` run of the resolved tool.
func defaultHTTPProtocolHelp(ctx context.Context, toolPath string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, httpProtocolHelpTimeout)
	defer cancel()
	// #nosec G204 -- toolPath is resolved from PATH by the caller and the
	// arguments are fixed.
	cmd := exec.CommandContext(ctx, toolPath, "-hide_banner", "-h", "protocol=http")
	out := &boundedBuffer{limit: httpProtocolHelpLimit}
	if err := supervise(ctx, cmd, out, io.Discard); err != nil {
		return nil, err
	}
	if out.overflowed() {
		return nil, ErrOutputOverflow
	}
	return out.bytes(), nil
}

type maxRedirectsEntry struct {
	once      sync.Once
	supported bool
}

var (
	maxRedirectsMu     sync.Mutex
	maxRedirectsByTool                        = map[string]*maxRedirectsEntry{}
	httpProtocolHelp   httpProtocolHelpRunner = defaultHTTPProtocolHelp
)

// toolHasMaxRedirects reports whether toolPath's http protocol takes
// -max_redirects, asking the binary once per path and file identity. A
// listing that fails counts as supported: the run then fails exactly as it did
// before this check, rather than silently dropping the guard.
func toolHasMaxRedirects(toolPath string) bool {
	key := toolIdentityKey(toolPath)
	maxRedirectsMu.Lock()
	entry, ok := maxRedirectsByTool[key]
	if !ok {
		entry = &maxRedirectsEntry{}
		maxRedirectsByTool[key] = entry
	}
	run := httpProtocolHelp
	maxRedirectsMu.Unlock()
	entry.once.Do(func() {
		help, err := run(context.Background(), toolPath)
		entry.supported = err != nil || bytes.Contains(help, []byte(maxRedirectsOption+" "))
	})
	return entry.supported
}

// toolIdentityKey keys the capability cache by the probed binary itself, so
// replacing ffmpeg or ffprobe in place (an image upgrade) re-asks that tool. A
// path that cannot be stat'd keys by path alone.
func toolIdentityKey(toolPath string) string {
	info, err := os.Stat(toolPath)
	if err != nil {
		return toolPath
	}
	return fmt.Sprintf("%s\x00%d\x00%d", toolPath, info.Size(), info.ModTime().UnixNano())
}

// pipeArgsForTool adapts a pipe argv built with pipeInputOptions to the tool
// that will run it, dropping the -max_redirects pair on a build without it.
func pipeArgsForTool(toolPath string, args []string) []string {
	if toolHasMaxRedirects(toolPath) {
		return args
	}
	return withoutMaxRedirects(args)
}

// withoutMaxRedirects returns args without any "-max_redirects <n>" pair.
func withoutMaxRedirects(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == maxRedirectsOption && i+1 < len(args) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

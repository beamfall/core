// Command fixtureworker is a minimal Integration Worker used only by
// internal/integrationhost tests to prove out-of-process isolation (ADR-0058).
// It connects to the Host API over the unix-domain socket named by
// --host-api-socket, presents the capability token read from fd3, then performs
// the single Host API request named by --probe and prints the response (or the
// error) to stdout. It has NO access to a database, admin token, GraphQL
// endpoint, or raw filesystem beyond what it is given — exactly the isolation
// the supervisor enforces.
package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/beamfall/core/internal/integrationhost"
)

func main() {
	socket := flag.String("host-api-socket", "", "Host API unix-domain socket path")
	probe := flag.String("probe", "read-catalog", "Host API method to invoke")
	treeMode := flag.String("tree-mode", "", "test-only process-tree mode")
	pidFile := flag.String("pid-file", "", "test-only process-tree pid file")
	holdFile := flag.String("hold-file", "", "test-only process-tree release file")
	descendant := flag.Bool("descendant", false, "run as a test-only descendant")
	termResistant := flag.Bool("term-resistant", false, "ignore TERM in test-only descendant mode")
	readyFile := flag.String("ready-file", "", "test-only descendant readiness file")
	isolationSecret := flag.String("isolation-secret", "", "test-only forbidden file probe")
	isolationNetwork := flag.Bool("isolation-network", false, "test-only raw network probe")
	flag.Parse()
	if *isolationSecret != "" || *isolationNetwork {
		runIsolationProbe(*isolationSecret, *isolationNetwork)
		return
	}

	if *descendant {
		runDescendant(*termResistant, *readyFile)
		return
	}
	if *treeMode != "" {
		runProcessTree(*treeMode, *pidFile, *holdFile, *termResistant)
		return
	}

	// The capability token arrives on fd3 (the first ExtraFile). Read it to EOF.
	tokenFile := os.NewFile(3, "capability-token")
	if tokenFile == nil {
		fmt.Println("ERR: no capability token fd")
		os.Exit(2)
	}
	tokenBytes, err := io.ReadAll(tokenFile)
	if err != nil {
		fmt.Printf("ERR: read token: %v\n", err)
		os.Exit(2)
	}

	conn, err := net.Dial("unix", *socket)
	if err != nil {
		fmt.Printf("ERR: dial: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = conn.Close() }()

	if _, err := integrationhost.NegotiateWorker(conn, string(tokenBytes), integrationhost.VersionOffer{
		MinVersion: 1,
		MaxVersion: 2,
	}, 5*time.Second); err != nil {
		fmt.Printf("ERR: handshake rejected: %v\n", err)
		os.Exit(2)
	}

	// One request named by --probe.
	if err := writeFrame(conn, mustJSON(map[string]any{"method": *probe, "ref": "api-key", "limit": 50})); err != nil {
		fmt.Printf("ERR: request write: %v\n", err)
		os.Exit(2)
	}
	resp, err := readFrame(conn)
	if err != nil {
		fmt.Printf("ERR: response read: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("RESP: %s\n", string(resp))
}

func runIsolationProbe(secretPath string, networkProbe bool) {
	if secretPath != "" {
		if _, err := os.ReadFile(secretPath); err == nil {
			fmt.Println("BREACH: unrelated secret was readable")
			os.Exit(1)
		}
	}
	if networkProbe {
		conn, err := net.DialTimeout("tcp", "1.1.1.1:443", 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			fmt.Println("BREACH: raw network connection succeeded")
			os.Exit(1)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func writeFrame(conn net.Conn, payload []byte) error {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := conn.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

func readFrame(conn net.Conn) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(conn, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

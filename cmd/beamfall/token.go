package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/beamfall/core/internal/config"
	"github.com/beamfall/core/internal/store"
)

func tokenCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if helpArg(args) {
		_, err := fmt.Fprint(stdout, tokenHelp)
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("token subcommand is required\n\n%s", tokenHelp)
	}
	switch args[0] {
	case "create":
		return tokenCreate(ctx, args[1:], stdout)
	case "list":
		return tokenList(ctx, args[1:], stdout)
	case "revoke":
		return tokenRevoke(ctx, args[1:], stdout)
	default:
		return fmt.Errorf("unknown token subcommand %q", args[0])
	}
}

const tokenHelp = `Usage: beamfall token <command>

Commands:
  create   create a local API token while the server is stopped
  list     list API tokens through the running server
  revoke   revoke an API token through the running server
`

func tokenCreate(ctx context.Context, args []string, stdout io.Writer) error {
	cfg := config.Default()
	if value := os.Getenv("BEAMFALL_DATA_DIR"); value != "" {
		cfg.DataDir = value
	}
	var name string
	fs := commandFlagSet("beamfall token create", stdout)
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "data directory")
	fs.StringVar(&name, "name", "", "token name")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if err := refuseServerLock(cfg.DataDir); err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.DataDir, store.Options{})
	if err != nil {
		return err
	}
	defer st.Close()
	_, fullToken, err := st.CreateAPIToken(ctx, name, time.Now().UTC())
	if err != nil {
		return err
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "%s\n", fullToken)
	}
	return nil
}

func refuseServerLock(dataDir string) error {
	_, err := os.Stat(filepath.Join(dataDir, "server.lock"))
	if err == nil {
		return errors.New("server appears to be running; stop the server before bootstrapping the first API token")
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func tokenList(ctx context.Context, args []string, stdout io.Writer) error {
	var opts cliHTTPOptions
	fs := commandFlagSet("beamfall token list", stdout)
	addHTTPFlags(fs, &opts)
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	var resp struct {
		APITokens []cliAPITokenInfo `json:"apiTokens"`
	}
	if err := cliGraphQL(ctx, opts,
		"query APITokens { apiTokens { id name createdAt lastUsedAt revoked } }",
		map[string]any{},
		&resp,
	); err != nil {
		return err
	}
	for _, token := range resp.APITokens {
		if stdout != nil {
			_, _ = fmt.Fprintf(stdout, "id=%s name=%q created=%s last_used=%s revoked=%t\n", token.ID, token.Name, token.CreatedAt, token.LastUsedAt, token.Revoked)
		}
	}
	return nil
}

func tokenRevoke(ctx context.Context, args []string, stdout io.Writer) error {
	var opts cliHTTPOptions
	var id string
	fs := commandFlagSet("beamfall token revoke", stdout)
	addHTTPFlags(fs, &opts)
	fs.StringVar(&id, "id", "", "token id")
	if helped, err := parseCommandFlags(fs, args); err != nil || helped {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if id == "" {
		return fmt.Errorf("--id is required")
	}
	var resp struct {
		RevokeAPIToken cliAPITokenInfo `json:"revokeApiToken"`
	}
	if err := cliGraphQL(ctx, opts,
		"mutation RevokeAPIToken($id: String!) { revokeApiToken(id: $id) { id name createdAt lastUsedAt revoked } }",
		map[string]any{"id": id},
		&resp,
	); err != nil {
		return err
	}
	if stdout != nil {
		_, _ = fmt.Fprintf(stdout, "id=%s revoked=%t\n", resp.RevokeAPIToken.ID, resp.RevokeAPIToken.Revoked)
	}
	return nil
}

type cliAPITokenInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt"`
	Revoked    bool   `json:"revoked"`
}

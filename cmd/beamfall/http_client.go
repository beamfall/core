package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/beamfall/core/internal/config"
)

const defaultCLIHTTPTimeout = 15 * time.Second

var defaultCLIHTTPClient = &http.Client{Timeout: defaultCLIHTTPTimeout}

type cliHTTPOptions struct {
	server string
	token  string
}

func addHTTPFlags(fs *flag.FlagSet, opts *cliHTTPOptions) {
	opts.server = os.Getenv("BEAMFALL_SERVER")
	if opts.server == "" {
		opts.server = "http://" + config.Default().Addr
	}
	fs.StringVar(&opts.server, "server", opts.server, "Beamfall server URL")
	fs.StringVar(&opts.token, "token", "", "API token")
}

func resolveHTTPOptions(opts cliHTTPOptions) (cliHTTPOptions, error) {
	if strings.TrimSpace(opts.token) == "" {
		opts.token = strings.TrimSpace(os.Getenv("BEAMFALL_TOKEN"))
	}
	opts.server = strings.TrimSpace(opts.server)
	if opts.server == "" {
		opts.server = "http://" + config.Default().Addr
	}
	if !strings.Contains(opts.server, "://") {
		opts.server = "http://" + opts.server
	}
	if _, err := url.ParseRequestURI(opts.server); err != nil {
		return cliHTTPOptions{}, fmt.Errorf("invalid server URL")
	}
	if strings.TrimSpace(opts.token) == "" {
		return cliHTTPOptions{}, errors.New("API token is required; set --token or BEAMFALL_TOKEN and check the server is running")
	}
	return opts, nil
}

type graphEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func cliGraphQL(ctx context.Context, opts cliHTTPOptions, query string, variables map[string]any, out any) error {
	opts, err := resolveHTTPOptions(opts)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return err
	}
	endpoint := strings.TrimRight(opts.server, "/") + "/api/graphql"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.token)
	resp, err := defaultCLIHTTPClient.Do(req)
	if err != nil {
		return errors.New("could not reach Beamfall server; check the server is running and the token is set")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("invalid API token")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s; check the server is running and the token is set", resp.Status)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var envelope graphEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		if envelope.Errors[0].Message == "unauthorized" {
			return errors.New("invalid API token")
		}
		return errors.New(envelope.Errors[0].Message)
	}
	if out == nil {
		return nil
	}
	if len(envelope.Data) == 0 {
		return errors.New("server returned no data")
	}
	return json.Unmarshal(envelope.Data, out)
}

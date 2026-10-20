package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

type hostList []string

func (h *hostList) String() string { return fmt.Sprint([]string(*h)) }
func (h *hostList) Set(value string) error {
	*h = append(*h, value)
	return nil
}

func main() {
	var hosts hostList
	feed := flag.String("feed", "", "public RSS feed URL")
	guid := flag.String("episode-guid", "", "GUID of the episode to re-fetch")
	output := flag.String("output", "", "snapshot output root")
	interval := flag.Duration("interval", 6*time.Hour, "positive delay between the two fetches")
	ack := flag.Bool("policy-acknowledged", false, "confirm host ToS and regional fetch constraints were reviewed")
	flag.Var(&hosts, "allow-host", "exact policy-reviewed source host (repeat for feed/CDN hosts)")
	flag.Parse()

	result, err := NewFetcher().Fetch(context.Background(), Request{
		FeedURL: *feed, EpisodeGUID: *guid, OutputRoot: *output, Interval: *interval,
		AllowedHosts: hosts, PolicyAcknowledged: *ack,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "podcast-corpus-fetcher:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "podcast-corpus-fetcher:", err)
		os.Exit(1)
	}
}

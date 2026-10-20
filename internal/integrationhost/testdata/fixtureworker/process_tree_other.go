//go:build !unix

package main

import "os"

func runProcessTree(_, _, _ string, _ bool) { os.Exit(15) }

func runDescendant(_ bool, _ string) { os.Exit(15) }

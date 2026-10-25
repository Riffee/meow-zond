//go:build !windows

package main

import "meowzond/internal/probe"

func runService(_ probe.Config) (bool, error) { return false, nil }

//go:build windows

package ssh

import "os"

var hostsFilePath = os.ExpandEnv("${SystemRoot}\System32\drivers\etc\hosts")

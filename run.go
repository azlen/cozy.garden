package main

import (
	"flag"
	"fmt"
	// "net/url"
	"os"
	// "strings"

	"cerca/server"
	// "cerca/util"
)

func complain(msg string) {
	fmt.Printf("cozy garden: %s\n", msg)
	os.Exit(0)
}

func main() {
	// var allowlistLocation string
	var sessionKey string
	var dev bool
	flag.BoolVar(&dev, "dev", false, "trigger development mode")
	// flag.StringVar(&allowlistLocation, "allowlist", "", "domains which can be used to read verification codes from during registration")
	flag.StringVar(&sessionKey, "authkey", "", "session cookies authentication key")
	flag.Parse()
	if len(sessionKey) == 0 {
		complain("please pass a random session auth key with --authkey")
	}
	// allowlist := readAllowlist(allowlistLocation)
	// allowlist = append(allowlist, "merveilles.town")
	server.Serve(sessionKey, dev)
}

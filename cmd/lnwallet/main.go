// Copyright (c) 2013-2017 The btcsuite developers
// Copyright (c) 2015-2016 The Decred developers
// Copyright (C) 2015-2017 The Lightning Network Developers

package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/urfave/cli"
	"golang.org/x/crypto/ssh/terminal"
)

var (
	//Commit stores the current commit hash of this build. This should be
	//set using -ldflags during compilation.
	Commit string

	defaultAccount = uint32(waddrmgr.DefaultAccountNum)

	errNoConsole = errors.New("wallet db requires console access")
)

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "[lnwallet] %v\n", err)
	os.Exit(1)
}

func getNetParams(ctx *cli.Context) *chaincfg.Params {
	if ctx.GlobalBool("testnet") {
		return &chaincfg.TestNet3Params
	}
	return &chaincfg.MainNetParams
}

func readPassword(ctx *cli.Context, userQuery string) []byte {
	if ctx.GlobalIsSet("password") {
		return []byte(ctx.GlobalString("password"))
	}
	if terminal.IsTerminal(syscall.Stdin) {
		fmt.Printf(userQuery)
		pw, err := terminal.ReadPassword(int(syscall.Stdin))
		if err != nil {
			fatal(err)
		}
		fmt.Println()
		return pw
	} else {
		reader := bufio.NewReader(os.Stdin)
		pw, err := reader.ReadBytes('\n')
		if err != nil {
			fatal(err)
		}
		return pw
	}
}

func noConsole() ([]byte, error) {
	return nil, errNoConsole
}

func main() {
	app := cli.NewApp()
	app.Name = "lnwallet"
	app.Version = fmt.Sprintf("%s commit=%s", "0.4.2", Commit)
	app.Usage = "wallet utility for your Lightning Network Daemon (lnd)"
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "testnet",
			Usage: "use testnet parameters",
		},
		cli.StringFlag{
			Name:  "password",
			Usage: "wallet password as a command line parameter " +
				"(not recommended for security reasons!)",
		},
	}
	app.Commands = []cli.Command{
		dumpWalletCommand,
		walletInfoCommand,
	}

	if err := app.Run(os.Args); err != nil {
		fatal(err)
	}
}

// cleanAndExpandPath expands environment variables and leading ~ in the
// passed path, cleans the result, and returns it.
// This function is taken from https://github.com/btcsuite/btcd
func cleanAndExpandPath(path string) string {
	if path == "" {
		return ""
	}

	// Expand initial ~ to OS specific home directory.
	if strings.HasPrefix(path, "~") {
		var homeDir string
		u, err := user.Current()
		if err == nil {
			homeDir = u.HomeDir
		} else {
			homeDir = os.Getenv("HOME")
		}

		path = strings.Replace(path, "~", homeDir, 1)
	}

	// NOTE: The os.ExpandEnv doesn't work with Windows-style %VARIABLE%,
	// but the variables can still be expanded via POSIX-style $VARIABLE.
	return filepath.Clean(os.ExpandEnv(path))
}

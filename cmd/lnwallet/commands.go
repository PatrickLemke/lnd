package main

import (
	"fmt"

	"github.com/urfave/cli"
	"github.com/btcsuite/btcwallet/wallet"
	"github.com/btcsuite/btcwallet/walletdb"
	"github.com/btcsuite/btcwallet/waddrmgr"

	// This is required to register bdb as a valid walletdb driver. In the
	// init function of the package, it registers itself. The import is used
	// to activate the side effects w/o actually binding the package name to
	// a file-level variable.
	_ "github.com/btcsuite/btcwallet/walletdb/bdb"
	"github.com/lightningnetwork/lnd/lnwallet"
	"time"
	"github.com/lightningnetwork/lnd/keychain"
	"encoding/hex"
	"github.com/btcsuite/btcd/btcec"
)

var (
	waddrmgrNamespaceKey = []byte("waddrmgr")

	walletFile      string
	publicWalletPw  = lnwallet.DefaultPublicPassphrase
	privateWalletPw = lnwallet.DefaultPrivatePassphrase
	openCallbacks   = &waddrmgr.OpenCallbacks{
		ObtainSeed:        noConsole,
		ObtainPrivatePass: noConsole,
	}
)

func openWalletDbFile(ctx *cli.Context) (walletdb.DB, error) {
	args := ctx.Args()

	// Parse and clean up wallet file parameter.
	switch {
	case ctx.IsSet("wallet_file"):
		walletFile = ctx.String("wallet_file")
	case args.Present():
		walletFile = args.First()
		args = args.Tail()
	default:
		return nil, fmt.Errorf("Wallet-file argument missing")
	}
	walletFile = cleanAndExpandPath(walletFile)

	// Ask the user for the wallet password. If it's empty, the default
	// password will be used, since the lnd wallet is always encrypted.
	pw := readPassword(ctx, "Input wallet password: ")
	if len(pw) > 0 {
		publicWalletPw = pw
		privateWalletPw = pw
	}

	// Try to load and open the wallet.
	db, err := walletdb.Open("bdb", walletFile)
	if err != nil {
		fmt.Errorf("Failed to open database: %v", err)
		return nil, err
	}
	return db, nil
}

var walletInfoCommand = cli.Command{
	Name:      "walletinfo",
	Category:  "wallet",
	Usage:     "Show all relevant info of a lnd wallet.",
	ArgsUsage: "wallet-file",
	Description: `
	Show information about the specified lnd wallet.
	Information includes the public key and number of addresses used.`,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "wallet_file",
			Usage: "the path to the wallet.db file",
		},
		cli.BoolFlag{
			Name:  "with_root_key",
			Usage: "also show the BIP32 extended root key",
		},
	},
	Action: walletInfo,
}

func walletInfo(ctx *cli.Context) error {
	// Show command help if no arguments provided.
	if ctx.NArg() == 0 && ctx.NumFlags() == 0 {
		cli.ShowCommandHelp(ctx, "walletinfo")
		return nil
	}

	db, err := openWalletDbFile(ctx)
	if err != nil {
		return err
	}

	w, err := wallet.Open(
		db, publicWalletPw, openCallbacks, getNetParams(ctx), 0,
	)
	if err != nil {
		// If opening the wallet fails (e.g. because of wrong
		// passphrase), we must close the backing database to
		// allow future calls to walletdb.Open().
		e := db.Close()
		if e != nil {
			return fmt.Errorf("error closing database: %v", e)
		}
		return err
	}

	w.Start()
	err = w.Unlock(privateWalletPw, nil)
	if err != nil {
		return err
	}

	err = walletdb.View(db, func (tx walletdb.ReadTx) error {
		bucket := tx.ReadBucket(waddrmgrNamespaceKey)
		managers := w.Manager.ActiveScopedKeyManagers()
		for _, m := range managers {
			address, err := m.DeriveFromKeyPath(bucket, waddrmgr.DerivationPath{
				Account: uint32(keychain.KeyFamilyNodeKey),
				Branch:  0,
				Index:   0,
			})
			if address == nil {
				continue
			}
			if err != nil {
				fmt.Errorf("error when reading account: %v", err)
			}
			key, err := w.PrivKeyForAddress(address.Address())
			if err != nil {
				fmt.Errorf("error when getting priv key: %v", err)
			}
			if key == nil {
				continue
			}
			key.Curve = btcec.S256()
			fmt.Printf("%d: %s\n", m.Scope().Purpose, hex.EncodeToString(key.PubKey().SerializeCompressed()))
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

var dumpWalletCommand = cli.Command{
	Name:      "dumpwallet",
	Category:  "Wallet",
	Usage:     "Dump the private keys of a lnd wallet.",
	ArgsUsage: "wallet-file",
	Description: `
	Generate a bitcoind compatible dump of the lnd wallet.
	All used private keys and addresses are dumped as a text representation
	that can then be imported by bitcoind.`,
	Flags: []cli.Flag{
		cli.StringFlag{
			Name:  "wallet_file",
			Usage: "the path to the wallet.db file",
		},
	},
	Action: dumpWallet,
}

func dumpWallet(ctx *cli.Context) error {
	// Show command help if no arguments provided.
	if ctx.NArg() == 0 && ctx.NumFlags() == 0 {
		cli.ShowCommandHelp(ctx, "dumpwallet")
		return nil
	}

	db, err := openWalletDbFile(ctx)
	if err != nil {
		return err
	}

	w, err := wallet.Open(
		db, publicWalletPw, openCallbacks, getNetParams(ctx), 0,
	)
	if err != nil {
		// If opening the wallet fails (e.g. because of wrong
		// passphrase), we must close the backing database to
		// allow future calls to walletdb.Open().
		e := db.Close()
		if e != nil {
			return fmt.Errorf("error closing database: %v", e)
		}
		return err
	}

	w.Start()
	err = w.Unlock(privateWalletPw, nil)
	if err != nil {
		return err
	}

	// Now collect all the information we can about the default account,
	// get all addresses and their private key.
	amount, err := w.CalculateBalance(0)
	if err != nil {
		return err
	}
	block := w.Manager.SyncedTo()
	fmt.Printf("# Wallet dump created by lnwallet %s\n", ctx.App.Version)
	fmt.Printf("# * Created on %s\n", time.Now().UTC())
	fmt.Printf("# * Best block at time of backup was %d (%s),\n",
		block.Height, block.Hash.String())
	fmt.Printf("#   mined on %s", block.Timestamp.UTC())
	fmt.Printf("# * Total balance: %.8f\n\n", amount.ToBTC())

	addrs, err := w.AccountAddresses(defaultAccount)
	if err != nil {
		return err
	}
	var empty struct{}
	for _, addr := range addrs {
		privateKey, err := w.DumpWIFPrivateKey(addr)
		if err != nil {
			return fmt.Errorf("error getting address info: %v", err)
		}
		fmt.Printf(
			"%s 1970-01-01T00:00:01Z label= # addr=%s",
			privateKey, addr.EncodeAddress(),
		)
		list := make(map[string]struct{})
		list[addr.EncodeAddress()] = empty
		unspent, err := w.ListUnspent(0, 999999, list)
		if err != nil {
			return err
		}
		for _, u := range unspent {
			fmt.Printf(" unspent=%f", u.Amount)
		}
		fmt.Println()
	}

	w.Stop()
	db.Close()
	return nil
}

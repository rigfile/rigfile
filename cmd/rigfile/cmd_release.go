package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/digitaldreamer3462/rigfile/internal/minisign"
	"github.com/digitaldreamer3462/rigfile/internal/selfupdate"
)

// cmdSelfUpdate replaces this binary with the latest release after verifying its signature and checksum.
func cmdSelfUpdate(args []string, e env) int {
	fs := flag.NewFlagSet("self-update", flag.ContinueOnError)
	fs.SetOutput(e.err)
	check := fs.Bool("check", false, "verify the latest release and say what would change, without replacing anything")
	down := fs.Bool("allow-downgrade", false, "accept a release older than this build")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 0 {
		fmt.Fprintln(e.err, "usage: rigfile self-update [--check] [--allow-downgrade]")
		return 2
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	res, err := selfupdate.Run(context.Background(), selfupdate.Options{
		Current: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Exe: exe, CheckOnly: *check, AllowDowngrade: *down,
	})
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	switch {
	case res.UpToDate:
		fmt.Fprintf(e.out, "rigfile %s is up to date\n", version)
	case *check:
		fmt.Fprintf(e.out, "%s is available and verified (this build is %s); run `rigfile self-update` to install it\n", res.Latest, version)
	default:
		fmt.Fprintf(e.out, "updated to %s (the previous binary is kept as %s.old)\n", res.Latest, exe)
	}
	return 0
}

// cmdVerifySignature checks a minisign signature: used to confirm the verifier agrees with the real minisign tool, and
// by anyone who wants to verify a download by hand.
func cmdVerifySignature(args []string, e env) int {
	fs := flag.NewFlagSet("verify-signature", flag.ContinueOnError)
	fs.SetOutput(e.err)
	pub := fs.String("pubkey", "", "minisign public key file (default: the key built into this binary)")
	sig := fs.String("sig", "", "signature file (default: <file>.minisig)")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) != 1 {
		fmt.Fprintln(e.err, "usage: rigfile verify-signature <file> [--sig file.minisig] [--pubkey key.pub]")
		return 2
	}
	keyText := selfupdate.PublicKey
	if *pub != "" {
		b, err := os.ReadFile(*pub)
		if err != nil {
			fmt.Fprintln(e.err, "rigfile:", err)
			return 1
		}
		keyText = string(b)
	}
	if keyText == "" {
		fmt.Fprintln(e.err, "rigfile:", selfupdate.ErrNoKey)
		return 1
	}
	pk, err := minisign.ParsePublicKey(keyText)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if *sig == "" {
		*sig = pos[0] + ".minisig"
	}
	msg, err := os.ReadFile(pos[0])
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	st, err := os.ReadFile(*sig)
	if err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	if err := minisign.Verify(pk, msg, string(st)); err != nil {
		fmt.Fprintln(e.err, "rigfile:", err)
		return 1
	}
	fmt.Fprintf(e.out, "%s: signature is valid\n", pos[0])
	return 0
}

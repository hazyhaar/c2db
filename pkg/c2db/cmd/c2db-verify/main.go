// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"crypto/mldsa"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hazyhaar/c2db/pkg/c2db"
)

const snapMagic = uint32(0x4332534E)

var (
	errBadKey       = errors.New("c2db-verify: key must be 64 hex digits")
	errUnknownMagic = errors.New("c2db-verify: unknown magic")
	errUsage        = errors.New("usage: c2db-verify -key hex64 -in fichier.arch|fichier.snap")
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("c2db-verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	keyHex := fs.String("key", "", "")
	inPath := fs.String("in", "", "")
	pubPath := fs.String("pubkey", "", "")
	if err := fs.Parse(args); err != nil || *keyHex == "" || *inPath == "" || fs.NArg() != 0 {
		fmt.Fprintln(stderr, errUsage)
		return 1
	}
	key, err := parseKey(*keyHex)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	var pub *mldsa.PublicKey
	if *pubPath != "" {
		pub, err = loadPub(*pubPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	if err := verifyFile(*inPath, key, pub); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func loadPub(path string) (*mldsa.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) != mldsa.MLDSA44PublicKeySize {
		return nil, errors.New("c2db-verify: pubkey must be 1312 bytes")
	}
	return mldsa.NewPublicKey(mldsa.MLDSA44(), raw)
}

func parseKey(s string) ([32]byte, error) {
	var key [32]byte
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return key, errBadKey
	}
	copy(key[:], raw)
	return key, nil
}

func verifyFile(path string, key [32]byte, pub *mldsa.PublicKey) error {
	magic, err := peekMagic(path)
	if err != nil {
		return err
	}
	switch magic {
	case c2db.ArchMagic:
		if pub != nil {
			return c2db.VerifyArchiveAuth(path, key, pub)
		}
		return c2db.VerifyArchive(path, key)
	case snapMagic:
		if pub != nil {
			return c2db.VerifySnapshotAuth(path, key, pub)
		}
		return c2db.VerifySnapshot(path, key)
	default:
		return errUnknownMagic
	}
}

func peekMagic(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var mag [4]byte
	if _, err := io.ReadFull(f, mag[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(mag[:]), nil
}

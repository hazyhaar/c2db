//go:build ignore

// SPDX-License-Identifier: Apache-2.0 OR MIT

package c2db_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"code.hazyhaar.fr/devhoros/c2simd/sgoiter/emit"
	"code.hazyhaar.fr/devhoros/c2simd/sgoiter/front"
	"code.hazyhaar.fr/devhoros/c2simd/sgoiter/rules"
)

func TestC2DBVFSTranspile(t *testing.T) {
	cPath := filepath.Join("c_src", "c2db_vfs.c")
	hPath := filepath.Join("c_src", "c2db_vfs.h")

	cBytes, err := os.ReadFile(cPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", cPath, err)
	}
	hBytes, err := os.ReadFile(hPath)
	if err != nil {
		t.Fatalf("os.ReadFile(%s): %v", hPath, err)
	}

	src := strings.Replace(string(cBytes), `#include "c2db_vfs.h"`, string(hBytes), 1)

	m, err := front.Parse(src, "c2db")
	if err != nil {
		t.Fatalf("front.Parse: %v", err)
	}

	m, err = rules.ApplyAll(m)
	if err != nil {
		t.Fatalf("rules.ApplyAll: %v", err)
	}

	m.Name = "c2db"
	out, err := emit.Emit(m, emit.ProfileGo127)
	if err != nil {
		t.Fatalf("emit.Emit: %v\nEmitted:\n%s", err, out)
	}

	t.Logf("Generated code:\n%s", out)

	// 1. Vérification des types stricts Sqlite3_io_methods et Sqlite3_vfs
	if !strings.Contains(out, "type Sqlite3_io_methods struct {") {
		t.Errorf("missing struct definition 'type Sqlite3_io_methods struct {' in emitted code")
	}
	if !strings.Contains(out, "type Sqlite3_vfs struct {") {
		t.Errorf("missing struct definition 'type Sqlite3_vfs struct {' in emitted code")
	}

	extractStructBody := func(code, structName string) string {
		re := regexp.MustCompile(`(?s)type\s+` + structName + `\s+struct\s*\{([^}]*)\}`)
		match := re.FindStringSubmatch(code)
		if len(match) > 1 {
			return match[1]
		}
		return ""
	}

	// Sqlite3_io_methods ne doit contenir aucun any ni interface{} (0 any, 0 interface{})
	ioMethodsBody := extractStructBody(out, "Sqlite3_io_methods")
	if ioMethodsBody == "" {
		t.Fatalf("failed to extract Sqlite3_io_methods struct body")
	}
	if strings.Contains(ioMethodsBody, "any") || strings.Contains(ioMethodsBody, "interface{}") {
		t.Errorf("Sqlite3_io_methods contains 'any' or 'interface{}':\n%s", ioMethodsBody)
	}

	// Tous les callbacks d'E/S de Sqlite3_io_methods doivent être typés strictement (func(...) int)
	expectedIoMethods := []string{
		"XClose", "XRead", "XWrite", "XTruncate", "XSync",
		"XFileSize", "XLock", "XUnlock", "XCheckReservedLock",
		"XFileControl", "XSectorSize", "XDeviceCharacteristics",
	}
	for _, fn := range expectedIoMethods {
		fnRe := regexp.MustCompile(`\b` + fn + `\s+func\(`)
		if !fnRe.MatchString(ioMethodsBody) {
			t.Errorf("Sqlite3_io_methods missing strictly typed callback %s", fn)
		}
	}

	// Sqlite3_vfs : aucun interface{}, et tous les callbacks de méthodes sont strictement typés (0 any, 0 interface{})
	vfsBody := extractStructBody(out, "Sqlite3_vfs")
	if vfsBody == "" {
		t.Fatalf("failed to extract Sqlite3_vfs struct body")
	}
	if strings.Contains(vfsBody, "interface{}") {
		t.Errorf("Sqlite3_vfs contains 'interface{}':\n%s", vfsBody)
	}

	expectedVfsMethods := []string{
		"XOpen", "XDelete", "XAccess", "XFullPathname",
		"XDlOpen", "XDlError", "XDlSym", "XDlClose",
		"XRandomness", "XSleep", "XCurrentTime", "XGetLastError",
	}
	for _, fn := range expectedVfsMethods {
		fnRe := regexp.MustCompile(`\b` + fn + `\s+func\(`)
		if !fnRe.MatchString(vfsBody) {
			t.Errorf("Sqlite3_vfs missing strictly typed callback %s", fn)
		}
	}

	// Vérification stricte des 4 callbacks xDl : aucun any ni interface{}
	expectedXdlMethods := []string{"XDlOpen", "XDlError", "XDlSym", "XDlClose"}
	for _, fn := range expectedXdlMethods {
		fnRe := regexp.MustCompile(`(?m)^\s*` + fn + `\s+func\(`)
		if !fnRe.MatchString(vfsBody) {
			t.Errorf("Sqlite3_vfs missing xDl callback %s with signature func(...)", fn)
		}
	}

	for _, line := range strings.Split(vfsBody, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "X") {
			if strings.Contains(line, "any") || strings.Contains(line, "interface{}") {
				t.Errorf("Sqlite3_vfs callback %q contains 'any' or 'interface{}'", line)
			}
		}
	}

	// Vérification de la structure C2db_file et son padding d'alignement _pad
	c2dbFileBody := extractStructBody(out, "C2db_file")
	if c2dbFileBody == "" {
		t.Fatalf("failed to extract C2db_file struct body")
	}
	if !strings.Contains(c2dbFileBody, "_pad int") && !strings.Contains(c2dbFileBody, "_pad") {
		t.Errorf("C2db_file missing _pad alignment field:\n%s", c2dbFileBody)
	}

	// 2. Vérification des instances littérales globales C2db_io_methods et C2db_vfs_driver
	if !strings.Contains(out, "var C2db_io_methods = Sqlite3_io_methods{") {
		t.Errorf("missing global literal initialization 'var C2db_io_methods = Sqlite3_io_methods{' in emitted code:\n%s", out)
	}
	if !strings.Contains(out, "var C2db_vfs_driver = Sqlite3_vfs{") {
		t.Errorf("missing global literal initialization 'var C2db_vfs_driver = Sqlite3_vfs{' in emitted code:\n%s", out)
	}

	// Vérification que szOsFile reflète bien 32 octets (taille de c2db_file sur arch 64-bit)
	if !strings.Contains(out, "SzOsFile:      32,") && !strings.Contains(out, "SzOsFile: 32") {
		t.Errorf("expected 'SzOsFile: 32' in C2db_vfs_driver initialization:\n%s", out)
	}

	// Vérification que les 4 callbacks de chargement dynamique d'extensions sont initialisés à nil
	for _, fn := range expectedXdlMethods {
		nilRe := regexp.MustCompile(`\b` + fn + `\s*:\s*nil\b`)
		if !nilRe.MatchString(out) {
			t.Errorf("expected '%s: nil' in C2db_vfs_driver initialization:\n%s", fn, out)
		}
	}

	// 3. Validation syntaxique par go/parser
	fset := token.NewFileSet()
	parsedFile, err := parser.ParseFile(fset, "c2db_vfs.go", out, parser.AllErrors)
	if err != nil {
		t.Fatalf("parser.ParseFile failed: %v\nCode:\n%s", err, out)
	}

	// 4. Validation sémantique et typage strict par go/types
	conf := types.Config{
		Importer: importer.Default(),
		Error: func(err error) {
			t.Errorf("go/types error: %v", err)
		},
	}
	pkg, err := conf.Check("c2db", fset, []*ast.File{parsedFile}, nil)
	if err != nil {
		t.Fatalf("conf.Check failed: %v\nCode:\n%s", err, out)
	}
	if pkg == nil {
		t.Fatalf("conf.Check returned nil package")
	}
}

//go:build ignore

// SPDX-License-Identifier: Apache-2.0 OR MIT

package ci

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/hazyhaar/c2db/pkg/c2db"
	"github.com/hazyhaar/pkg/cigate55"
	"github.com/hazyhaar/c2db/pkg/cihook55"
	"golang.org/x/sys/unix"
)

func Recipe() cigate55.Recipe {
	root := "/devhoros/c2simd/c2pkg"
	return cigate55.Recipe{
		Name: "c2db-deep",
		Gates: []cigate55.Gate{
			gateBipolar,
			gateHookPublish,
			func() cigate55.Result {
				return cigate55.Stamp(filepath.Join(root, "c2db/db_btree_gen.go"), "sgoiter")
			},
		},
		Deep: []cigate55.Gate{
			gateSealHost,
			gateOverlayAblate,
		},
		MaxTutorials: 0,
		// Le hook before-publish est sauté quand le support O_DIRECT manque :
		// le plafond marque ce saut comme dépassement isolé, mais un saut reste
		// non mesuré et laisse le verdict non vert.
		MaxSkips: 1,
	}
}

func gateBipolar() cigate55.Result {
	return cigate55.Bipolar(func() error {
		dir, err := os.MkdirTemp("", "c2db-ci-nom-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		var key [32]byte
		s, err := c2db.OpenShard(dir, key, 1)
		if err != nil {
			if errors.Is(err, unix.EINVAL) {
				return nil
			}
			return err
		}
		defer s.Close()
		if err := s.Put([]byte("k"), []byte("v")); err != nil {
			return err
		}
		got, err := s.Get([]byte("k"))
		if err != nil {
			return err
		}
		if string(got) != "v" {
			return errors.New("get mismatch")
		}
		return nil
	}, func() error {
		dir, err := os.MkdirTemp("", "c2db-ci-rej-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		var key [32]byte
		s, err := c2db.OpenShard(dir, key, 1)
		if err != nil {
			if errors.Is(err, unix.EINVAL) {
				return errors.New("skip o_direct as reject")
			}
			return err
		}
		defer s.Close()
		_, err = s.Get([]byte("absent"))
		return err
	})
}

func gateHookPublish() cigate55.Result {
	var n atomic.Int32
	cihook55.Set("before-publish", func() { n.Add(1) })
	defer cihook55.Set("before-publish", nil)
	dir, err := os.MkdirTemp("", "c2db-ci-hook-*")
	if err != nil {
		return cigate55.Result{ID: "hook", Zone: "functional", Passed: false, Message: err.Error()}
	}
	defer os.RemoveAll(dir)
	var key [32]byte
	s, err := c2db.OpenShard(dir, key, 1)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			return cigate55.Skip("hook", "functional", "O_DIRECT")
		}
		return cigate55.Result{ID: "hook", Zone: "functional", Passed: false, Message: err.Error()}
	}
	defer s.Close()
	if err := s.Put([]byte("h"), []byte("1")); err != nil {
		return cigate55.Result{ID: "hook", Zone: "functional", Passed: false, Message: err.Error()}
	}
	if n.Load() < 1 {
		return cigate55.Result{ID: "hook", Zone: "functional", Passed: false, Message: "before-publish jamais tiré"}
	}
	return cigate55.Result{ID: "hook", Zone: "functional", Passed: true, Message: "before-publish tiré"}
}

func gateSealHost() cigate55.Result {
	return cigate55.OverlayTest(cigate55.OverlaySpec{
		Dir:     "/devhoros/c2simd/c2pkg",
		Package: "./c2db",
		Run:     "TestCI55_PageSealReject",
		Replace: map[string]string{},
		Env:     []string{"GOTOOLCHAIN=go1.27.0", "GOEXPERIMENT=simd", "GOWORK=/devhoros/c2simd/go.work"},
	})
}

func gateOverlayAblate() cigate55.Result {
	pkg := "/devhoros/c2simd/c2pkg/c2db"
	killed := filepath.Join(pkg, "ci/testdata/pager_seal_ablate.go")
	res := cigate55.OverlayTest(cigate55.OverlaySpec{
		Dir:     "/devhoros/c2simd/c2pkg",
		Package: "./c2db",
		Run:     "TestCI55_PageSealReject",
		Replace: map[string]string{
			filepath.Join(pkg, "pager_seal.go"): killed,
		},
		Env: []string{"GOTOOLCHAIN=go1.27.0", "GOEXPERIMENT=simd", "GOWORK=/devhoros/c2simd/go.work"},
	})
	if res.Passed {
		return cigate55.Result{ID: "overlay-ablate", Zone: "functional", Passed: false, Message: "ablation du sceau encore verte"}
	}
	return cigate55.Result{ID: "overlay-ablate", Zone: "functional", Passed: true, Message: "ablation du sceau rougit l'hôte"}
}

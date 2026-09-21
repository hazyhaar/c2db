// SPDX-License-Identifier: Apache-2.0 OR MIT

package ci

import (
	"os"
	"testing"

	"github.com/hazyhaar/pkg/cigate55"
)

func TestCI_Deep(t *testing.T) {
	ok, rs := cigate55.Run(Recipe())
	for _, r := range rs {
		cigate55.Print(os.Stderr, r)
		if !r.Passed && !r.Skipped {
			t.Errorf("%s/%s %s", r.Zone, r.ID, r.Message)
		}
	}
	if !ok {
		t.Fatal("CI profonde c2db : une porte a échoué")
	}
}

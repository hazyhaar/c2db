package c2uuidv7

import (
	"strings"
	"testing"
)

func TestStrictV7RejectAndRecover(t *testing.T) {
	const good = "01900000-0000-7c03-8000-000000000abc"
	for _, bad := range []string{"", strings.ReplaceAll(good, "-", ""), "urn:uuid:" + good, "{" + good + "}", "01900000-0000-4c03-8000-000000000abc", "01900000-0000-7c03-c000-000000000abc"} {
		if IsV7String(bad) {
			t.Errorf("accepted %q", bad)
		}
	}
	u, err := ParseV7(strings.ToUpper(good))
	if err != nil || u.String() != good {
		t.Fatalf("%v %v", u, err)
	}
	stamp, err := ParseV7Time(good)
	if err != nil || stamp.UnixMilli() != int64(u.TimestampMs()) {
		t.Fatalf("%v %v", stamp, err)
	}
}

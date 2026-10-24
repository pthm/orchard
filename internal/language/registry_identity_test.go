package language

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	refulid "github.com/oklog/ulid/v2"
	refxid "github.com/rs/xid"
	refksuid "github.com/segmentio/ksuid"
	"github.com/zclconf/go-cty/cty"
)

var at = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func atArg() cty.Value { return cty.StringVal(at.Format(time.RFC3339)) }

// TestIDsParse checks each ID with the format's reference library and
// that the embedded time is the one passed in.
func TestIDsParse(t *testing.T) {
	u, err := uuid.Parse(callSeeded(t, "x", "uuidv7", atArg()).AsString())
	if err != nil || u.Version() != 7 || u.Variant() != uuid.RFC4122 {
		t.Fatalf("uuidv7: %v %v %v", u, err, u.Version())
	}
	if sec, nsec := u.Time().UnixTime(); time.Unix(sec, nsec).UTC() != at {
		t.Errorf("uuidv7 time = %v, want %v", time.Unix(sec, nsec).UTC(), at)
	}
	if v4, err := uuid.Parse(callSeeded(t, "x", "uuid").AsString()); err != nil || v4.Version() != 4 {
		t.Errorf("uuid: %v %v", v4, err)
	}

	l, err := refulid.ParseStrict(callSeeded(t, "x", "ulid", atArg()).AsString())
	if err != nil || refulid.Time(l.Time()).UTC() != at {
		t.Errorf("ulid: %v %v", l, err)
	}

	k, err := refksuid.Parse(callSeeded(t, "x", "ksuid", atArg()).AsString())
	if err != nil || k.Time().UTC() != at {
		t.Errorf("ksuid: %v %v (time %v)", k, err, k.Time())
	}

	x, err := refxid.FromString(callSeeded(t, "x", "xid", atArg()).AsString())
	if err != nil || x.Time().UTC() != at {
		t.Errorf("xid: %v %v", x, err)
	}

	oid := callSeeded(t, "x", "objectid", atArg()).AsString()
	if sec, err := strconv.ParseInt(oid[:8], 16, 64); len(oid) != 24 || err != nil || sec != at.Unix() {
		t.Errorf("objectid %q", oid)
	}

	tid := callSeeded(t, "x", "typeid", cty.StringVal("user"), atArg()).AsString()
	suffix, ok := strings.CutPrefix(tid, "user_")
	if !ok || len(suffix) != 26 || suffix != strings.ToLower(suffix) {
		t.Fatalf("typeid %q", tid)
	}
	// A TypeID suffix is a ULID-encoded v7 UUID.
	tl, err := refulid.ParseStrict(strings.ToUpper(suffix))
	if err != nil {
		t.Fatalf("typeid suffix: %v", err)
	}
	if tu := uuid.UUID(tl); tu.Version() != 7 || refulid.Time(tl.Time()).UTC() != at {
		t.Errorf("typeid suffix isn't a v7 UUID of %v: %v", at, tu)
	}
	if bare := callSeeded(t, "x", "typeid", cty.StringVal("")).AsString(); len(bare) != 26 {
		t.Errorf("typeid with no prefix = %q", bare)
	}
	if _, err := RandomFuncs(NewRand(1, "x"))["typeid"].Call([]cty.Value{cty.StringVal("User")}); err == nil {
		t.Error("typeid with an uppercase prefix should fail")
	}

	if n := callSeeded(t, "x", "nanoid").AsString(); !regexp.MustCompile(`^[A-Za-z0-9_-]{21}$`).MatchString(n) {
		t.Errorf("nanoid %q", n)
	}
	if n := callSeeded(t, "x", "nanoid", num(10)).AsString(); len(n) != 10 {
		t.Errorf("nanoid(10) %q", n)
	}

	for _, n := range []int64{2, 24, 32} {
		c := callSeeded(t, "x", "cuid2", num(n)).AsString()
		if !regexp.MustCompile(`^[a-z][a-z0-9]+$`).MatchString(c) || int64(len(c)) != n {
			t.Errorf("cuid2(%d) = %q", n, c)
		}
	}
}

func TestSnowflake(t *testing.T) {
	fn := RandomFuncs(NewRand(1, "x"))["snowflake"]
	const discord = 1420070400000
	var last int64
	for i := range 10000 {
		v, err := fn.Call([]cty.Value{num(5), num(discord)})
		if err != nil {
			t.Fatal(err)
		}
		id, err := strconv.ParseInt(v.AsString(), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if id <= last {
			t.Fatalf("id %d (%d) not above the previous one %d", i, id, last)
		}
		last = id
		if node := id >> 12 & 1023; node != 5 {
			t.Fatalf("node bits = %d, want 5", node)
		}
	}
	if ms := last>>22 + discord; time.Since(time.UnixMilli(ms)) > time.Minute || time.Until(time.UnixMilli(ms)) > time.Minute {
		t.Errorf("timestamp %v isn't now", time.UnixMilli(ms))
	}
	if _, err := fn.Call([]cty.Value{num(1024)}); err == nil {
		t.Error("node 1024 should fail")
	}
}

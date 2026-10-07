package contract

import (
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
)

func TestPageResolve(t *testing.T) {
	cases := []struct {
		in         page
		limit, off int
		badRequest bool
	}{
		{page{}, 25, 0, false},
		{page{Limit: 10, Offset: 30}, 10, 30, false},
		{page{Limit: 1000}, 100, 0, false},
		{page{Limit: -1}, 0, 0, true},
		{page{Offset: -5}, 0, 0, true},
	}
	for _, tc := range cases {
		limit, off, err := tc.in.resolve()
		if tc.badRequest {
			if codeOf(err) != dashcontract.CodeBadRequest {
				t.Errorf("%+v: err = %v, want BAD_REQUEST", tc.in, err)
			}
			continue
		}
		if err != nil || limit != tc.limit || off != tc.off {
			t.Errorf("%+v: got %d %d %v, want %d %d", tc.in, limit, off, err, tc.limit, tc.off)
		}
	}
}

func TestEmptyIfNil(t *testing.T) {
	if m := emptyIfNil(nil); m == nil || len(m) != 0 {
		t.Errorf("nil: %v", m)
	}
	in := map[string]string{"a": "b"}
	if m := emptyIfNil(in); m["a"] != "b" {
		t.Errorf("kept: %v", m)
	}
}

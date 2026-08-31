package catalog

import (
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func TestClassifyErr(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{404, "404 not found"},
		{401, "access denied"},
		{403, "access denied"},
		{500, "registry error 500"},
	}
	for _, c := range cases {
		msg := classifyErr(&transport.Error{StatusCode: c.code})
		if !strings.Contains(msg, c.want) {
			t.Fatalf("status %d -> %q, want contains %q", c.code, msg, c.want)
		}
	}
}

func TestVerify(t *testing.T) {
	// stub the network HEAD: python ok, node 403, foo 404
	orig := headManifest
	defer func() { headManifest = orig }()
	headManifest = func(ref name.Reference) error {
		switch {
		case strings.Contains(ref.String(), "python"):
			return nil
		case strings.Contains(ref.String(), "node"):
			return &transport.Error{StatusCode: 403}
		default:
			return &transport.Error{StatusCode: 404}
		}
	}
	if code := Verify([]string{"cgr.dev/o/python:3.12"}); code != 0 {
		t.Fatalf("python should pass, got %d", code)
	}
	if code := Verify([]string{"cgr.dev/o/node:22"}); code != 1 {
		t.Fatalf("node (403) should fail, got %d", code)
	}
	if code := Verify([]string{"cgr.dev/o/foo:1"}); code != 1 {
		t.Fatalf("foo (404) should fail, got %d", code)
	}
}

package expr

import (
	"reflect"
	"strings"
	"testing"
)

func testEnv() Env {
	return Env{
		"steps": map[string]any{
			"check": map[string]any{"failed": true, "succeeded": false, "output": map[string]any{"status": int64(503), "tags": []any{"a", "b"}}},
			"ok":    map[string]any{"failed": false, "succeeded": true, "output": "fine"},
		},
		"date": "2026-11-09",
		"item": map[string]any{"url": "https://example.com"},
	}
}

func TestString(t *testing.T) {
	env := testEnv()
	cases := []struct {
		in   string
		want any
	}{
		{"plain", "plain"},
		{"${steps.check.output.status}", int64(503)},
		{"  ${ steps.check.output.tags }  ", []any{"a", "b"}},
		{"status ${steps.check.output.status} on ${date}", "status 503 on 2026-11-09"},
		{"tags: ${steps.check.output.tags}", `tags: ["a","b"]`},
		{"${steps.check.failed || steps.ok.failed}", true},
		{"${steps.check.output.status >= 500}", true},
		{"${item.url}/health", "https://example.com/health"},
		{"${steps.ok.output}!", "fine!"},
	}
	for _, tc := range cases {
		got, err := String(tc.in, env)
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestErrors(t *testing.T) {
	for _, in := range []string{"${steps.check.output.status +}", "x ${1 +} y"} {
		if _, err := String(in, testEnv()); err == nil || !strings.Contains(err.Error(), "expression") {
			t.Errorf("%q: expected an expression error, got %v", in, err)
		}
	}
}

func TestValue(t *testing.T) {
	in := map[string]any{
		"a": "${steps.check.output.status}",
		"b": []any{"${date}", 3},
		"c": map[string]string{"h": "v-${date}"},
	}
	got, err := Value(in, testEnv())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"a": int64(503),
		"b": []any{"2026-11-09", 3},
		"c": map[string]string{"h": "v-2026-11-09"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestTruthy(t *testing.T) {
	falsy := []any{nil, false, "", "false", "0", 0, int64(0), 0.0, []any{}, map[string]any{}}
	for _, v := range falsy {
		if Truthy(v) {
			t.Errorf("Truthy(%#v) = true", v)
		}
	}
	truthy := []any{true, "yes", 1, int64(2), 0.5, []any{1}, map[string]any{"a": 1}}
	for _, v := range truthy {
		if !Truthy(v) {
			t.Errorf("Truthy(%#v) = false", v)
		}
	}
}

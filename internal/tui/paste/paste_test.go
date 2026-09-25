package paste

import "testing"

func TestLines(t *testing.T) {
	cases := map[string]string{
		"one\r\ntwo\rthree\nfour":   "one\ntwo\nthree\nfour",
		"a\tb":                      "a    b",
		"red\x1b[31m\x07\x7f\u0085": "red[31m",
	}
	for in, want := range cases {
		if got := Lines(in); got != want {
			t.Errorf("Lines(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLine(t *testing.T) {
	cases := map[string]string{
		"  sk-ant-api03-abc\n":      "sk-ant-api03-abc",
		"sk-\r\nant\x1b]52;c;x\x07": "sk-ant]52;c;x",
		"model\tname":               "model    name",
	}
	for in, want := range cases {
		if got := Line(in); got != want {
			t.Errorf("Line(%q) = %q, want %q", in, got, want)
		}
	}
}

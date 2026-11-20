package main

import (
	"testing"
)

func TestParsePprofBaseFromArgs(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, defaultPprofBaseURL},
		{[]string{"--configfile", "x"}, defaultPprofBaseURL},
		{[]string{"--profile=:6060"}, "http://127.0.0.1:6060"},
		{[]string{"--profile", ":18577"}, "http://127.0.0.1:18577"},
		{[]string{"--profile=0.0.0.0:6060"}, "http://127.0.0.1:6060"},
		{[]string{"--profile=127.0.0.1:9090"}, "http://127.0.0.1:9090"},
	}
	for _, tc := range cases {
		got := parsePprofBaseFromArgs(tc.args)
		if got != tc.want {
			t.Fatalf("parsePprofBaseFromArgs(%v) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

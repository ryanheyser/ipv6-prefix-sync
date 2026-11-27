package main

import (
	"strings"
	"testing"
)

const sample = `260100c20a0074c4021c73fffe000099 03 40 00 80 eno1
fd000011000000000000000000010011 03 40 00 80 eno1
fe80000000000000021c73fffe000099 03 40 20 80 eno1
260100c20a0074c40000000000010011 03 40 00 80 eno1
260100c20a0074c50000000000010011 03 40 20 80 old0
fd002550000000000000000000000001 03 80 00 80 vxlan.calico
`

func TestPrefix(t *testing.T) {
	p, err := prefix60(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	got, err := metallb64(p)
	if err != nil || got != "2601:c2:a00:74cf" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTwoPrefixesFails(t *testing.T) {
	in := sample + "20010558601100300000000000000001 03 40 00 80 eno2\n"
	if _, err := prefix60(strings.NewReader(in)); err == nil {
		t.Fatal("expected error for two /60s")
	}
}

func TestNoGUAFails(t *testing.T) {
	if _, err := prefix60(strings.NewReader("fd000011000000000000000000010011 03 40 00 80 eno1\n")); err == nil {
		t.Fatal("expected error with no GUA")
	}
}

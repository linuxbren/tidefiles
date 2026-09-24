package main

import "testing"

func TestProtoFromReply(t *testing.T) {
	cases := map[string]gfxProto{
		"\x1b_Gi=31;OK\x1b\\\x1b[?62;c":          protoKitty,
		"\x1b[?62;4;9;22c":                       protoSixel,
		"\x1b[?1;2c":                             protoBlocks,
		"":                                       protoBlocks,
		"\x1b_Gi=31;ENOTSUPPORTED\x1b\\\x1b[?6c": protoBlocks,
	}
	for in, want := range cases {
		if got := protoFromReply(in); got != want {
			t.Errorf("protoFromReply(%q) = %v, want %v", in, got, want)
		}
	}
}

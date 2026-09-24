package omarchy

import "testing"

func TestFromMapUsesResolverKeys(t *testing.T) {
	p, ok := fromMap(parseFlatTOML(`
accent = "#82FB9C"
background = "#0B0C16"   
foreground = "#ddf7ff"
muted = "#2d3450"
`))
	if !ok || p.Accent != "#82FB9C" || p.Background != "#0B0C16" || p.Muted != "#2d3450" {
		t.Fatalf("unexpected palette %+v ok=%v", p, ok)
	}
}

func TestFromMapRejectsMissingColors(t *testing.T) {
	if _, ok := fromMap(map[string]string{"accent": "#fff000"}); ok {
		t.Fatal("expected failure without background/foreground")
	}
}
